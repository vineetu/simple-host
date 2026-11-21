package accesstest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/host/web"
)

// A deploy-only key creates, updates, rolls back and lists sites and makes
// preview links, and is refused (403 deploy_only_key) everywhere else, on the
// REST routes, the page-data routes and through /mcp alike. Needs DB_DSN.
func TestDeployOnlyKey(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "deploykey")
	full := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}

	if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "CI", "scope": "admin"}), full); r.Status != http.StatusBadRequest {
		t.Fatalf("unknown scope: %d %s", r.Status, r.Body)
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "GitHub Actions", "scope": "deploy"}), full)
	if r.Status != http.StatusCreated || r.JSON(t)["scope"] != "deploy" {
		t.Fatalf("mint deploy key: %d %s", r.Status, r.Body)
	}
	dk := r.JSON(t)["api_key"].(string)
	dh := map[string]string{"X-API-Key": dk, "Content-Type": "application/json"}
	files := func(body string) any { return map[string]any{"files": map[string]string{"index.html": body}} }

	// Allowed: one-call create, update, list, versions, roll back, preview link.
	if r := a.Do(t, http.MethodPut, "/v1/sites/ci/files?create=1", servertest.JSONBody(files("v1")), dh); r.Status != http.StatusCreated {
		t.Fatalf("PUT ?create=1 on a missing site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/ci/files?create=1", servertest.JSONBody(files("v2")), dh); r.Status != http.StatusOK {
		t.Fatalf("PUT ?create=1 on an existing site: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/sites"},
		{http.MethodGet, "/v1/sites/ci/versions"},
		{http.MethodGet, "/v1/sites/ci/versions/1/files"},
		// This test server has no site hosts, so the route itself answers
		// 409 preview_unavailable: what matters is that the gate let it by.
		{http.MethodPost, "/v1/sites/ci/versions/1/preview-link"},
	} {
		if r := a.Do(t, c.method, c.path, nil, dh); r.Status/100 != 2 && r.JSON(t)["code"] != "preview_unavailable" {
			t.Errorf("deploy key %s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/ci/active-version", servertest.JSONBody(map[string]int{"version_number": 1}), dh); r.Status != http.StatusOK {
		t.Errorf("deploy key rollback: %d %s", r.Status, r.Body)
	}

	// Refused: everything else.
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodDelete, "/v1/sites/ci", nil},
		{http.MethodPatch, "/v1/sites/ci", map[string]string{"name": "ci2"}},
		{http.MethodPost, "/v1/sites/ci/domain", map[string]string{"domain": "ci.example.com"}},
		{http.MethodGet, "/v1/me", nil},
		{http.MethodGet, "/v1/me/keys", nil},
		{http.MethodPost, "/v1/me/keys", map[string]string{"name": "escalate"}},
		{http.MethodPost, "/v1/me/api-key/rotate", nil},
		{http.MethodGet, "/v1/me/export.zip", nil},
		{http.MethodGet, "/v1/sites/ci/storage/resources", nil},
		{http.MethodPut, "/v1/sites/ci/storage/kv/notes/keys/a", map[string]any{"value": 1}},
		{http.MethodGet, "/v1/sites/ci/analytics", nil},
	} {
		var body = servertest.JSONBody(c.body)
		if c.body == nil {
			body = nil
		}
		r := a.Do(t, c.method, c.path, body, dh)
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "deploy_only_key" {
			t.Errorf("deploy key %s %s: %d %s, want 403 deploy_only_key", c.method, c.path, r.Status, r.Body)
		}
	}

	// The same key on /mcp: tools behind deploy routes work, the rest are refused.
	mcpCall := func(tool string, args map[string]any) (string, bool) {
		r := a.Do(t, http.MethodPost, "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args}}),
			map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
		text, _, isErr := servertest.ToolResultOf(t, r)
		return text, isErr
	}
	if text, isErr := mcpCall("list_sites", map[string]any{}); isErr {
		t.Errorf("list_sites with a deploy key: %s", text)
	}
	if text, isErr := mcpCall("delete_site", map[string]any{"site": "ci", "confirm_name": "ci"}); !isErr || !strings.Contains(text, "deploy_only_key") {
		t.Errorf("delete_site with a deploy key: %v %s", isErr, text)
	}

	// The full key is untouched, and the list shows the scope.
	if r := a.Do(t, http.MethodGet, "/v1/me", nil, full); r.Status != http.StatusOK {
		t.Fatalf("full key /v1/me: %d", r.Status)
	}
	var sawDeploy bool
	for _, k := range servertest.KeysOf(t, a, p.Key) {
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

// Every pattern in the deploy route table is a registered route, so a
// renamed route cannot silently drop out of it.
func TestDeployRoutesAreRegistered(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/sites"}, {"POST", "/v1/sites/x"}, {"PUT", "/v1/sites/x"},
		{"POST", "/v1/sites/x/files"}, {"PUT", "/v1/sites/x/files"}, {"GET", "/v1/sites/x/versions"},
		{"GET", "/v1/sites/x/versions/1/files"}, {"GET", "/v1/sites/x/versions/1/files/a/b.html"},
		{"PUT", "/v1/sites/x/active-version"}, {"POST", "/v1/sites/x/versions/1/preview-link"},
		{"GET", "/mcp"}, {"POST", "/mcp"}, {"DELETE", "/mcp"},
	} {
		req, _ := http.NewRequest(c.method, a.Srv.URL+c.path, nil)
		if _, pattern := a.Mux.Handler(req); !auth.DeployKeyAllows(pattern) {
			t.Errorf("%s %s matched %q, which the deploy table does not list", c.method, c.path, pattern)
		}
	}
}

// Keys stop working after their fixed expiry or after going unused for
// KEY_IDLE_EXPIRY_DAYS, with a 401 that says which and what to do; 0 turns
// idle expiry off. Needs DB_DSN.
func TestKeyExpiry(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "expiry")
	full := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	for _, bad := range []int{0, -1, 3651} {
		if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "x", "expires_in_days": bad}), full); r.Status != http.StatusBadRequest {
			t.Fatalf("expires_in_days %d: %d", bad, r.Status)
		}
	}
	mint := func(name string, body map[string]any) (string, string) {
		body["name"] = name
		r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(body), full)
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
	me := func(k string) servertest.Resp {
		mux2 := http.NewServeMux()
		a.Users.Register(mux2, auth.Middleware(a.Admin, adminID, accountapp.New(a.Database), a.Users.Limits.KeyIdleExpiry), web.NoticeMiddleware("1.0.0"))
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.Header.Set("X-API-Key", k)
		rec := httptest.NewRecorder()
		mux2.ServeHTTP(rec, req)
		return servertest.Resp{Status: rec.Code, Header: rec.Header(), Body: rec.Body.Bytes()}
	}
	if me(fixed).Status != http.StatusOK || me(idle).Status != http.StatusOK {
		t.Fatal("fresh keys should work")
	}
	for _, k := range servertest.KeysOf(t, a, p.Key) {
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
	for _, k := range servertest.KeysOf(t, a, p.Key) {
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

	setConnectorAppLimits(a, servertest.WithLimits(t, map[string]string{"KEY_IDLE_EXPIRY_DAYS": "0"}))
	if r := me(idle); r.Status != http.StatusOK {
		t.Errorf("idle expiry off: %d %s", r.Status, r.Body)
	}
	if r := me(fixed); r.Status != http.StatusUnauthorized {
		t.Errorf("fixed expiry still applies with idle expiry off: %d", r.Status)
	}
}

// PUT ?create=1 creates a missing site in the same call, and a POST onto an
// existing site says to use PUT. Needs DB_DSN.
func TestPutCreateIfMissing(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "upsert")
	h := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	files := map[string]any{"files": map[string]string{"index.html": "hi"}}
	if r := a.Do(t, http.MethodPut, "/v1/sites/fresh/files", servertest.JSONBody(files), h); r.Status != http.StatusNotFound || !strings.Contains(string(r.Body), "create=1") {
		t.Fatalf("PUT without create on a missing site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/fresh/files?create=maybe", servertest.JSONBody(files), h); r.Status != http.StatusBadRequest {
		t.Fatalf("create=maybe: %d", r.Status)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/admin/files?create=1", servertest.JSONBody(files), h); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "name_reserved" {
		t.Fatalf("create=1 with a reserved name: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/fresh/files?create=1&publish=false", servertest.JSONBody(files), h); r.Status != http.StatusBadRequest {
		t.Fatalf("create=1 with publish=false on a missing site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/fresh/files?create=1", servertest.JSONBody(files), h); r.Status != http.StatusCreated {
		t.Fatalf("create=1: %d %s", r.Status, r.Body)
	}
	r := a.Do(t, http.MethodPost, "/v1/sites/fresh/files", servertest.JSONBody(files), h)
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "site_exists" || !strings.Contains(string(r.Body), "use PUT") {
		t.Fatalf("POST onto an existing site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/fresh/files?create=1&publish=false", servertest.JSONBody(files), h); r.Status != http.StatusOK {
		t.Fatalf("create=1 on an existing site keeps publish=false: %d %s", r.Status, r.Body)
	}
}

// Top pages and referring domains for the chosen range, most views first,
// from the people-only daily tables. Needs DB_DSN.
func TestSiteTopAnalytics(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "toppages")
	h := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	if r := a.Do(t, http.MethodPost, "/v1/sites/shop/files", servertest.JSONBody(map[string]any{"files": map[string]string{"index.html": "x"}}), h); r.Status != http.StatusCreated {
		t.Fatalf("deploy: %d %s", r.Status, r.Body)
	}
	var siteID string
	if err := a.Database.QueryRow(`SELECT s.id FROM sites s JOIN api_keys k ON k.user_id = s.user_id WHERE k.key_hash = encode(sha256($1::bytea), 'hex') AND s.name = 'shop'`, p.Key).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO site_page_daily (site_id, day, path, views) VALUES ($1, current_date, '/', 5), ($1, current_date - 1, '/', 2), ($1, current_date, '/menu/', 9), ($1, current_date - 40, '/old/', 99)`,
		`INSERT INTO site_referrer_daily (site_id, day, domain, views) VALUES ($1, current_date, 'news.ycombinator.com', 4), ($1, current_date, 't.co', 6), ($1, current_date - 40, 'old.example', 50)`,
	} {
		if _, err := a.Database.Exec(q, siteID); err != nil {
			t.Fatal(err)
		}
	}
	r := a.Do(t, http.MethodGet, "/v1/sites/shop/analytics/top?days=30", nil, h)
	if r.Status != http.StatusOK {
		t.Fatalf("top: %d %s", r.Status, r.Body)
	}
	body := string(r.Body)
	for _, want := range []string{
		`"pages":[{"path":"/menu/","views":9},{"path":"/","views":7}]`,
		`"referrers":[{"domain":"t.co","views":6},{"domain":"news.ycombinator.com","views":4}]`,
		`"range_days":30`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("top analytics %s: missing %s", body, want)
		}
	}
}

// A deploy key minted by an admin carries no admin powers: GET /v1/sites and
// the MCP list_sites tool list only the account's own sites, and the site
// quota applies. The admin's full key keeps both, including ?all=1
// (SIM-329: by default even the admin's own GET /v1/sites is own-account
// only). Needs DB_DSN.
func TestDeployKeyNeverAdmin(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	setConnectorAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "1"}))
	boss := a.NewPerson(t, "adminci")
	other := a.NewPerson(t, "bystander")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE id = (SELECT user_id FROM api_keys WHERE key_hash = $1)`, accountsrepo.HashAPIKey(boss.Key)); err != nil {
		t.Fatal(err)
	}
	files := map[string]any{"files": map[string]string{"index.html": "x"}}
	hdr := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, "Content-Type": "application/json"}
	}
	if r := a.Do(t, http.MethodPost, "/v1/sites/bystander-site/files", servertest.JSONBody(files), hdr(other.Key)); r.Status != http.StatusCreated {
		t.Fatalf("other's site: %d %s", r.Status, r.Body)
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "CI", "scope": "deploy"}), hdr(boss.Key))
	if r.Status != http.StatusCreated {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	dk := r.JSON(t)["api_key"].(string)

	if r := a.Do(t, http.MethodGet, "/v1/sites", nil, hdr(boss.Key)); strings.Contains(string(r.Body), "bystander-site") {
		t.Fatalf("admin's default GET /v1/sites leaked another account's site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodGet, "/v1/sites?all=1", nil, hdr(boss.Key)); !strings.Contains(string(r.Body), "bystander-site") {
		t.Fatalf("admin full key with all=1 should list every site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodGet, "/v1/sites", nil, hdr(dk)); r.Status != http.StatusOK || strings.Contains(string(r.Body), "bystander-site") {
		t.Errorf("deploy key listed another account's site: %d %s", r.Status, r.Body)
	}
	res := a.Do(t, http.MethodPost, "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list_sites", "arguments": map[string]any{}}}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
	if text, _, isErr := servertest.ToolResultOf(t, res); isErr || strings.Contains(text, "bystander-site") {
		t.Errorf("MCP list_sites with a deploy key: %v %s", isErr, text)
	}

	// Quota 1: the deploy key's first site fits, the second is refused.
	if r := a.Do(t, http.MethodPut, "/v1/sites/ci-one/files?create=1", servertest.JSONBody(files), hdr(dk)); r.Status != http.StatusCreated {
		t.Fatalf("first site: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPut, "/v1/sites/ci-two/files?create=1", servertest.JSONBody(files), hdr(dk)); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_quota_reached" {
		t.Errorf("deploy key over the quota: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPost, "/v1/sites/ci-three/files", servertest.JSONBody(files), hdr(dk)); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_quota_reached" {
		t.Errorf("deploy key POST over the quota: %d %s", r.Status, r.Body)
	}
	if r := a.Do(t, http.MethodPost, "/v1/sites/boss-two/files", servertest.JSONBody(files), hdr(boss.Key)); r.Status != http.StatusCreated {
		t.Errorf("admin full key stays exempt from the quota: %d %s", r.Status, r.Body)
	}
}

// Two CI jobs racing PUT ?create=1 on a site that does not exist yet: one
// creates it and the rest publish a new version, none gets site_exists.
// Needs DB_DSN.
func TestPutCreateRace(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "race")
	const n = 6
	statuses := make(chan int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"files": map[string]string{"index.html": fmt.Sprint("v", i)}})
			req, _ := http.NewRequest(http.MethodPut, a.Srv.URL+"/v1/sites/raced/files?create=1", bytes.NewReader(body))
			req.Header.Set("X-API-Key", p.Key)
			req.Header.Set("Content-Type", "application/json")
			<-start
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)
				return
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
				t.Errorf("racing PUT ?create=1: %d %s", res.StatusCode, b)
			}
			statuses <- res.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()
	close(statuses)
	created := 0
	for s := range statuses {
		if s == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Errorf("%d calls created the site, want 1", created)
	}
	var versions int
	if err := a.Database.QueryRow(`SELECT count(*) FROM versions v JOIN sites s ON s.id = v.site_id
		JOIN api_keys k ON k.user_id = s.user_id WHERE k.key_hash = $1 AND s.name = 'raced'`, accountsrepo.HashAPIKey(p.Key)).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != n {
		t.Errorf("%d versions after %d uploads", versions, n)
	}
}

// Keys that stopped working leave room under MAX_KEYS_PER_ACCOUNT, and are
// deleted 30 days after they stopped. Needs DB_DSN.
func TestExpiredKeysFreeTheCapAndArePurged(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	setConnectorAppLimits(a, servertest.WithLimits(t, map[string]string{"MAX_KEYS_PER_ACCOUNT": "3"}))
	p := a.NewPerson(t, "keycap")
	h := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	var ids []string
	for i := len(servertest.KeysOf(t, a, p.Key)); i < 3; i++ {
		r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": fmt.Sprint("k", i)}), h)
		if r.Status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.Status, r.Body)
		}
		ids = append(ids, r.JSON(t)["id"].(string))
	}
	if r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "full"}), h); r.Status != http.StatusConflict {
		t.Fatalf("at the cap: %d %s", r.Status, r.Body)
	}
	if len(ids) < 2 {
		t.Fatalf("need two minted keys, have %d", len(ids))
	}
	// One past its fixed expiry long ago, one idle long ago.
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '31 days' WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE api_keys SET idle_from = now() - interval '300 days', last_used_at = NULL WHERE id = $1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]string{"name": "room"}), h)
	if r.Status != http.StatusCreated {
		t.Fatalf("expired keys still count toward the cap: %d %s", r.Status, r.Body)
	}
	recent := r.JSON(t)["id"].(string)
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 day' WHERE id = $1`, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := accountsrepo.PurgeExpiredAPIKeys(context.Background(), a.Database, 180*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	for _, k := range servertest.KeysOf(t, a, p.Key) {
		left[k["id"].(string)] = true
	}
	if left[ids[0]] || left[ids[1]] {
		t.Errorf("keys expired over 30 days ago were kept: %v", left)
	}
	if !left[recent] {
		t.Error("a key expired a day ago was purged; it should stay listed for 30 days")
	}
}

