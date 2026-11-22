package accesstest

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/vsriram/simple-host/host/access/featuretest"
	model "github.com/vsriram/simple-host/host/model"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	"github.com/vsriram/simple-host/host/web"
)

// Keys stop working after their fixed expiry or after going unused for
// KEY_IDLE_EXPIRY_DAYS, with a 401 that says which and what to do; 0 turns
// idle expiry off. Needs DB_DSN.
func TestKeyExpiry(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "expiry")
	full := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	for _, bad := range []int{0, -1, 3651} {
		if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]any{"name": "x", "expires_in_days": bad}), full); r.Status != http.StatusBadRequest {
			t.Fatalf("expires_in_days %d: %d", bad, r.Status)
		}
	}
	mint := func(name string, body map[string]any) (string, string) {
		body["name"] = name
		r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(body), full)
		if r.Status != http.StatusCreated {
			t.Fatalf("mint %s: %d %s", name, r.Status, r.Body)
		}
		return r.JSON(t)["api_key"].(string), r.JSON(t)["id"].(string)
	}
	fixed, fixedID := mint("thirty days", map[string]any{"expires_in_days": 30})
	idle, idleID := mint("forgotten", map[string]any{})
	adminID, err := accountsrepo.EnsureAdminUser(context.Background(), a.Database)
	if err != nil {
		t.Fatal(err)
	}
	// Built fresh per call (not through a.do/a.mux's one middleware instance,
	// fixed at construction) so it picks up a.users' current limits snapshot,
	// including the KEY_IDLE_EXPIRY_DAYS=0 override applied below.
	me := func(k string) Resp {
		mux2 := http.NewServeMux()
		a.Users.Register(mux2, auth.Middleware(a.Admin, adminID, accountapp.New(a.Database), a.Users.Limits.KeyIdleExpiry), web.NoticeMiddleware("1.0.0"))
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("X-API-Key", k)
		rec := httptest.NewRecorder()
		mux2.ServeHTTP(rec, req)
		return Resp{Status: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
	}
	if me(fixed).Status != http.StatusOK || me(idle).Status != http.StatusOK {
		t.Fatal("fresh keys should work")
	}
	for _, k := range a.KeysOf(t, p.Key) {
		if k["id"] == fixedID && k["expires_at"] == nil {
			t.Errorf("fixed expiry not listed: %v", k)
		}
		if k["idle_expires_at"] == nil {
			t.Errorf("idle expiry not listed: %v", k)
		}
	}

	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, fixedID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE api_keys SET idle_from = now() - interval '200 days', last_used_at = now() - interval '181 days' WHERE id = $1`, idleID); err != nil {
		t.Fatal(err)
	}
	if r := me(fixed); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "key_expired" || !strings.Contains(string(r.Body), "new key") {
		t.Errorf("expired key: %d %s", r.Status, r.Body)
	}
	if r := me(idle); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "key_expired_idle" || !strings.Contains(string(r.Body), "180 days") {
		t.Errorf("idle key: %d %s", r.Status, r.Body)
	}
	for _, k := range a.KeysOf(t, p.Key) {
		switch k["id"] {
		case fixedID:
			if k["expired"] != "expired" {
				t.Errorf("fixed key listed as %v", k["expired"])
			}
		case idleID:
			if k["expired"] != "idle" {
				t.Errorf("idle key listed as %v", k["expired"])
			}
		}
	}

	a.SetLimits(WithLimits(t, map[string]string{"KEY_IDLE_EXPIRY_DAYS": "0"}))
	if r := me(idle); r.Status != http.StatusOK {
		t.Errorf("idle expiry off: %d %s", r.Status, r.Body)
	}
	if r := me(fixed); r.Status != http.StatusUnauthorized {
		t.Errorf("fixed expiry still applies with idle expiry off: %d", r.Status)
	}
}

// Sign-in names the key it issues: a typed code defaults to "agent sign-in",
// a link to "dashboard sign-in", and the caller may name it.
func TestSignInNamesItsKey(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "named")
	verify := func(code string, body map[string]string) string {
		t.Helper()
		lt, _ := auth.GenerateAPIKey()
		if err := accountsrepo.CreateAuthToken(context.Background(), a.Database, ann.Email, code, lt, time.Now().Add(time.Minute), "dashboard", sql.NullString{}, sql.NullString{}); err != nil {
			t.Fatal(err)
		}
		body["email"], body["code"] = ann.Email, code
		r := a.Do(t, http.MethodPost, "/v1/auth/verify", rt.JSONBody(body), map[string]string{"Content-Type": "application/json"})
		if r.Status != http.StatusOK {
			t.Fatalf("verify: %d %s", r.Status, r.Body)
		}
		k, _ := r.JSON(t)["api_key"].(string)
		if !strings.HasPrefix(k, auth.APIKeyPrefix) {
			t.Fatalf("sign-in key without prefix: %q", k[:6])
		}
		return k
	}
	agent := verify("333333", map[string]string{})
	dash := verify("444444", map[string]string{"name": "dashboard sign-in"})
	names := map[string]string{}
	for _, k := range a.KeysOf(t, dash) {
		l4, _ := k["last4"].(string)
		n, _ := k["name"].(string)
		names[l4+"|"+n] = "ok"
	}
	if names[agent[len(agent)-4:]+"|agent sign-in"] == "" || names[dash[len(dash)-4:]+"|dashboard sign-in"] == "" {
		t.Fatalf("names: %v", names)
	}
}

// The admin replaces a participant's keys with one new key, shown once.
func TestAdminReissuesKey(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "lost")
	nobody := a.NewPerson(t, "nobody")
	var id, adminID string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = $1`, p.Email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = 'admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	reissue := func(uid, key string) Resp {
		return a.Do(t, http.MethodPost, "/v1/admin/users/"+uid+"/key", nil, map[string]string{"X-API-Key": key})
	}
	if r := reissue(id, nobody.Key); r.Status != http.StatusNotFound {
		t.Fatalf("non-admin reissue: %d", r.Status)
	}
	if r := reissue(adminID, a.Admin); r.Status != http.StatusBadRequest {
		t.Fatalf("admin account reissue: %d", r.Status)
	}
	r := reissue(id, a.Admin)
	if r.Status != http.StatusOK {
		t.Fatalf("reissue: %d %s", r.Status, r.Body)
	}
	fresh, _ := r.JSON(t)["api_key"].(string)
	if a.Do(t, http.MethodGet, "/v1/me/keys", nil, map[string]string{"X-API-Key": p.Key}).Status != http.StatusUnauthorized {
		t.Fatal("old key survived a reissue")
	}
	ks := a.KeysOf(t, fresh)
	if len(ks) != 1 || ks[0]["name"] != model.KeyNameEvent || ks[0]["current"] != true {
		t.Fatalf("after reissue: %v", ks)
	}
	var event bool
	if err := a.Database.QueryRow(`SELECT event_account FROM users WHERE id = $1`, id).Scan(&event); err != nil || !event {
		t.Fatalf("reissue did not mark the event account: %v %v", event, err)
	}
}

