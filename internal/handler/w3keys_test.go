package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// A deploy-only key creates, updates, rolls back and lists sites and makes
// preview links, and is refused (403 deploy_only_key) everywhere else, on the
// REST routes, the page-data routes and through /mcp alike. Needs DB_DSN.
func TestDeployOnlyKey(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "deploykey")
	full := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}

	if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "CI", "scope": "admin"}), full); r.status != http.StatusBadRequest {
		t.Fatalf("unknown scope: %d %s", r.status, r.body)
	}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]any{"name": "GitHub Actions", "scope": "deploy"}), full)
	if r.status != http.StatusCreated || r.json(t)["scope"] != "deploy" {
		t.Fatalf("mint deploy key: %d %s", r.status, r.body)
	}
	dk := r.json(t)["api_key"].(string)
	dh := map[string]string{"X-API-Key": dk, "Content-Type": "application/json"}
	files := func(body string) any { return map[string]any{"files": map[string]string{"index.html": body}} }

	// Allowed: one-call create, update, list, versions, roll back, preview link.
	if r := a.do(t, http.MethodPut, "/v1/sites/ci/files?create=1", jsonBody(files("v1")), dh); r.status != http.StatusCreated {
		t.Fatalf("PUT ?create=1 on a missing site: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/ci/files?create=1", jsonBody(files("v2")), dh); r.status != http.StatusOK {
		t.Fatalf("PUT ?create=1 on an existing site: %d %s", r.status, r.body)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/sites"},
		{http.MethodGet, "/v1/sites/ci/versions"},
		{http.MethodGet, "/v1/sites/ci/versions/1/files"},
		// This test server has no site hosts, so the route itself answers
		// 409 preview_unavailable: what matters is that the gate let it by.
		{http.MethodPost, "/v1/sites/ci/versions/1/preview-link"},
	} {
		if r := a.do(t, c.method, c.path, nil, dh); r.status/100 != 2 && r.json(t)["code"] != "preview_unavailable" {
			t.Errorf("deploy key %s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/ci/active-version", jsonBody(map[string]int{"version_number": 1}), dh); r.status != http.StatusOK {
		t.Errorf("deploy key rollback: %d %s", r.status, r.body)
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
		{http.MethodGet, "/v1/sites/ci/collections", nil},
		{http.MethodGet, "/v1/sites/ci/state", nil},
		{http.MethodPut, "/v1/sites/ci/state", map[string]any{"a": 1}},
		{http.MethodGet, "/v1/sites/ci/analytics", nil},
	} {
		var body = jsonBody(c.body)
		if c.body == nil {
			body = nil
		}
		r := a.do(t, c.method, c.path, body, dh)
		if r.status != http.StatusForbidden || r.json(t)["code"] != "deploy_only_key" {
			t.Errorf("deploy key %s %s: %d %s, want 403 deploy_only_key", c.method, c.path, r.status, r.body)
		}
	}

	// The same key on /mcp: tools behind deploy routes work, the rest are refused.
	mcpCall := func(tool string, args map[string]any) (string, bool) {
		r := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args}}),
			map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
		text, _, isErr := toolResultOf(t, r)
		return text, isErr
	}
	if text, isErr := mcpCall("list_sites", map[string]any{}); isErr {
		t.Errorf("list_sites with a deploy key: %s", text)
	}
	if text, isErr := mcpCall("delete_site", map[string]any{"site": "ci", "confirm_name": "ci"}); !isErr || !strings.Contains(text, "deploy_only_key") {
		t.Errorf("delete_site with a deploy key: %v %s", isErr, text)
	}

	// The full key is untouched, and the list shows the scope.
	if r := a.do(t, http.MethodGet, "/v1/me", nil, full); r.status != http.StatusOK {
		t.Fatalf("full key /v1/me: %d", r.status)
	}
	var sawDeploy bool
	for _, k := range keysOf(t, a, p.key) {
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
	a := newConnectorApp(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/v1/sites"}, {"POST", "/v1/sites/x"}, {"PUT", "/v1/sites/x"},
		{"POST", "/v1/sites/x/files"}, {"PUT", "/v1/sites/x/files"}, {"GET", "/v1/sites/x/versions"},
		{"GET", "/v1/sites/x/versions/1/files"}, {"GET", "/v1/sites/x/versions/1/files/a/b.html"},
		{"PUT", "/v1/sites/x/active-version"}, {"POST", "/v1/sites/x/versions/1/preview-link"},
		{"GET", "/mcp"}, {"POST", "/mcp"}, {"DELETE", "/mcp"},
	} {
		req, _ := http.NewRequest(c.method, a.srv.URL+c.path, nil)
		if _, pattern := a.mux.Handler(req); !auth.DeployKeyAllows(pattern) {
			t.Errorf("%s %s matched %q, which the deploy table does not list", c.method, c.path, pattern)
		}
	}
}