// An expired key says so everywhere: on /mcp as 401 key_expired with no OAuth
// challenge, and a deploy key on a route it may not call as key_expired
// rather than deploy_only_key. Needs DB_DSN.
func TestExpiredKeyOnMCPAndOffDeployRoutes(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	p := a.NewPerson(t, "mcpexp")
	full := map[string]string{"X-API-Key": p.Key, "Content-Type": "application/json"}
	r := a.Do(t, http.MethodPost, "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "CI", "scope": "deploy"}), full)
	if r.Status != http.StatusCreated {
		t.Fatalf("mint: %d %s", r.Status, r.Body)
	}
	dk, id := r.JSON(t)["api_key"].(string), r.JSON(t)["id"].(string)
	if _, err := a.Database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	res := a.Do(t, http.MethodPost, "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
	if res.Status != http.StatusUnauthorized || res.JSON(t)["code"] != "key_expired" || res.Header.Get("WWW-Authenticate") != "" {
		t.Errorf("/mcp with an expired key: %d %v %s", res.Status, res.Header.Get("WWW-Authenticate"), res.Body)
	}
	if r := a.Do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": dk}); r.Status != http.StatusUnauthorized || r.JSON(t)["code"] != "key_expired" {
		t.Errorf("expired deploy key off its routes: %d %s", r.Status, r.Body)
	}
}