// A deploy-only key is refused (403 deploy_only_key) on every route outside
// the deploy table, on REST and through /mcp alike. What it may call is the
// table itself (Sites serves those routes).
func TestDeployOnlyKey(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "deploykey")
	full := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}

	if r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]string{"name": "CI", "scope": "admin"}), full); r.Status != http.StatusBadRequest {
		t.Fatalf("unknown scope: %d %s", r.Status, r.Body)
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]any{"name": "GitHub Actions", "scope": "deploy"}), full)
	if r.Status != http.StatusCreated || r.JSON(t)["scope"] != "deploy" {
		t.Fatalf("mint deploy key: %d %s", r.Status, r.Body)
	}
	dk := r.JSON(t)["api_key"].(string)
	dh := map[string]string{"X-API-Key": dk, "Content-Type": "application/json"}

	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodDelete, "/v1/sites/ci", nil},
		{http.MethodPost, "/v1/sites/ci/domain", map[string]string{"domain": "ci.example.com"}},
		{http.MethodGet, "/v1/me/keys", nil},
		{http.MethodGet, "/v1/me/keys", nil},
		{http.MethodPost, "/v1/me/keys", map[string]string{"name": "escalate"}},
		{http.MethodPost, "/v1/me/api-key/rotate", nil},
		{http.MethodGet, "/v1/me/export.zip", nil},
		{http.MethodGet, "/v1/sites/ci/storage/resources", nil},
	} {
		r := a.Do(t, c.method, c.path, c.body, dh)
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "deploy_only_key" {
			t.Errorf("deploy key %s %s: %d %s, want 403 deploy_only_key", c.method, c.path, r.Status, r.Body)
		}
	}

	// The same key on /mcp: a tool behind a refused route is refused.
	r = a.Do(t, http.MethodPost, "/mcp", rt.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "delete_site", "arguments": map[string]any{"site": "ci", "confirm_name": "ci"}}}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
	if text, _, isErr := ToolResultOf(t, r); !isErr || !strings.Contains(text, "deploy_only_key") {
		t.Errorf("delete_site with a deploy key: %v %s", isErr, text)
	}

	// The full key is untouched, and the list shows the scope.
	if r := a.Do(t, http.MethodGet, "/v1/me/keys", nil, full); r.Status != http.StatusOK {
		t.Fatalf("full key /v1/me: %d", r.Status)
	}
	var sawDeploy bool
	for _, k := range a.KeysOf(t, p.Key) {
		if k["name"] == "GitHub Actions" {
			sawDeploy = k["scope"] == "deploy"
		} else if k["scope"] != "full" {
			t.Errorf("sign-in key scope %v", k["scope"])
		}
	}
	if !sawDeploy {
		t.Error("deploy key not listed with scope deploy")
	}
	if !auth.DeployKeyAllows("PUT /v1/sites/{sitename}/files") || auth.DeployKeyAllows("DELETE /v1/sites/{sitename}") {
		t.Error("route table")
	}
}

// An expired key says so everywhere: on /mcp as 401 key_expired with no OAuth
// challenge, and a deploy key on a route it may not call as key_expired
// rather than deploy_only_key. Needs DB_DSN.
func TestExpiredKeyOnMCPAndOffDeployRoutes(t *testing.T) {
	a := NewApp(t)
	p := a.NewPerson(t, "mcpexp")
	full := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", rt.JSONBody(map[string]any{"name": "CI", "scope": "deploy"}), full)
	if r.Status != http.StatusCreated {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	dk, id := r.JSON(t)["api_key"].(string), r.JSON(t)["id"].(string)
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	res := a.Do(t, http.MethodPost, "/mcp", rt.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
	if res.Status != http.StatusUnauthorized || res.JSON(t)["code"] != "key_expired" || res.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("/mcp with an expired key: %d %v %s", res.Status, res.Header.Get("WWW-Authenticate"), res.Body)
	}
	if r := a.Do(t, http.MethodGet, "/v1/me/keys", nil, map[string]string{"X-API-Key": dk}); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "key_expired" {
		t.Errorf("expired deploy key off its routes: %d %s", r.Status, r.Body)
	}
}
