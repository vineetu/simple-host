package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
	accountsrepo "github.com/vsriram/simple-host/internal/repository/accounts"
)

func TestPasswordAccounts(t *testing.T) {
	a := newConnectorApp(t)
	post := func(path string, body map[string]string, key string) resp {
		return a.do(t, "POST", path, jsonBody(body), map[string]string{"Content-Type": "application/json", "X-API-Key": key})
	}
	name := fmt.Sprintf("password-%d", time.Now().UnixNano())
	a.users.SetPasswordSignIn(true, true)
	signup := post("/v1/auth/password/signup", map[string]string{"username": name, "password": "first password"}, "")
	if signup.status != 200 {
		t.Fatalf("signup: %d %s", signup.status, signup.body)
	}
	id := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })
	key := signup.json(t)["api_key"].(string)
	if r := a.do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.status != 200 {
		t.Fatalf("me: %d %s", r.status, r.body)
	}
	login := func(password, next string) resp {
		return post("/v1/auth/password", map[string]string{"login": "  " + strings.ToUpper(name) + " ", "password": password, "new_password": next}, "")
	}
	r := login("first password", "")
	if r.status != 200 {
		t.Fatalf("signin: %d %s", r.status, r.body)
	}
	key = r.json(t)["api_key"].(string)
	if r := a.do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.status != 200 {
		t.Fatalf("password key: %d %s", r.status, r.body)
	}
	wrong := login("wrong password", "")
	unknown := post("/v1/auth/password", map[string]string{"login": name + "-missing", "password": "wrong password"}, "")
	if wrong.status != 401 || unknown.status != 401 || string(wrong.body) != string(unknown.body) {
		t.Fatalf("credential errors differ: %d %s / %d %s", wrong.status, wrong.body, unknown.status, unknown.body)
	}
	// Existing accounts with no password are indistinguishable too.
	noPassword := a.newPerson(t, "passwordless")
	missing := post("/v1/auth/password", map[string]string{"login": noPassword.email, "password": "wrong password"}, "")
	if missing.status != 401 || string(missing.body) != string(wrong.body) {
		t.Fatalf("passwordless: %d %s", missing.status, missing.body)
	}
	// A password-issued key completes the unchanged connector consent flow.
	redirect := "https://client.example/callback"
	client := a.registerClient(t, redirect)
	_, challenge := newVerifier()
	q := authorizeQuery(client, redirect, challenge)
	q.Set("resource", a.srv.URL+"/mcp")
	if code := codeFrom(t, a.consent(t, q, key, "allow")); code == "" {
		t.Fatal("no connector code")
	}
	// Admin-created credentials require replacement before issuing a key.
	created := post("/v1/admin/password-users", map[string]string{"username": name + "-admin"}, a.admin)
	if created.status != 200 {
		t.Fatalf("admin create: %d %s", created.status, created.body)
	}
	data := created.json(t)
	adminID := data["id"].(string)
	temp := data["temporary_password"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", adminID) })
	for _, target := range []struct{ id, username, password string }{{adminID, name + "-admin", temp}, {id, name, "reset password"}} {
		if target.id == id {
			r := post("/v1/admin/users/"+id+"/password", map[string]string{"password": target.password}, a.admin)
			if r.status != 200 {
				t.Fatalf("reset: %d %s", r.status, r.body)
			}
		}
		body := map[string]string{"login": target.username, "password": target.password}
		r := post("/v1/auth/password", body, "")
		if r.status != 403 || r.json(t)["code"] != "password_change_required" {
			t.Fatalf("must change: %d %s", r.status, r.body)
		}
		body["new_password"] = target.password
		if r := post("/v1/auth/password", body, ""); r.status != 400 {
			t.Fatalf("same password accepted: %d", r.status)
		}
		body["new_password"] = "replacement password"
		r = post("/v1/auth/password", body, "")
		if r.status != 200 {
			t.Fatalf("change: %d %s", r.status, r.body)
		}
		delete(body, "new_password")
		if r := post("/v1/auth/password", body, ""); r.status != 401 {
			t.Fatalf("old temp works: %d", r.status)
		}
		body["password"] = "replacement password"
		if r := post("/v1/auth/password", body, ""); r.status != 200 {
			t.Fatalf("must_change not cleared: %d %s", r.status, r.body)
		}
	}
}

