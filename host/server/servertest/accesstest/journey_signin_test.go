package accesstest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"

	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"

	"github.com/vsriram/simple-host/host/web"
)

// withEmailSignIn flips the package-level EMAIL_SIGNIN switch for the
// duration of a test and restores it afterward, so tests never leak state
// into one another (the switch is a single process-wide var).
func withEmailSignIn(t *testing.T, on bool) {
	t.Helper()
	previous := web.EmailSignInEnabled
	web.SetEmailSignIn(on)
	t.Cleanup(func() { web.SetEmailSignIn(previous) })
}

// newSignupAddr returns a fresh address with a live dashboard code.
func newSignupAddr(t *testing.T, a *App, label string) (addr, code string) {
	t.Helper()
	addr = label + "-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.com"
	code = "515151"
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, addr, code, lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	return addr, code
}

func signupOf(t *testing.T, a *App, addr string) signupRow {
	t.Helper()
	var r signupRow
	if err := a.Database.QueryRow(`SELECT signup_source, signup_agent, signup_method, signup_inferred FROM users WHERE username = $1`, addr).
		Scan(&r.source, &r.agent, &r.method, &r.inferred); err != nil {
		t.Fatal(err)
	}
	return r
}

type signupRow struct {
	source, agent, method sql.NullString
	inferred              bool
}

// TestAccountExportShowsRealLoginForPasswordAccount covers the
// adversary-review fix (account_data.go's accountDocuments, the "Download
// my data" export): a self sign-up's account.json must carry its own login
// alongside account_name, never just the synthetic "pw-..." value.
func TestAccountExportShowsRealLoginForPasswordAccount(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)

	raw := fmt.Sprintf("  Export-Me+%d@Example.com  ", time.Now().UnixNano())
	wantLogin := strings.ToLower(strings.TrimSpace(raw))
	signup := a.Do(t, "POST", "/v1/auth/password/signup", rt.JSONBody(map[string]string{"username": raw, "password": "first password"}), map[string]string{"Content-Type": "application/json"})
	if signup.Status != 200 {
		t.Fatalf("signup: %d %s", signup.Status, signup.Body)
	}
	id := signup.JSON(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	key := signup.JSON(t)["api_key"].(string)
	syntheticUsername, _ := signup.JSON(t)["username"].(string)
	if syntheticUsername == wantLogin {
		t.Fatalf("signup did not use a synthetic username")
	}

	r := a.Do(t, "GET", "/v1/me/export.zip", nil, map[string]string{"X-API-Key": key})
	if r.Status != 200 {
		t.Fatalf("export: %d %s", r.Status, r.Body)
	}
	files := acctZipFiles(t, r.Body)
	var account map[string]any
	if err := json.Unmarshal([]byte(acctFileUnder(t, files, "/account.json")), &account); err != nil {
		t.Fatal(err)
	}
	if account["account_name"] != syntheticUsername {
		t.Fatalf("account.json account_name: got %v want %q", account["account_name"], syntheticUsername)
	}
	if account["login"] != wantLogin {
		t.Fatalf("account.json login: got %v want %q (a password account's export must not show only the synthetic username)", account["login"], wantLogin)
	}
	if _, has := account["email"]; has {
		t.Errorf("account.json has an email field for a password account: %v", account["email"])
	}
}

