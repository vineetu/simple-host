package handler

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/edition"
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

	// Wrong current password: 401, counted on the same limiter as sign-in,
	// and the key used to ask is untouched.
	wrong := post("/v1/me/password", map[string]string{"current_password": "not it", "new_password": "second password"}, key)
	if wrong.status != 401 || wrong.json(t)["code"] != "bad_credentials" {
		t.Fatalf("wrong current password: %d %s", wrong.status, wrong.body)
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