func TestChangeOwnPassword(t *testing.T) {
	a := newConnectorApp(t)
	post := func(path string, body map[string]string, key string) resp {
		return a.do(t, "POST", path, jsonBody(body), map[string]string{"Content-Type": "application/json", "X-API-Key": key})
	}
	name := fmt.Sprintf("change-password-%d", time.Now().UnixNano())
	a.users.SetPasswordSignIn(true, true)
	signup := post("/v1/auth/password/signup", map[string]string{"username": name, "password": "first password"}, "")
	if signup.status != 200 {
		t.Fatalf("signup: %d %s", signup.status, signup.body)
	}
	id := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })
	key := signup.json(t)["api_key"].(string)

	// Discovery: has_password is on only once there is a password to change.
	if r := a.do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.status != 200 || r.json(t)["has_password"] != true {
		t.Fatalf("has_password: %d %s", r.status, r.body)
	}

	// Wrong current password: 403 (not 401, which both UIs read as an
	// expired key and sign the browser out), counted on the same limiter as
	// sign-in, and the key used to ask is untouched.
	wrong := post("/v1/me/password", map[string]string{"current_password": "not it", "new_password": "second password"}, key)
	if wrong.status != 403 || wrong.json(t)["code"] != "bad_credentials" {
		t.Fatalf("wrong current password: %d %s", wrong.status, wrong.body)
	}

	// A connector access token carries this account's full REST permissions
	// (BearerAuth) but must never be able to change the password itself.
	redirect := "https://client.example/callback"
	client := a.registerClient(t, redirect)
	verifier, challenge := newVerifier()
	code := codeFrom(t, a.consent(t, authorizeQuery(client, redirect, challenge), key, "allow"))
	tok := a.form(t, "/oauth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirect},
		"client_id": {client}, "code_verifier": {verifier},
	}, nil)
	if tok.status != 200 {
		t.Fatalf("token: %d %s", tok.status, tok.body)
	}
	access := tok.json(t)["access_token"].(string)
	connectorTry := a.do(t, "POST", "/v1/me/password", jsonBody(map[string]string{"current_password": "first password", "new_password": "second password"}),
		map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + access})
	if connectorTry.status != 403 || connectorTry.json(t)["code"] != "connector_not_allowed" {
		t.Fatalf("connector token changed password: %d %s", connectorTry.status, connectorTry.body)
	}

	// Correct current password and a valid new one: success.
	changed := post("/v1/me/password", map[string]string{"current_password": "first password", "new_password": "second password"}, key)
	if changed.status != 204 {
		t.Fatalf("change: %d %s", changed.status, changed.body)
	}

	// The old password no longer signs in; the new one does.
	loginWith := func(password string) resp {
		return post("/v1/auth/password", map[string]string{"login": name, "password": password}, "")
	}
	if r := loginWith("first password"); r.status != 401 {
		t.Fatalf("old password still works: %d %s", r.status, r.body)
	}
	if r := loginWith("second password"); r.status != 200 {
		t.Fatalf("new password: %d %s", r.status, r.body)
	}

	// The API key used to change the password still works: changing a
	// password never revokes keys, sessions or connector tokens.
	if r := a.do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}); r.status != 200 {
		t.Fatalf("key after change: %d %s", r.status, r.body)
	}
}

func TestPasswordAccountsDisabled(t *testing.T) {
	for _, selfSignup := range []bool{false, true} {
		t.Run(fmt.Sprintf("password-enabled-%v", selfSignup), func(t *testing.T) {
			a := newConnectorApp(t)
			path := "/v1/auth/password"
			if selfSignup {
				a.users.SetPasswordSignIn(true, false)
				path += "/signup"
			}
			r := a.do(t, "POST", path, jsonBody(map[string]string{"username": "signup-off", "password": "first password"}), map[string]string{"Content-Type": "application/json"})
			if r.status != 404 {
				t.Fatalf("disabled: %d %s", r.status, r.body)
			}
		})
	}
}