func TestChangeOwnPassword(t *testing.T) {
	a := NewApp(t)
	post := func(path string, body map[string]string, key string) Resp {
		return a.Do(t, "POST", path, rt.JSONBody(body), map[string]string{"Content-Type": "application/json", "X-API-Key": key})
	}
	name := fmt.Sprintf("change-password-%d", time.Now().UnixNano())
	a.Users.SetPasswordSignIn(true, true)
	signup := post("/v1/auth/password/signup", map[string]string{"username": name, "password": "first password"}, "")
	if signup.Status != 200 {
		t.Fatalf("signup: %d %s", signup.Status, signup.Body)
	}
	id := signup.JSON(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	key := signup.JSON(t)["api_key"].(string)

	// Discovery: has_password is on only once there is a password to change.
	if r := a.Do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.Status != 200 || r.JSON(t)["has_password"] != true {
		t.Fatalf("has_password: %d %s", r.Status, r.Body)
	}

	// Wrong current password: 403 (not 401, which both UIs read as an
	// expired key and sign the browser out), counted on the same limiter as
	// sign-in, and the key used to ask is untouched.
	wrong := post("/v1/me/password", map[string]string{"current_password": "not it", "new_password": "second password"}, key)
	if wrong.Status != 403 || wrong.JSON(t)["code"] != "bad_credentials" {
		t.Fatalf("wrong current password: %d %s", wrong.Status, wrong.Body)
	}

	// A connector access token carries this account's full REST permissions
	// (BearerAuth) but must never be able to change the password itself.
	redirect := "https://client.example/callback"
	client := a.RegisterClient(t, redirect)
	verifier, challenge := NewVerifier()
	code := CodeFrom(t, a.Consent(t, AuthorizeQuery(client, redirect, challenge), key, "allow"))
	tok := a.Form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {client}, "code_verifier": {verifier},
	}, nil)
	if tok.Status != 200 {
		t.Fatalf("token: %d %s", tok.Status, tok.Body)
	}
	access := tok.JSON(t)["access_token"].(string)
	connectorTry := a.Do(t, "POST", "/v1/me/password", rt.JSONBody(map[string]string{"current_password": "first password", "new_password": "second password"}),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + access})
	if connectorTry.Status != 403 || connectorTry.JSON(t)["code"] != "connector_not_allowed" {
		t.Fatalf("connector token changed password: %d %s", connectorTry.Status, connectorTry.Body)
	}

	// Correct current password and a valid new one: success.
	changed := post("/v1/me/password", map[string]string{"current_password": "first password", "new_password": "second password"}, key)
	if changed.Status != 204 {
		t.Fatalf("change: %d %s", changed.Status, changed.Body)
	}

	// The old password no longer signs in; the new one does.
	loginWith := func(password string) Resp {
		return post("/v1/auth/password", map[string]string{"login": name, "password": password}, "")
	}
	if r := loginWith("first password"); r.Status != 401 {
		t.Fatalf("old password still works: %d %s", r.Status, r.Body)
	}
	if r := loginWith("second password"); r.Status != 200 {
		t.Fatalf("new password: %d %s", r.Status, r.Body)
	}

	// The API key used to change the password still works: changing a
	// password never revokes keys, sessions or connector tokens.
	if r := a.Do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.Status != 200 {
		t.Fatalf("key after change: %d %s", r.Status, r.Body)
	}
}

// GET /v1/handles/check: available, taken, reserved, malformed; your own
// handle is yours; a cross-site browser request is refused.
func TestHandleCheck(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "checker")
	own := p.Handle
	check := func(h string, hdr map[string]string) Resp {
		return a.Do(t, http.MethodGet, "/v1/handles/check?handle="+h, nil, hdr)
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 36)
	if r := check("free-"+stamp, nil); r.Status != 200 || r.JSON(t)["available"] != true {
		t.Fatalf("free: %d %s", r.Status, r.Body)
	}
	if r := check(own, nil); r.JSON(t)["available"] != false || r.JSON(t)["code"] != "handle_taken" {
		t.Fatalf("taken: %d %s", r.Status, r.Body)
	}
	if r := check(own, map[string]string{"X-API-Key": p.Key}); r.JSON(t)["available"] != true {
		t.Fatalf("own: %d %s", r.Status, r.Body)
	}
	if r := check("admin", nil); r.JSON(t)["code"] != "handle_reserved" {
		t.Fatalf("reserved: %d %s", r.Status, r.Body)
	}
	if r := check("Not_OK", nil); r.JSON(t)["code"] != "invalid_handle" {
		t.Fatalf("invalid: %d %s", r.Status, r.Body)
	}
	if r := check("x", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.Status != http.StatusForbidden {
		t.Fatalf("cross-site: %d", r.Status)
	}
}

