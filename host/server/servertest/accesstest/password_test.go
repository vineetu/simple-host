package accesstest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/edition"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
)

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

func TestPasswordAccountsDisabled(t *testing.T) {
	for _, selfSignup := range []bool{false, true} {
		t.Run(fmt.Sprintf("password-enabled-%v", selfSignup), func(t *testing.T) {
			a := NewApp(t)
			path := "/v1/auth/password"
			if selfSignup {
				a.Users.SetPasswordSignIn(true, false)
				path += "/signup"
			}
			r := a.Do(t, "POST", path, rt.JSONBody(map[string]string{"username": "signup-off", "password": "first password"}), map[string]string{"Content-Type": "application/json"})
			if r.Status != 404 {
				t.Fatalf("disabled: %d %s", r.Status, r.Body)
			}
		})
	}
}

func TestPasswordLoginLimit(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	for i := 0; i < 11; i++ {
		r := a.Do(t, "POST", "/v1/auth/password", rt.JSONBody(map[string]string{"login": "missing-login", "password": "wrong password"}), map[string]string{"Content-Type": "application/json"})
		if i < 10 {
			if r.Status != 401 {
				t.Fatalf("attempt %d: %d %s", i, r.Status, r.Body)
			}
		} else if r.Status != 429 || r.Header.Get("Retry-After") == "" {
			t.Fatalf("limit: %d %s", r.Status, r.Body)
		}
	}
}

func TestPasswordIPLimitAcrossUsernames(t *testing.T) {
	limits := config.DefaultLimits()
	limits.RateSigninIP = config.Rate{Burst: 3, Every: time.Hour}
	a := NewApp(t, edition.Default(limits))
	a.Users.SetPasswordSignIn(true, true)
	for i := 0; i < 4; i++ {
		r := a.Do(t, "POST", "/v1/auth/password", rt.JSONBody(map[string]string{"login": fmt.Sprintf("missing-%d", i), "password": "wrong password"}), map[string]string{"Content-Type": "application/json"})
		if i < 3 {
			if r.Status != 401 {
				t.Fatalf("attempt %d: %d %s", i, r.Status, r.Body)
			}
		} else if r.Status != 429 || r.Header.Get("Retry-After") == "" {
			t.Fatalf("IP limit: %d %s", r.Status, r.Body)
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

// TestPasswordSelfSignupNeverHijacksEmailSignIn covers the redesigned
// pre-account-hijacking fix (2026-11-05): a self sign-up's users.username is
// now a synthetic value, so it can never be found by address at all -- an
// email-code sign-in to the address the squatter typed as their login
// creates or uses a SEPARATE, ordinary email account, never resolving to or
// touching the password account.
func TestPasswordSelfSignupNeverHijacksEmailSignIn(t *testing.T) {
	withEmailSignIn(t, true)
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	ctx := context.Background()

	address := fmt.Sprintf("hijack-target-%d@example.com", time.Now().UnixNano())
	signup := a.Do(t, "POST", "/v1/auth/password/signup", rt.JSONBody(map[string]string{"username": address, "password": "squatter password"}), map[string]string{"Content-Type": "application/json"})
	if signup.Status != 200 {
		t.Fatalf("attacker signup: %d %s", signup.Status, signup.Body)
	}
	pwID := signup.JSON(t)["id"].(string)
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", pwID) })

	// The real owner now proves control of the same address with an
	// emailed code.
	code := "913042"
	linkToken := fmt.Sprintf("hijack-link-%d", time.Now().UnixNano())
	if err := accountsrepo.CreateAuthToken(ctx, a.Database, address, code, linkToken, time.Now().Add(15*time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM auth_tokens WHERE email=$1", address) })

	user, created, status, body := accesshttp.VerifyEmailCode(ctx, a.Database, a.Users.EmailLimiter, accesshttp.VerifyRequest{Email: address, Code: code}, "dashboard", sql.NullString{}, false, 15*time.Minute)
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
	t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", user.ID) })

	// The password account itself is untouched: the squatter's original
	// password still signs in, into its own separate account.
	signin := a.Do(t, "POST", "/v1/auth/password", rt.JSONBody(map[string]string{"login": address, "password": "squatter password"}), map[string]string{"Content-Type": "application/json"})
	if signin.Status != 200 || signin.JSON(t)["id"] != pwID {
		t.Fatalf("password account was affected by the separate email sign-in: %d %s", signin.Status, signin.Body)
	}
}

// TestPasswordUsernameBoundaries covers validUsername's sanity limits at
// their edges: 1 rune and 254 multibyte runes are both fine (no character
// class, no format gating), but a username carrying a control character is
// refused regardless of length.
func TestPasswordUsernameBoundaries(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	post := func(username string) Resp {
		return a.Do(t, "POST", "/v1/auth/password/signup", rt.JSONBody(map[string]string{"username": username, "password": "boundary password"}), map[string]string{"Content-Type": "application/json"})
	}

	// 1 rune, from the Unicode Private Use Area so it never collides with
	// anything a real sign-up would choose.
	one := post("")
	if one.Status != 200 {
		t.Fatalf("1-rune username refused: %d %s", one.Status, one.Body)
	}
	if id, ok := one.JSON(t)["id"].(string); ok && id != "" {
		t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	}

	// 254 multibyte runes (508 UTF-8 bytes): still within the 254-rune limit.
	long := strings.Repeat("é", 254)
	exact := post(long)
	if exact.Status != 200 {
		t.Fatalf("254-multibyte-rune username refused: %d %s", exact.Status, exact.Body)
	}
	if id, ok := exact.JSON(t)["id"].(string); ok && id != "" {
		t.Cleanup(func() { _, _ = a.Database.Exec("DELETE FROM users WHERE id=$1", id) })
	}

	// An embedded control character (BEL) is refused regardless of length.
	withControl := post("abc\x07def")
	if withControl.Status != 400 || withControl.JSON(t)["code"] != "invalid_username" {
		t.Fatalf("control character accepted: %d %s", withControl.Status, withControl.Body)
	}
}

// The operator's "admin" row is protected even when its is_admin flag is
// off (accounts.IsOperator): its password cannot be set here. Needs DB_DSN.
func TestResetPasswordRefusesOperatorRow(t *testing.T) {
	a := NewApp(t)
	a.Users.SetPasswordSignIn(true, true)
	var id string
	if err := a.Database.QueryRow(`UPDATE users SET is_admin = false WHERE username = 'admin' RETURNING id`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = $1`, id) })
	r := a.Do(t, "POST", "/v1/admin/users/"+id+"/password", rt.JSONBody(map[string]string{"password": "reset password"}), map[string]string{"Content-Type": "application/json", "X-API-Key": a.Admin})
	if r.Status != 400 {
		t.Fatalf("operator row password reset: %d %s", r.Status, r.Body)
	}
}