func TestPasswordLoginLimit(t *testing.T) {
	a := newConnectorApp(t)
	a.users.SetPasswordSignIn(true, true)
	for i := 0; i < 11; i++ {
		r := a.do(t, "POST", "/v1/auth/password", jsonBody(map[string]string{"login": "missing-login", "password": "wrong password"}), map[string]string{"Content-Type": "application/json"})
		if i < 10 {
			if r.status != 401 {
				t.Fatalf("attempt %d: %d %s", i, r.status, r.body)
			}
		} else if r.status != 429 || r.header.Get("Retry-After") == "" {
			t.Fatalf("limit: %d %s", r.status, r.body)
		}
	}
}

func TestPasswordIPLimitAcrossUsernames(t *testing.T) {
	limits := config.DefaultLimits()
	limits.RateSigninIP = config.Rate{Burst: 3, Every: time.Hour}
	a := newConnectorApp(t, edition.ForKindWithLimits(edition.Host, limits))
	a.users.SetPasswordSignIn(true, true)
	for i := 0; i < 4; i++ {
		r := a.do(t, "POST", "/v1/auth/password", jsonBody(map[string]string{"login": fmt.Sprintf("missing-%d", i), "password": "wrong password"}), map[string]string{"Content-Type": "application/json"})
		if i < 3 {
			if r.status != 401 {
				t.Fatalf("attempt %d: %d %s", i, r.status, r.body)
			}
		} else if r.status != 429 || r.header.Get("Retry-After") == "" {
			t.Fatalf("IP limit: %d %s", r.status, r.body)
		}
	}
}

func TestSelfHostMarketingOnlyOnHostedServices(t *testing.T) {
	prev := instanceIsHostedService
	t.Cleanup(func() { instanceIsHostedService = prev })
	for _, tc := range []struct {
		name   string
		kind   edition.Kind
		hosted bool
		base   string
	}{
		{"hosted Host", edition.Host, true, "https://simple-hack.app"},
		{"self Host example.com", edition.Host, false, "https://simple-hack.app"},
		{"hosted Hack", edition.Hack, false, "https://simple-hack.app"},
		{"self Hack", edition.Hack, false, "https://hack.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			instanceIsHostedService = tc.hosted
			p := newAssetPipeline(edition.ForKind(tc.kind), tc.base)
			hosted := tc.hosted || tc.base == "https://simple-hack.app" && tc.kind == edition.Hack
			for _, page := range []struct{ name, marketing string }{
				{"hack-home.html", "Prefer your own server?"}, {"hack-home.html", "Running this inside a company?"}, {"hack-story.html", "Prefer your own server?"},
				{"hack-get-started.html", "Can I run my own?"}, {"features.html", `id="self-host"`},
				{"host-story.html", "Self-host on one small server"}, {"hack-companies.html", `href="#install"`},
			} {
				b, err := p.chromePage(page.name, p.chromeDataFor(httptest.NewRequest("GET", "/", nil), ""))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(b), page.marketing) != hosted {
					t.Errorf("%s marketing hosted=%v", page.name, hosted)
				}
				if !hosted && (strings.Contains(string(b), `href="/companies"`) || strings.Contains(string(b), "Self-host an event")) {
					t.Errorf("%s marketing navigation shown", page.name)
				}
			}
		})
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
	a := newConnectorApp(t)
	post := func(path string, body map[string]string, key string) resp {
		return a.do(t, "POST", path, jsonBody(body), map[string]string{"Content-Type": "application/json", "X-API-Key": key})
	}
	a.users.SetPasswordSignIn(true, true)

	stamp := time.Now().UnixNano()
	raw := fmt.Sprintf("  Victim+%d@Example.com  ", stamp)
	wantLogin := strings.ToLower(strings.TrimSpace(raw))

	signup := post("/v1/auth/password/signup", map[string]string{"username": raw, "password": "first password"}, "")
	if signup.status != 200 {
		t.Fatalf("free-form signup: %d %s", signup.status, signup.body)
	}
	id := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })

	gotUsername, _ := signup.json(t)["username"].(string)
	if gotUsername == wantLogin || strings.Contains(gotUsername, "@") {
		t.Fatalf("self sign-up stored the typed login as users.username: %q", gotUsername)
	}
	if got := signup.json(t)["login"]; got != wantLogin {
		t.Fatalf("signup response login: got %q want %q", got, wantLogin)
	}
	key := signup.json(t)["api_key"].(string)
	me := a.do(t, "GET", "/v1/me", nil, map[string]string{"X-API-Key": key}).json(t)
	if me["login"] != wantLogin {
		t.Fatalf("GET /v1/me login: got %v want %q", me["login"], wantLogin)
	}

	// A differently-cased, differently-padded form of the same login signs
	// into the same account.
	login := post("/v1/auth/password", map[string]string{"login": "  " + strings.ToUpper(raw) + "  ", "password": "first password"}, "")
	if login.status != 200 {
		t.Fatalf("sign-in with re-cased/padded login: %d %s", login.status, login.body)
	}
	if login.json(t)["id"] != id {
		t.Fatalf("signed into a different account: got %v want %v", login.json(t)["id"], id)
	}

	// A second signup differing only in case collides against
	// account_passwords.login, not a second account.
	dup := post("/v1/auth/password/signup", map[string]string{"username": strings.ToUpper(raw), "password": "another password"}, "")
	if dup.status != 409 || dup.json(t)["code"] != "username_taken" {
		t.Fatalf("case-only duplicate not refused: %d %s", dup.status, dup.body)
	}

	// Sanity limits, not format gating: empty (after trim) and over-long are
	// still refused, with the new, non-format message.
	empty := post("/v1/auth/password/signup", map[string]string{"username": "   ", "password": "another password"}, "")
	if empty.status != 400 || empty.json(t)["code"] != "invalid_username" {
		t.Fatalf("empty username accepted: %d %s", empty.status, empty.body)
	}
	tooLong := post("/v1/auth/password/signup", map[string]string{"username": strings.Repeat("a", 255), "password": "another password"}, "")
	if tooLong.status != 400 || tooLong.json(t)["code"] != "invalid_username" {
		t.Fatalf("over-long username accepted: %d %s", tooLong.status, tooLong.body)
	}
}