func TestPasswordAccounts(t *testing.T) {
	a := NewApp(t)
	post := func(path string, body map[string]string, key string) Resp {
		return a.Do(t, "POST", path, rt.JSONBody(body), map[string]string{"Content-Type": "application/json", "X-API-Key": key})
	}
	name := fmt.Sprintf("password-%d", time.Now().UnixNano())
	a.Users.SetPasswordSignIn(true, true)
	signup := post("/v1/auth/password/signup", map[string]string{"username": name, "password": "first password"}, "")
	if signup.Status != 200 {
		t.Fatalf("signup: %d %s", signup.Status, signup.Body)
	}
	id := signup.JSON(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	key := signup.JSON(t)["api_key"].(string)
	if r := a.Do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.Status != 200 {
		t.Fatalf("me: %d %s", r.Status, r.Body)
	}
	login := func(password, next string) Resp {
		return post("/v1/auth/password", map[string]string{"login": "  " + strings.ToUpper(name) + " ", "password": password, "new_password": next}, "")
	}
	r := login("first password", "")
	if r.Status != 200 {
		t.Fatalf("signin: %d %s", r.Status, r.Body)
	}
	key = r.JSON(t)["api_key"].(string)
	if r := a.Do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.Status != 200 {
		t.Fatalf("password key: %d %s", r.Status, r.Body)
	}
	wrong := login("wrong password", "")
	unknown := post("/v1/auth/password", map[string]string{"login": name + "-missing", "password": "wrong password"}, "")
	if wrong.Status != 401 || unknown.Status != 401 || string(wrong.Body) != string(unknown.Body) {
		t.Fatalf("credential errors differ: %d %s / %d %s", wrong.Status, wrong.Body, unknown.Status, unknown.Body)
	}
	// Existing accounts with no password are indistinguishable too.
	noPassword := a.NewPerson(t, "passwordless")
	missing := post("/v1/auth/password", map[string]string{"login": noPassword.Email, "password": "wrong password"}, "")
	if missing.Status != 401 || string(missing.Body) != string(wrong.Body) {
		t.Fatalf("passwordless: %d %s", missing.Status, missing.Body)
	}
	// A password-issued key completes the unchanged connector consent flow.
	redirect := "https://client.example/callback"
	client := a.RegisterClient(t, redirect)
	_, challenge := NewVerifier()
	q := AuthorizeQuery(client, redirect, challenge)
	q.Set("resource", a.Srv.URL+"/mcp")
	if code := CodeFrom(t, a.Consent(t, q, key, "allow")); code == "" {
		t.Fatal("no connector code")
	}
	// Admin-created credentials require replacement before issuing a key.
	created := post("/v1/admin/password-users", map[string]string{"username": name + "-admin"}, a.Admin)
	if created.Status != 200 {
		t.Fatalf("admin create: %d %s", created.Status, created.Body)
	}
	data := created.JSON(t)
	adminID := data["id"].(string)
	temp := data["temporary_password"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", adminID) })
	for _, target := range []struct{ id, username, password string }{{adminID, name + "-admin", temp}, {id, name, "reset password"}} {
		if target.id == id {
			r := post("/v1/admin/users/"+id+"/password", map[string]string{"password": target.password}, a.Admin)
			if r.Status != 200 {
				t.Fatalf("reset: %d %s", r.Status, r.Body)
			}
		}
		body := map[string]string{"login": target.username, "password": target.password}
		r := post("/v1/auth/password", body, "")
		if r.Status != 403 || r.JSON(t)["code"] != "password_change_required" {
			t.Fatalf("must change: %d %s", r.Status, r.Body)
		}
		body["new_password"] = target.password
		if r := post("/v1/auth/password", body, ""); r.Status != 400 {
			t.Fatalf("same password accepted: %d", r.Status)
		}
		body["new_password"] = "replacement password"
		r = post("/v1/auth/password", body, "")
		if r.Status != 200 {
			t.Fatalf("change: %d %s", r.Status, r.Body)
		}
		delete(body, "new_password")
		if r := post("/v1/auth/password", body, ""); r.Status != 401 {
			t.Fatalf("old temp works: %d", r.Status)
		}
		body["password"] = "replacement password"
		if r := post("/v1/auth/password", body, ""); r.Status != 200 {
			t.Fatalf("must_change not cleared: %d %s", r.Status, r.Body)
		}
	}
}

// TestPasswordSelfSignupUsesSyntheticUsername (owner decision 2026-11-05,
// redesigned after review): self sign-up accepts any login a person types
// -- no character class, no email-only allowlist -- but never stores it as
// users.username. A self-chosen login never proved it controls that
// address, even one shaped like an email, so users.username becomes a
// synthetic, never-shown value instead (CreatePasswordAccount); the typed
// login lives only in account_passwords.login, surfaced back on the signup
// response and GET /v1/me as "login". Sign-in still works by a
// differently-cased, differently-padded form of the same login, and a
// second signup differing only in case collides against
// account_passwords.login, not a second account. Length and control
// characters are a sanity limit, not format gating.
func TestPasswordSelfSignupUsesSyntheticUsername(t *testing.T) {
	a := NewApp(t)
	post := func(path string, body map[string]string, key string) Resp {
		return a.Do(t, "POST", path, rt.JSONBody(body), map[string]string{"Content-Type": "application/json", "X-API-Key": key})
	}
	a.Users.SetPasswordSignIn(true, true)

	stamp := time.Now().UnixNano()
	raw := fmt.Sprintf("  Victim+%d@Example.com  ", stamp)
	wantLogin := strings.ToLower(strings.TrimSpace(raw))

	signup := post("/v1/auth/password/signup", map[string]string{"username": raw, "password": "first password"}, "")
	if signup.Status != 200 {
		t.Fatalf("free-form signup: %d %s", signup.Status, signup.Body)
	}
	id := signup.JSON(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })

	gotUsername, _ := signup.JSON(t)["username"].(string)
	if gotUsername == wantLogin || strings.Contains(gotUsername, "@") {
		t.Fatalf("self sign-up stored the typed login as users.username: %q", gotUsername)
	}
	if got := signup.JSON(t)["login"]; got != wantLogin {
		t.Fatalf("signup response login: got %q want %q", got, wantLogin)
	}
	key := signup.JSON(t)["api_key"].(string)
	me := a.Do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}).JSON(t)
	if me["login"] != wantLogin {
		t.Fatalf("GET /v1/me login: got %v want %q", me["login"], wantLogin)
	}

	// A differently-cased, differently-padded form of the same login signs
	// into the same account.
	login := post("/v1/auth/password", map[string]string{"login": "  " + strings.ToUpper(raw) + "  ", "password": "first password"}, "")
	if login.Status != 200 {
		t.Fatalf("sign-in with re-cased/padded login: %d %s", login.Status, login.Body)
	}
	if login.JSON(t)["id"] != id {
		t.Fatalf("signed into a different account: got %v want %v", login.JSON(t)["id"], id)
	}

	// A second signup differing only in case collides against
	// account_passwords.login, not a second account.
	dup := post("/v1/auth/password/signup", map[string]string{"username": strings.ToUpper(raw), "password": "another password"}, "")
	if dup.Status != 409 || dup.JSON(t)["code"] != "username_taken" {
		t.Fatalf("case-only duplicate not refused: %d %s", dup.Status, dup.Body)
	}

	// Sanity limits, not format gating: empty (after trim) and over-long are
	// still refused, with the new, non-format message.
	empty := post("/v1/auth/password/signup", map[string]string{"username": "   ", "password": "another password"}, "")
	if empty.Status != 400 || empty.JSON(t)["code"] != "invalid_username" {
		t.Fatalf("empty username accepted: %d %s", empty.Status, empty.Body)
	}
	tooLong := post("/v1/auth/password/signup", map[string]string{"username": strings.Repeat("a", 255), "password": "another password"}, "")
	if tooLong.Status != 400 || tooLong.JSON(t)["code"] != "invalid_username" {
		t.Fatalf("over-long username accepted: %d %s", tooLong.Status, tooLong.Body)
	}
}

