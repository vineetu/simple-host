package signin_test

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"

	access "simplehost/access/core"
	"simplehost/access/signin"
	"simplehost/app"
	"simplehost/core/testkit"
	"simplehost/core/web"
)

// whoami answers with the caller's id, as any route would see them.
func whoami(a *app.App) {
	a.Routes.Platform.Handle("GET /test/me", web.Func(func(w http.ResponseWriter, r *http.Request) error {
		w.Write([]byte(web.CallerOf(r).ID))
		return nil
	}))
}

// signIn asks for a code for email and verifies it with the extra fields.
func signIn(t *testing.T, k *testkit.Kit, b *http.Client, email string, extra map[string]any) testkit.Res {
	t.Helper()
	if r := testkit.Call(t, b, "POST", "https://simple-host.app/v1/auth/code", map[string]string{"email": email}); r.Status != 200 {
		t.Fatalf("code: %d %s", r.Status, r.Body)
	}
	mails := k.Mail.To(strings.ToLower(email))
	in := map[string]any{"email": email, "code": regexp.MustCompile(`\d{6}`).FindString(mails[len(mails)-1].Text)}
	for key, v := range extra {
		in[key] = v
	}
	return testkit.Call(t, b, "POST", "https://simple-host.app/v1/auth/verify", in)
}

// SPEC test 1 (F1): sign up with an emailed code and pick a handle. Google
// with the same email reaches the same person.
func TestSignUpWithCodeThenGoogle(t *testing.T) {
	k := testkit.Start(t, signin.Mount, whoami)
	b := k.Browser()
	if r := testkit.Call(t, b, "POST", "https://simple-host.app/v1/auth/verify", map[string]string{"email": "nina@example.com", "code": "000000"}); r.Status != 401 {
		t.Fatalf("a code never sent: %d %s", r.Status, r.Body)
	}
	r := signIn(t, k, b, "Nina@Example.com", map[string]any{"handle": "nina"})
	var me struct{ ID, Email, Handle string }
	json.Unmarshal([]byte(r.Body), &me)
	if r.Status != 200 || me.Handle != "nina" || me.Email != "nina@example.com" {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	if got := testkit.Call(t, b, "GET", "https://simple-host.app/test/me", nil).Body; got != me.ID {
		t.Fatalf("signed-in browser is %q, want %q", got, me.ID)
	}
	if r := signIn(t, k, k.Browser(), "olga@example.com", map[string]any{"handle": "nina"}); r.Status != 409 || !strings.Contains(r.Body, "nina") {
		t.Fatalf("a taken handle: %d %s", r.Status, r.Body)
	}

	g := k.Browser()
	k.Google.Email = "nina@example.com"
	r = testkit.Call(t, g, "GET", "https://simple-host.app/v1/auth/google", nil)
	for i := 0; i < 2 && r.Status == http.StatusFound; i++ { // to Google, then back to the callback
		r = testkit.Call(t, g, "GET", r.Header.Get("Location"), nil)
	}
	if r.Status != http.StatusFound || r.Header.Get("Location") != "/dashboard" {
		t.Fatalf("google callback: %d %s %s", r.Status, r.Header.Get("Location"), r.Body)
	}
	if got := testkit.Call(t, g, "GET", "https://simple-host.app/test/me", nil).Body; got != me.ID {
		t.Fatalf("google reached %q, want the same person %q", got, me.ID)
	}
}

// SPEC test 2 (F1): sign out everywhere ends a browser session and an app token.
func TestSignOutEverywhere(t *testing.T) {
	k := testkit.Start(t, signin.Mount, whoami)
	b := k.Browser()
	var me struct{ ID string }
	json.Unmarshal([]byte(signIn(t, k, b, "nina@example.com", nil).Body), &me)
	var grant string
	k.Exec(t, `INSERT INTO oauth_clients (id, name) VALUES ('app', 'An app')`)
	if err := k.App.DB.QueryRow(`INSERT INTO oauth_grants (person_id, client_id) VALUES ($1, 'app') RETURNING id`, me.ID).Scan(&grant); err != nil {
		t.Fatal(err)
	}
	token := access.NewSecret("shat_")
	k.Exec(t, `INSERT INTO oauth_tokens (hash, kind, grant_id, person_id, expires_at) VALUES ($1, 'access', $2, $3, now() + interval '1 hour')`,
		access.Hash(token), grant, me.ID)
	app := k.Browser()
	if got := testkit.Call(t, app, "GET", "https://simple-host.app/test/me", nil, "Authorization", "Bearer "+token).Body; got != me.ID {
		t.Fatalf("app token acts as %q, want %q", got, me.ID)
	}

	if r := testkit.Call(t, b, "POST", "https://simple-host.app/v1/auth/signout", map[string]bool{"everywhere": true}); r.Status != 200 {
		t.Fatalf("sign out: %d %s", r.Status, r.Body)
	}
	if got := testkit.Call(t, b, "GET", "https://simple-host.app/test/me", nil).Body; got != "" {
		t.Fatalf("browser still signed in as %q", got)
	}
	if r := testkit.Call(t, app, "GET", "https://simple-host.app/test/me", nil, "Authorization", "Bearer "+token); r.Status != 401 {
		t.Fatalf("app token after sign out everywhere: %d %s", r.Status, r.Body)
	}
}