// TestResetPasswordResponseShowsRealLogin covers the adversary-review fix
// (password.go's temporaryPasswordResponse): a self sign-up's users.username
// is the synthetic "pw-..." value, so admin.html's "Temporary password for
// <username>" must not show it -- the reset response now carries the
// account's own login (account_passwords.login) too.
func TestResetPasswordResponseShowsRealLogin(t *testing.T) {
	a := newConnectorApp(t)
	a.users.SetPasswordSignIn(true, true)

	raw := fmt.Sprintf("  Reset-Me+%d@Example.com  ", time.Now().UnixNano())
	wantLogin := strings.ToLower(strings.TrimSpace(raw))
	signup := a.do(t, "POST", "/v1/auth/password/signup", jsonBody(map[string]string{"username": raw, "password": "first password"}), map[string]string{"Content-Type": "application/json"})
	if signup.status != 200 {
		t.Fatalf("signup: %d %s", signup.status, signup.body)
	}
	id := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })
	syntheticUsername, _ := signup.json(t)["username"].(string)
	if syntheticUsername == wantLogin {
		t.Fatalf("signup did not use a synthetic username")
	}

	reset := a.do(t, "POST", "/v1/admin/users/"+id+"/password", jsonBody(map[string]string{"password": "reset password"}), map[string]string{"Content-Type": "application/json", "X-API-Key": a.admin})
	if reset.status != 200 {
		t.Fatalf("reset: %d %s", reset.status, reset.body)
	}
	got := reset.json(t)
	if got["username"] != syntheticUsername {
		t.Fatalf("reset response username: got %v want %q", got["username"], syntheticUsername)
	}
	if got["login"] != wantLogin {
		t.Fatalf("reset response login: got %v want %q (admin.html would show the synthetic username instead)", got["login"], wantLogin)
	}

	// An admin-created account (temporary=true, username==login already)
	// still gets a login field, equal to its username -- no regression.
	adminRaw := fmt.Sprintf("admin-made-%d", time.Now().UnixNano())
	created := a.do(t, "POST", "/v1/admin/password-users", jsonBody(map[string]string{"username": adminRaw}), map[string]string{"Content-Type": "application/json", "X-API-Key": a.admin})
	if created.status != 200 {
		t.Fatalf("admin create: %d %s", created.status, created.body)
	}
	cdata := created.json(t)
	adminID := cdata["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", adminID) })
	if cdata["login"] != adminRaw || cdata["username"] != adminRaw {
		t.Fatalf("admin-created account login/username: got login=%v username=%v want %q", cdata["login"], cdata["username"], adminRaw)
	}
}