// Keys stop working after their fixed expiry or after going unused for
// KEY_IDLE_EXPIRY_DAYS, with a 401 that says which and what to do; 0 turns
// idle expiry off. Needs DB_DSN.
func TestKeyExpiry(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "expiry")
	full := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	for _, bad := range []int{0, -1, 3651} {
		if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]any{"name": "x", "expires_in_days": bad}), full); r.status != http.StatusBadRequest {
			t.Fatalf("expires_in_days %d: %d", bad, r.status)
		}
	}
	mint := func(name string, body map[string]any) (string, string) {
		body["name"] = name
		r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(body), full)
		if r.status != http.StatusCreated {
			t.Fatalf("mint %s: %d %s", name, r.status, r.body)
		}
		return r.json(t)["api_key"].(string), r.json(t)["id"].(string)
	}
	fixed, fixedID := mint("thirty days", map[string]any{"expires_in_days": 30})
	idle, idleID := mint("forgotten", map[string]any{})
	me := func(k string) resp { return a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": k}) }
	if me(fixed).status != http.StatusOK || me(idle).status != http.StatusOK {
		t.Fatal("fresh keys should work")
	}
	for _, k := range keysOf(t, a, p.key) {
		if k["id"] == fixedID && k["expires_at"] == nil {
			t.Errorf("fixed expiry not listed: %v", k)
		}
		if k["idle_expires_at"] == nil {
			t.Errorf("idle expiry not listed: %v", k)
		}
	}

	if _, err := a.database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, fixedID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE api_keys SET idle_from = now() - interval '200 days', last_used_at = now() - interval '181 days' WHERE id = $1`, idleID); err != nil {
		t.Fatal(err)
	}
	if r := me(fixed); r.status != http.StatusUnauthorized || r.json(t)["code"] != "key_expired" || !strings.Contains(string(r.body), "new key") {
		t.Errorf("expired key: %d %s", r.status, r.body)
	}
	if r := me(idle); r.status != http.StatusUnauthorized || r.json(t)["code"] != "key_expired_idle" || !strings.Contains(string(r.body), "180 days") {
		t.Errorf("idle key: %d %s", r.status, r.body)
	}
	for _, k := range keysOf(t, a, p.key) {
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

	withLimits(t, map[string]string{"KEY_IDLE_EXPIRY_DAYS": "0"})
	if r := me(idle); r.status != http.StatusOK {
		t.Errorf("idle expiry off: %d %s", r.status, r.body)
	}
	if r := me(fixed); r.status != http.StatusUnauthorized {
		t.Errorf("fixed expiry still applies with idle expiry off: %d", r.status)
	}
}

// PUT ?create=1 creates a missing site in the same call, and a POST onto an
// existing site says to use PUT. Needs DB_DSN.
func TestPutCreateIfMissing(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "upsert")
	h := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	files := map[string]any{"files": map[string]string{"index.html": "hi"}}
	if r := a.do(t, http.MethodPut, "/v1/sites/fresh/files", jsonBody(files), h); r.status != http.StatusNotFound || !strings.Contains(string(r.body), "create=1") {
		t.Fatalf("PUT without create on a missing site: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/fresh/files?create=maybe", jsonBody(files), h); r.status != http.StatusBadRequest {
		t.Fatalf("create=maybe: %d", r.status)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/admin/files?create=1", jsonBody(files), h); r.status != http.StatusBadRequest || r.json(t)["code"] != "name_reserved" {
		t.Fatalf("create=1 with a reserved name: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/fresh/files?create=1&publish=false", jsonBody(files), h); r.status != http.StatusBadRequest {
		t.Fatalf("create=1 with publish=false on a missing site: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/fresh/files?create=1", jsonBody(files), h); r.status != http.StatusCreated {
		t.Fatalf("create=1: %d %s", r.status, r.body)
	}
	r := a.do(t, http.MethodPost, "/v1/sites/fresh/files", jsonBody(files), h)
	if r.status != http.StatusConflict || r.json(t)["code"] != "site_exists" || !strings.Contains(string(r.body), "use PUT") {
		t.Fatalf("POST onto an existing site: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/fresh/files?create=1&publish=false", jsonBody(files), h); r.status != http.StatusOK {
		t.Fatalf("create=1 on an existing site keeps publish=false: %d %s", r.status, r.body)
	}
}

// Top pages and referring domains for the chosen range, most views first,
// from the people-only daily tables. Needs DB_DSN.
func TestSiteTopAnalytics(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "toppages")
	h := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	if r := a.do(t, http.MethodPost, "/v1/sites/shop/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "x"}}), h); r.status != http.StatusCreated {
		t.Fatalf("deploy: %d %s", r.status, r.body)
	}
	var siteID string
	if err := a.database.QueryRow(`SELECT s.id FROM sites s JOIN api_keys k ON k.user_id = s.user_id WHERE k.key_hash = encode(sha256($1::bytea), 'hex') AND s.name = 'shop'`, p.key).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO site_page_daily (site_id, day, path, views) VALUES ($1, current_date, '/', 5), ($1, current_date - 1, '/', 2), ($1, current_date, '/menu/', 9), ($1, current_date - 40, '/old/', 99)`,
		`INSERT INTO site_referrer_daily (site_id, day, domain, views) VALUES ($1, current_date, 'news.ycombinator.com', 4), ($1, current_date, 't.co', 6), ($1, current_date - 40, 'old.example', 50)`,
	} {
		if _, err := a.database.Exec(q, siteID); err != nil {
			t.Fatal(err)
		}
	}
	r := a.do(t, http.MethodGet, "/v1/sites/shop/analytics/top?days=30", nil, h)
	if r.status != http.StatusOK {
		t.Fatalf("top: %d %s", r.status, r.body)
	}
	body := string(r.body)
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
// quota applies. The admin's full key keeps both. Needs DB_DSN.
func TestDeployKeyNeverAdmin(t *testing.T) {
	a := newConnectorApp(t)
	withLimits(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "1"})
	boss := a.newPerson(t, "adminci")
	other := a.newPerson(t, "bystander")
	if _, err := a.database.Exec(`UPDATE users SET is_admin = true WHERE id = (SELECT user_id FROM api_keys WHERE key_hash = $1)`, db.HashAPIKey(boss.key)); err != nil {
		t.Fatal(err)
	}
	files := map[string]any{"files": map[string]string{"index.html": "x"}}
	hdr := func(k string) map[string]string {
		return map[string]string{"X-API-Key": k, "Content-Type": "application/json"}
	}
	if r := a.do(t, http.MethodPost, "/v1/sites/bystander-site/files", jsonBody(files), hdr(other.key)); r.status != http.StatusCreated {
		t.Fatalf("other's site: %d %s", r.status, r.body)
	}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]any{"name": "CI", "scope": "deploy"}), hdr(boss.key))
	if r.status != http.StatusCreated {
		t.Fatalf("mint: %d %s", r.status, r.body)
	}
	dk := r.json(t)["api_key"].(string)

	if r := a.do(t, http.MethodGet, "/v1/sites", nil, hdr(boss.key)); !strings.Contains(string(r.body), "bystander-site") {
		t.Fatalf("admin full key should list every site: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodGet, "/v1/sites", nil, hdr(dk)); r.status != http.StatusOK || strings.Contains(string(r.body), "bystander-site") {
		t.Errorf("deploy key listed another account's site: %d %s", r.status, r.body)
	}
	res := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list_sites", "arguments": map[string]any{}}}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
	if text, _, isErr := toolResultOf(t, res); isErr || strings.Contains(text, "bystander-site") {
		t.Errorf("MCP list_sites with a deploy key: %v %s", isErr, text)
	}

	// Quota 1: the deploy key's first site fits, the second is refused.
	if r := a.do(t, http.MethodPut, "/v1/sites/ci-one/files?create=1", jsonBody(files), hdr(dk)); r.status != http.StatusCreated {
		t.Fatalf("first site: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPut, "/v1/sites/ci-two/files?create=1", jsonBody(files), hdr(dk)); r.status != http.StatusForbidden || r.json(t)["code"] != "site_quota_reached" {
		t.Errorf("deploy key over the quota: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, "/v1/sites/ci-three/files", jsonBody(files), hdr(dk)); r.status != http.StatusForbidden || r.json(t)["code"] != "site_quota_reached" {
		t.Errorf("deploy key POST over the quota: %d %s", r.status, r.body)
	}
	if r := a.do(t, http.MethodPost, "/v1/sites/boss-two/files", jsonBody(files), hdr(boss.key)); r.status != http.StatusCreated {
		t.Errorf("admin full key stays exempt from the quota: %d %s", r.status, r.body)
	}
}

// Two CI jobs racing PUT ?create=1 on a site that does not exist yet: one
// creates it and the rest publish a new version, none gets site_exists.
// Needs DB_DSN.
func TestPutCreateRace(t *testing.T) {
	a := newConnectorApp(t)
	p := a.newPerson(t, "race")
	const n = 6
	statuses := make(chan int, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body, _ := json.Marshal(map[string]any{"files": map[string]string{"index.html": fmt.Sprint("v", i)}})
			req, _ := http.NewRequest(http.MethodPut, a.srv.URL+"/v1/sites/raced/files?create=1", bytes.NewReader(body))
			req.Header.Set("X-API-Key", p.key)
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
	if err := a.database.QueryRow(`SELECT count(*) FROM versions v JOIN sites s ON s.id = v.site_id
		JOIN api_keys k ON k.user_id = s.user_id WHERE k.key_hash = $1 AND s.name = 'raced'`, db.HashAPIKey(p.key)).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != n {
		t.Errorf("%d versions after %d uploads", versions, n)
	}
}

// Keys that stopped working leave room under MAX_KEYS_PER_ACCOUNT, and are
// deleted 30 days after they stopped. Needs DB_DSN.
func TestExpiredKeysFreeTheCapAndArePurged(t *testing.T) {
	a := newConnectorApp(t)
	withLimits(t, map[string]string{"MAX_KEYS_PER_ACCOUNT": "3"})
	p := a.newPerson(t, "keycap")
	h := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	var ids []string
	for i := len(keysOf(t, a, p.key)); i < 3; i++ {
		r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": fmt.Sprint("k", i)}), h)
		if r.status != http.StatusCreated {
			t.Fatalf("key %d: %d %s", i, r.status, r.body)
		}
		ids = append(ids, r.json(t)["id"].(string))
	}
	if r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "full"}), h); r.status != http.StatusConflict {
		t.Fatalf("at the cap: %d %s", r.status, r.body)
	}
	if len(ids) < 2 {
		t.Fatalf("need two minted keys, have %d", len(ids))
	}
	// One past its fixed expiry long ago, one idle long ago.
	if _, err := a.database.Exec(`UPDATE api_keys SET expires_at = now() - interval '31 days' WHERE id = $1`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE api_keys SET idle_from = now() - interval '300 days', last_used_at = NULL WHERE id = $1`, ids[1]); err != nil {
		t.Fatal(err)
	}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]string{"name": "room"}), h)
	if r.status != http.StatusCreated {
		t.Fatalf("expired keys still count toward the cap: %d %s", r.status, r.body)
	}
	recent := r.json(t)["id"].(string)
	if _, err := a.database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 day' WHERE id = $1`, recent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PurgeExpiredAPIKeys(context.Background(), a.database); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	for _, k := range keysOf(t, a, p.key) {
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
	a := newConnectorApp(t)
	p := a.newPerson(t, "mcpexp")
	full := map[string]string{"X-API-Key": p.key, "Content-Type": "application/json"}
	r := a.do(t, http.MethodPost, "/v1/me/keys", jsonBody(map[string]any{"name": "CI", "scope": "deploy"}), full)
	if r.status != http.StatusCreated {
		t.Fatalf("mint: %d %s", r.status, r.body)
	}
	dk, id := r.json(t)["api_key"].(string), r.json(t)["id"].(string)
	if _, err := a.database.Exec(`UPDATE api_keys SET expires_at = now() - interval '1 second' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	res := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}),
		map[string]string{"Content-Type": "application/json", "X-API-Key": dk, "MCP-Protocol-Version": "2025-06-18"})
	if res.status != http.StatusUnauthorized || res.json(t)["code"] != "key_expired" || res.header.Get("WWW-Authenticate") != "" {
		t.Errorf("/mcp with an expired key: %d %v %s", res.status, res.header.Get("WWW-Authenticate"), res.body)
	}
	if r := a.do(t, http.MethodGet, "/v1/me", nil, map[string]string{"X-API-Key": dk}); r.status != http.StatusUnauthorized || r.json(t)["code"] != "key_expired" {
		t.Errorf("expired deploy key off its routes: %d %s", r.status, r.body)
	}
}

// The Keys panel states KEY_IDLE_EXPIRY_DAYS, including when it is off.
func TestKeysPanelIdleCopyFollowsSetting(t *testing.T) {
	for v, want := range map[string]string{
		"90": "A key unused for 90 days stops working.",
		"0":  "Unused keys keep working until revoked.",
	} {
		withLimits(t, map[string]string{"KEY_IDLE_EXPIRY_DAYS": v})
		page, err := chromePage("showcase.html", chromeData{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(page), want) || strings.Contains(string(page), "unused for 180 days") {
			t.Errorf("KEY_IDLE_EXPIRY_DAYS=%s: Keys panel does not say %q", v, want)
		}
	}
}