// Issued accounts and Google sign-ups carry their source too.
func TestSignupSourceIssuedAndGoogle(t *testing.T) {
	a := NewApp(t)
	prefix := "sui" + strconv.FormatInt(time.Now().UnixNano()%1e6, 36)
	r := a.At(t, "POST", rt.SiteDomain, "/v1/admin/users", map[string]any{"count": 1, "prefix": prefix}, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 201 {
		t.Fatalf("issue: %d %s", r.Status, r.Body)
	}
	name := r.JSON(t)["created"].([]any)[0].(map[string]any)["username"].(string)
	if got := signupOf(t, a, name); got.source.String != "admin" || got.method.String != "issued" {
		t.Fatalf("issued: %+v", got)
	}

	ctx := context.Background()
	claude := a.RegisterNamedClient(t, "Claude")
	for _, c := range []struct{ returnTo, want string }{
		{"https://simple-host.test/oauth/authorize?client_id=" + claude + "&cn=x", "connector:Claude"},
		{"https://simple-host.test/oauth/authorize?client_id=unknown&cn=x", "website"},
		{"https://simple-host.test/dashboard?cn=x", "website"},
	} {
		s := web.SignupForOwnerReturnTo(ctx, a.Database, httptest.NewRequest("GET", "/", nil), c.returnTo, "google")
		if s.Source != c.want || s.Method != "google" {
			t.Errorf("%s: %+v", c.returnTo, s)
		}
	}
	if s := web.SignupForOwnerReturnTo(ctx, a.Database, httptest.NewRequest("GET", "/", nil), "https://simple-host.test/dashboard?cn=x", "github"); s.Source != "website" || s.Method != "github" {
		t.Errorf("github: %+v", s)
	}
}

// Every way POST /v1/auth/verify creates an account records where it came
// from, once; a sign-in to an existing account never changes it.
func TestSignupSourceRecordedAtCreation(t *testing.T) {
	a := NewApp(t)
	chatgpt := a.RegisterNamedClient(t, "ChatGPT")
	page := map[string]string{"Sec-Fetch-Site": "same-origin"}

	cases := []struct {
		label   string
		body    map[string]any
		headers map[string]string
		want    signupRow
	}{
		{"web", map[string]any{"name": "dashboard sign-in"}, page,
			signupRow{source: ns("website"), method: ns("email")}},
		// A page on the app's own address: the Origin must match the public
		// base URL (here the plain http test server), and the account sign-in
		// gate compares it with the request's own scheme, so say http.
		{"web-origin", nil, map[string]string{"Origin": a.Srv.URL, "X-Forwarded-Proto": "http"},
			signupRow{source: ns("website"), method: ns("email")}},
		{"conn", map[string]any{"signup_client": chatgpt}, page,
			signupRow{source: ns("connector:ChatGPT"), method: ns("email")}},
		// An unknown app is not trusted to name itself: a page sign-up.
		{"conn-unknown", map[string]any{"signup_client": "nope"}, page,
			signupRow{source: ns("website"), method: ns("email")}},
		{"agent-client", nil, map[string]string{"X-Simple-Host-Client": "Codex CLI 1.2", "User-Agent": "curl/8.5.0"},
			signupRow{source: ns("agent"), agent: ns("Codex CLI 1.2"), method: ns("email")}},
		{"agent-ua", nil, map[string]string{"User-Agent": "python-requests/2.31.0"},
			signupRow{source: ns("agent"), agent: ns("python-requests"), method: ns("email")}},
		// A cross-site browser request is not one of the site's pages.
		{"agent-xsite", nil, map[string]string{"Sec-Fetch-Site": "cross-site", "User-Agent": "Mozilla/5.0 (X11)"},
			signupRow{source: ns("agent"), agent: ns("browser"), method: ns("email")}},
	}
	for _, c := range cases {
		addr, code := newSignupAddr(t, a, "su-"+c.label)
		body := map[string]any{"email": addr, "code": code}
		for k, v := range c.body {
			body[k] = v
		}
		r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", body, c.headers)
		if r.Status != 200 || r.JSON(t)["created"] != true {
			t.Fatalf("%s: verify %d %s", c.label, r.Status, r.Body)
		}
		if got := signupOf(t, a, addr); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.label, got, c.want)
		}
	}

	// An account from before tracking signs in again as an agent: still NULL.
	old := a.NewPerson(t, "su-old")
	lt, _ := auth.GenerateAPIKey()
	if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, old.Email, "626262", lt, time.Now().Add(5*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", a.AppHost(), "/v1/auth/verify", map[string]any{"email": old.Email, "code": "626262"}, map[string]string{"User-Agent": "curl/8"}); r.Status != 200 || r.JSON(t)["created"] == true {
		t.Fatalf("old account sign-in: %d %s", r.Status, r.Body)
	}
	if got := signupOf(t, a, old.Email); got.source.Valid || got.agent.Valid || got.method.Valid || got.inferred {
		t.Fatalf("existing account touched: %+v", got)
	}
	// SetSignup never overwrites a recorded source.
	id := old.ID
	_ = accountsrepo.SetSignup(context.Background(), a.Database, id, model.Signup{Source: "website", Method: "email"})
	_ = accountsrepo.SetSignup(context.Background(), a.Database, id, model.Signup{Source: "agent", Agent: "curl"})
	if got := signupOf(t, a, old.Email); got.source.String != "website" || got.agent.Valid {
		t.Fatalf("second SetSignup rewrote the source: %+v", got)
	}
}

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