// TestAdminUsersListLoginFieldGatesResetButton covers SIM-383: admin.html's
// Users list shows "Reset password" only when passwordEnabled AND the row's
// own u.login is non-empty, so an account with no password row never offers
// to reset a password it doesn't have. This pins the GET /v1/admin/users
// contract that gate depends on: "login" is "" for a passwordless account
// and the account's own login for a password account.
func TestAdminUsersListLoginFieldGatesResetButton(t *testing.T) {
	a := newConnectorApp(t)
	a.users.SetPasswordSignIn(true, true)

	noPassword := a.newPerson(t, "nopw-sim383")
	noPasswordUser, err := accountsrepo.GetUserByUsername(context.Background(), a.database, noPassword.email)
	if err != nil {
		t.Fatalf("look up passwordless account: %v", err)
	}
	noPasswordID := noPasswordUser.ID

	raw := fmt.Sprintf("  Sim383+%d@Example.com  ", time.Now().UnixNano())
	wantLogin := strings.ToLower(strings.TrimSpace(raw))
	signup := a.do(t, "POST", "/v1/auth/password/signup", jsonBody(map[string]string{"username": raw, "password": "first password"}), map[string]string{"Content-Type": "application/json"})
	if signup.status != 200 {
		t.Fatalf("signup: %d %s", signup.status, signup.body)
	}
	pwID := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", pwID) })

	list := a.do(t, "GET", "/v1/admin/users", nil, map[string]string{"X-API-Key": a.admin})
	if list.status != 200 {
		t.Fatalf("admin users: %d %s", list.status, list.body)
	}
	var sawNoPassword, sawPassword bool
	for _, x := range list.json(t)["users"].([]any) {
		u := x.(map[string]any)
		switch u["id"] {
		case noPasswordID:
			sawNoPassword = true
			if login, _ := u["login"].(string); login != "" {
				t.Fatalf("passwordless account carries a login (would show Reset password with nothing to reset): %v", u)
			}
		case pwID:
			sawPassword = true
			if u["login"] != wantLogin {
				t.Fatalf("password account login: got %v want %q", u["login"], wantLogin)
			}
		}
	}
	if !sawNoPassword || !sawPassword {
		t.Fatalf("missing accounts from listing: no-password seen=%v password seen=%v", sawNoPassword, sawPassword)
	}
}

// TestAccountExportShowsRealLoginForPasswordAccount covers the
// adversary-review fix (account_data.go's accountDocuments, the "Download
// my data" export): a self sign-up's account.json must carry its own login
// alongside account_name, never just the synthetic "pw-..." value.
func TestAccountExportShowsRealLoginForPasswordAccount(t *testing.T) {
	a := newConnectorApp(t)
	a.users.SetPasswordSignIn(true, true)

	raw := fmt.Sprintf("  Export-Me+%d@Example.com  ", time.Now().UnixNano())
	wantLogin := strings.ToLower(strings.TrimSpace(raw))
	signup := a.do(t, "POST", "/v1/auth/password/signup", jsonBody(map[string]string{"username": raw, "password": "first password"}), map[string]string{"Content-Type": "application/json"})
	if signup.status != 200 {
		t.Fatalf("signup: %d %s", signup.status, signup.body)
	}
	id := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })
	key := signup.json(t)["api_key"].(string)
	syntheticUsername, _ := signup.json(t)["username"].(string)
	if syntheticUsername == wantLogin {
		t.Fatalf("signup did not use a synthetic username")
	}

	r := a.do(t, "GET", "/v1/me/export.zip", nil, map[string]string{"X-API-Key": key})
	if r.status != 200 {
		t.Fatalf("export: %d %s", r.status, r.body)
	}
	files := zipFiles(t, r.body)
	var account map[string]any
	if err := json.Unmarshal([]byte(fileUnder(t, files, "/account.json")), &account); err != nil {
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

// TestPasswordSelfSignupNeverHijacksEmailSignIn covers the redesigned
// pre-account-hijacking fix (2026-11-05): a self sign-up's users.username is
// now a synthetic value, so it can never be found by address at all -- an
// email-code sign-in to the address the squatter typed as their login
// creates or uses a SEPARATE, ordinary email account, never resolving to or
// touching the password account.
func TestPasswordSelfSignupNeverHijacksEmailSignIn(t *testing.T) {
	withEmailSignIn(t, true)
	a := newConnectorApp(t)
	a.users.SetPasswordSignIn(true, true)
	ctx := context.Background()

	address := fmt.Sprintf("hijack-target-%d@example.com", time.Now().UnixNano())
	signup := a.do(t, "POST", "/v1/auth/password/signup", jsonBody(map[string]string{"username": address, "password": "squatter password"}), map[string]string{"Content-Type": "application/json"})
	if signup.status != 200 {
		t.Fatalf("attacker signup: %d %s", signup.status, signup.body)
	}
	pwID := signup.json(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", pwID) })

	// The real owner now proves control of the same address with an
	// emailed code.
	code := "913042"
	linkToken := fmt.Sprintf("hijack-link-%d", time.Now().UnixNano())
	if err := accountsrepo.CreateAuthToken(ctx, a.database, address, code, linkToken, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM auth_tokens WHERE email=$1", address) })

	user, created, status, body := verifyEmailCode(ctx, a.database, a.users.EmailLimiter(), verifyRequest{Email: address, Code: code}, "dashboard", sql.NullString{}, false, 15*time.Minute)
	if status != 0 {
		t.Fatalf("email code sign-in to a separate address was refused: status=%d body=%+v", status, body)
	}
	if !created {
		t.Fatalf("email code sign-in did not create a new, separate account")
	}
	if user.ID == pwID {
		t.Fatalf("email code sign-in resolved into the password account")
	}
	if user.Username != address {
		t.Fatalf("new email account username: got %q want %q", user.Username, address)
	}
	t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", user.ID) })

	// The password account itself is untouched: the squatter's original
	// password still signs in, into its own separate account.
	signin := a.do(t, "POST", "/v1/auth/password", jsonBody(map[string]string{"login": address, "password": "squatter password"}), map[string]string{"Content-Type": "application/json"})
	if signin.status != 200 || signin.json(t)["id"] != pwID {
		t.Fatalf("password account was affected by the separate email sign-in: %d %s", signin.status, signin.body)
	}
}

// TestPasswordUsernameBoundaries covers validUsername's sanity limits at
// their edges: 1 rune and 254 multibyte runes are both fine (no character
// class, no format gating), but a username carrying a control character is
// refused regardless of length.
func TestPasswordUsernameBoundaries(t *testing.T) {
	a := newConnectorApp(t)
	a.users.SetPasswordSignIn(true, true)
	post := func(username string) resp {
		return a.do(t, "POST", "/v1/auth/password/signup", jsonBody(map[string]string{"username": username, "password": "boundary password"}), map[string]string{"Content-Type": "application/json"})
	}

	// 1 rune, from the Unicode Private Use Area so it never collides with
	// anything a real sign-up would choose.
	one := post("")
	if one.status != 200 {
		t.Fatalf("1-rune username refused: %d %s", one.status, one.body)
	}
	if id, ok := one.json(t)["id"].(string); ok && id != "" {
		t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })
	}

	// 254 multibyte runes (508 UTF-8 bytes): still within the 254-rune limit.
	long := strings.Repeat("é", 254)
	exact := post(long)
	if exact.status != 200 {
		t.Fatalf("254-multibyte-rune username refused: %d %s", exact.status, exact.body)
	}
	if id, ok := exact.json(t)["id"].(string); ok && id != "" {
		t.Cleanup(func() { _, _ = a.database.Exec("DELETE FROM users WHERE id=$1", id) })
	}

	// An embedded control character (BEL) is refused regardless of length.
	withControl := post("abc\x07def")
	if withControl.status != 400 || withControl.json(t)["code"] != "invalid_username" {
		t.Fatalf("control character accepted: %d %s", withControl.status, withControl.body)
	}
}
