package servertest

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/web"
)

// A deploy-only key really publishes: JSON create and update, version
// listing, rollback and the MCP list, each served by the real Sites routes,
// and it is refused at the scope gate on every storage route, reads and
// writes alike (the gate answers before any storage handler runs).
func TestRkDeployOnlyKeyPublishesAndNeverReachesStorage(t *testing.T) {
	a := NewPrivateApp(t)
	owner := a.NewPerson(t, "deploykey")
	dk, _ := rkMintDeploy(t, a, owner)
	key := rkKey(dk)

	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/ci/files?create=1", rkFiles("v1"), key); r.Status != http.StatusCreated {
		t.Fatalf("create with a deploy key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/ci/files?create=1", rkFiles("v2"), key); r.Status != http.StatusOK {
		t.Fatalf("update with a deploy key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites/ci/versions", nil, key); r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"version_number":2`) {
		t.Fatalf("versions with a deploy key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites/ci/versions/1/files/index.html", nil, key); r.Status != http.StatusOK || string(r.Body) != "v1" {
		t.Fatalf("old version file with a deploy key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/ci/active-version", map[string]int{"version_number": 1}, key); r.Status != http.StatusOK {
		t.Fatalf("rollback with a deploy key: %d %s", r.Status, r.Body)
	}
	var active int
	if err := a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, a.SiteID(t, owner, "ci")).Scan(&active); err != nil || active != 1 {
		t.Fatalf("active version after rollback: %d %v", active, err)
	}
	if text, isErr := rkMCPKey(t, a, dk, "list_sites", map[string]any{}); isErr || !strings.Contains(text, "ci") {
		t.Fatalf("MCP list_sites with a deploy key: %v %s", isErr, text)
	}
	if text, isErr := rkMCPKey(t, a, dk, "delete_site", map[string]any{"site": "ci", "confirm_name": "ci"}); !isErr || !strings.Contains(text, "deploy_only_key") {
		t.Fatalf("MCP delete_site with a deploy key: %v %s", isErr, text)
	}

	// The owner's storage exists; the deploy key cannot touch any of it.
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/ci/storage/resources/vault", map[string]any{"kind": "kv"}, rkKey(owner.Key)); r.Status != http.StatusCreated {
		t.Fatalf("owner resource: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/v1/sites/ci/storage/resources", nil},
		{"PUT", "/v1/sites/ci/storage/resources/vault", map[string]any{"kind": "kv"}},
		{"DELETE", "/v1/sites/ci/storage/resources/vault", nil},
		{"GET", "/v1/sites/ci/storage/usage", nil},
		{"PUT", "/v1/sites/ci/storage/kv/vault/keys/a", map[string]any{"value": 1}},
		{"GET", "/v1/sites/ci/storage/kv/vault/keys/a", nil},
		{"DELETE", "/v1/sites/ci/storage/kv/vault/keys/a", nil},
	} {
		rkRefusal(t, c.method+" "+c.path, a.At(t, c.method, PcSiteDomain, c.path, c.body, key), http.StatusForbidden, "deploy_only_key")
	}
}

// A deploy key minted by an admin carries no admin powers: it lists only the
// account's own sites, and the owner site quota applies to it; the admin's
// full key stays exempt.
func TestRkDeployKeyNeverAdminAndKeepsTheQuota(t *testing.T) {
	a := NewPrivateApp(t, rkEdition(t, map[string]string{"MAX_SITES_PER_ACCOUNT": "1"}))
	boss, other := a.NewPerson(t, "adminci"), a.NewPerson(t, "bystander")
	if _, err := a.Database.Exec(`UPDATE users SET is_admin = true WHERE username = $1`, boss.Email); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/bystander-site/files", rkFiles("x"), rkKey(other.Key)); r.Status != http.StatusCreated {
		t.Fatalf("other's site: %d %s", r.Status, r.Body)
	}
	dk, _ := rkMintDeploy(t, a, boss)

	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites", nil, rkKey(boss.Key)); strings.Contains(string(r.Body), "bystander-site") {
		t.Fatalf("admin's default list leaked another account's site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites?all=1", nil, rkKey(boss.Key)); !strings.Contains(string(r.Body), "bystander-site") {
		t.Fatalf("admin full key with all=1 should list every site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites?all=1", nil, rkKey(dk)); r.Status != http.StatusForbidden {
		t.Fatalf("all=1 with a deploy key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites", nil, rkKey(dk)); r.Status != http.StatusOK || strings.Contains(string(r.Body), "bystander-site") {
		t.Fatalf("deploy key listed another account's site: %d %s", r.Status, r.Body)
	}
	if text, isErr := rkMCPKey(t, a, dk, "list_sites", map[string]any{}); isErr || strings.Contains(text, "bystander-site") {
		t.Fatalf("MCP list_sites with an admin-issued deploy key: %v %s", isErr, text)
	}

	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/ci-one/files?create=1", rkFiles("x"), rkKey(dk)); r.Status != http.StatusCreated {
		t.Fatalf("first site: %d %s", r.Status, r.Body)
	}
	rkRefusal(t, "PUT create over the quota", a.At(t, "PUT", PcSiteDomain, "/v1/sites/ci-two/files?create=1", rkFiles("x"), rkKey(dk)), http.StatusForbidden, "site_quota_reached")
	rkRefusal(t, "POST over the quota", a.At(t, "POST", PcSiteDomain, "/v1/sites/ci-three/files", rkFiles("x"), rkKey(dk)), http.StatusForbidden, "site_quota_reached")
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/boss-two/files", rkFiles("x"), rkKey(boss.Key)); r.Status != http.StatusCreated {
		t.Fatalf("admin full key stays exempt from the quota: %d %s", r.Status, r.Body)
	}
}

// Sites in Recently deleted still count against the owner's site quota.
func TestRkDeletedSitesCountTowardQuota(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	uid, _ := a.UserID(t, olive)
	if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
		SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, a.Sites.Limits.MaxSitesPerAccount); err != nil {
		t.Fatal(err)
	}
	rkRefusal(t, "create over the cap", a.At(t, "POST", PcSiteDomain, "/v1/sites/fresh/files", rkFiles("x"), rkKey(olive.Key)), http.StatusForbidden, "site_quota_reached")
}

// An upload link backed by a deploy-only key commits a real archive update,
// the new file is served by Sites, and a revoked key or a disconnected app
// ends its link without adding a version.
func TestRkUploadLinkWithDeployKeyDeploysUntilRevoked(t *testing.T) {
	a := NewPrivateApp(t)
	owner := a.NewPerson(t, "upload-owner")
	clientID := a.RegisterClient(t, TestRedirect)
	token := a.Connect(t, owner, clientID, TestRedirect)["access_token"].(string)
	args := map[string]any{"site": "archive", "archive": true}
	mint := func(apiKey string) string {
		t.Helper()
		var r Resp
		if apiKey == "" {
			r = a.Rpc(t, token, "tools/call", map[string]any{"name": "create_upload_link", "arguments": args})
		} else {
			r = a.At(t, "POST", PcSiteDomain, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call",
				"params": map[string]any{"name": "create_upload_link", "arguments": args}}, rkKey(apiKey))
		}
		_, out, failed := ToolResultOf(t, r)
		if failed {
			t.Fatalf("mint upload link: %s", r.Body)
		}
		u, err := url.Parse(out["upload_url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return u.Path
	}
	put := func(method, path, page string) Resp {
		return a.Do(t, method, path, strings.NewReader(rkTarGz(t, page)), map[string]string{"Content-Type": "application/gzip"})
	}
	if r := put("PUT", mint(""), "<h1>one</h1>"); r.Status != http.StatusCreated || r.JSON(t)["active_version"] != float64(1) {
		t.Fatalf("create through the link: %d %s", r.Status, r.Body)
	}
	dk, keyID := rkMintDeploy(t, a, owner)
	path := mint(dk)
	if r := put("POST", path, "<h1>two</h1>"); r.Status != http.StatusOK || r.JSON(t)["active_version"] != float64(2) {
		t.Fatalf("deploy-only update through the link: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites/archive/versions/2/files/index.html", nil, rkKey(owner.Key)); r.Status != http.StatusOK || !strings.Contains(string(r.Body), "<h1>two</h1>") {
		t.Fatalf("committed file: %d %s", r.Status, r.Body)
	}
	if r := put("POST", path, "<h1>again</h1>"); r.Status != http.StatusNotFound || r.JSON(t)["code"] != "upload_link_invalid" {
		t.Fatalf("a used link was accepted: %d %s", r.Status, r.Body)
	}

	assertEnded := func(what, path string) {
		t.Helper()
		rkRefusal(t, what, put("PUT", path, "<h1>late</h1>"), http.StatusForbidden, "upload_link_signed_out")
		var version, versions int
		if err := a.Database.QueryRow(`SELECT s.active_version, (SELECT count(*) FROM versions v WHERE v.site_id = s.id) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.username = $1 AND s.name = 'archive'`, owner.Email).Scan(&version, &versions); err != nil {
			t.Fatal(err)
		}
		if version != 2 || versions != 2 {
			t.Fatalf("%s deployed: active=%d versions=%d", what, version, versions)
		}
	}
	ended := mint("")
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/me/connections/"+clientID, nil, rkKey(owner.Key)); r.Status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	assertEnded("link of a disconnected app", ended)
	ended = mint(dk)
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/me/keys/"+keyID, nil, rkKey(owner.Key)); r.Status != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", r.Status, r.Body)
	}
	assertEnded("link of a revoked key", ended)
}

// A deploy-only credential is refused MCP Events and is not offered the
// notification tools.
func TestRkNotifyConnectorRefusesDeployScope(t *testing.T) {
	a := NewPrivateApp(t)
	if err := a.Access.SetPasscodeKey(TestPasscodeKey); err != nil {
		t.Fatal(err)
	}
	a.Conn.SetEventsBackend(a.Data.EventsBackend())
	owner := a.NewPerson(t, "notify-deploy")
	dk, _ := rkMintDeploy(t, a, owner)
	rpc := func(method string) Resp {
		return a.At(t, "POST", PcSiteDomain, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{}}, rkKey(dk))
	}
	for _, method := range []string{"events/subscribe", "events/unsubscribe"} {
		e, _ := rpc(method).JSON(t)["error"].(map[string]any)
		if e["code"] != float64(mcp.EventCodeForbidden) {
			t.Fatalf("%s with a deploy key: %v", method, e)
		}
	}
	tools := rpc("tools/list").JSON(t)["result"].(map[string]any)["tools"].([]any)
	for _, raw := range tools {
		if name := raw.(map[string]any)["name"]; name == "list_notifications" || name == "remove_notification" {
			t.Fatalf("connector lists %v for a deploy key", name)
		}
	}
}

// Wrong keys typed against the admin users API are rate limited per address.
func TestRkAdminUsersWrongKeysRateLimited(t *testing.T) {
	a := NewPersonApp(t, "serve", rkEdition(t, map[string]string{"RATE_LIMIT_SITE_OPS": "3,1m"}))
	bad := rkKey("sh_admin_" + strings.Repeat("0", 48))
	for i := 0; i < 3; i++ {
		if r := a.At(t, "GET", PcSiteDomain, "/v1/admin/users", nil, bad); r.Status != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", PcSiteDomain, "/v1/admin/users", nil, bad); r.Status != http.StatusTooManyRequests {
		t.Fatalf("fourth wrong key: %d %s", r.Status, r.Body)
	}
}

// Another account's site is out of reach for every way in: the connector's
// tools and a whole-server bearer token alike answer 404, and nothing of the
// site changes (update, delete, rollback, a named version's file, the REST
// routes with a plain key).
func TestRkAnotherAccountsSiteIsOutOfReach(t *testing.T) {
	a := NewPrivateApp(t)
	ann, bob := a.NewPerson(t, "ann"), a.NewPerson(t, "bob")
	bobKey := rkKey(bob.Key)
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/bobs-page/files", rkFiles("bob"), bobKey); r.Status != http.StatusCreated {
		t.Fatalf("bob deploy: %d %s", r.Status, r.Body)
	}
	access := a.Connect(t, ann, a.RegisterClient(t, TestRedirect), TestRedirect)["access_token"].(string)
	for _, call := range []map[string]any{
		{"name": "delete_site", "arguments": map[string]any{"site": "bobs-page", "confirm_name": "bobs-page"}},
		{"name": "update_site", "arguments": map[string]any{"site": "bobs-page", "files": map[string]any{"index.html": "ann was here"}}},
		{"name": "rollback_site", "arguments": map[string]any{"site": "bobs-page", "version": 1}},
		{"name": "read_site_file", "arguments": map[string]any{"site": "bobs-page", "path": "index.html", "version": 1}},
		{"name": "set_visibility", "arguments": map[string]any{"site": "bobs-page", "visibility": "public"}},
	} {
		text, _, isErr := ToolResultOf(t, a.Rpc(t, access, "tools/call", call))
		if !isErr || !(strings.Contains(text, "404") || strings.Contains(text, "no site named")) {
			t.Errorf("%s on another person's site: %q", call["name"], text)
		}
	}
	// An /mcp token is not a REST credential; a whole-server token is, with
	// exactly the person's power: still nothing of bob's.
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites", nil, map[string]string{"Authorization": "Bearer " + access}); r.Status != http.StatusUnauthorized {
		t.Fatalf("/mcp token accepted on /v1: %d", r.Status)
	}
	bearer := map[string]string{"Authorization": "Bearer " + a.ConnectResource(t, ann, a.Srv.URL)}
	// Names are per account: without owner= these routes look in ann's own
	// namespace (and find nothing), with owner=<bob> they name bob's site.
	_, bobHandle := a.UserID(t, bob)
	annKey := rkKey(ann.Key)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"DELETE", "/v1/sites/bobs-page", nil},
		{"PUT", "/v1/sites/bobs-page/active-version", map[string]int{"version_number": 1}},
		{"GET", "/v1/sites/bobs-page/versions", nil},
		{"GET", "/v1/sites/bobs-page/versions/1/files/index.html", nil},
		{"PATCH", "/v1/sites/bobs-page", map[string]string{"name": "annspage"}},
		{"DELETE", "/v1/sites/bobs-page?owner=" + bobHandle, nil},
		{"PUT", "/v1/sites/bobs-page/files?owner=" + bobHandle, rkFiles("ann was here")},
		{"PUT", "/v1/sites/bobs-page/files?create=1&owner=" + bobHandle, rkFiles("ann was here")},
		{"PUT", "/v1/sites/bobs-page/active-version?owner=" + bobHandle, map[string]int{"version_number": 1}},
		{"GET", "/v1/sites/bobs-page/versions?owner=" + bobHandle, nil},
		{"GET", "/v1/sites/bobs-page/versions/1/files/index.html?owner=" + bobHandle, nil},
		{"PATCH", "/v1/sites/bobs-page?owner=" + bobHandle, map[string]string{"name": "annspage"}},
	} {
		for who, h := range map[string]map[string]string{"bearer": bearer, "key": annKey} {
			if r := a.At(t, c.method, PcSiteDomain, c.path, c.body, h); r.Status != http.StatusNotFound {
				t.Errorf("%s %s with ann's %s: %d %s, want 404", c.method, c.path, who, r.Status, r.Body)
			}
		}
	}
	var active int
	_ = a.Database.QueryRow(`SELECT active_version FROM sites WHERE name = 'bobs-page'`).Scan(&active)
	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites/bobs-page/versions/1/files/index.html", nil, bobKey); active != 1 || string(r.Body) != "bob" {
		t.Fatalf("bob's site changed: active=%d %q", active, r.Body)
	}
	var annSites int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.username = $1`, ann.Email).Scan(&annSites); err != nil || annSites != 0 {
		t.Fatalf("a refused request made ann a site: %d %v", annSites, err)
	}
}

// The admin page's per-site actions reach anyone's site by id and act as its
// owner; the id has to match the site resolved by owner and name, and a
// delete honours a site hold.
func TestRkAdminSiteActionsActAsOwner(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "asolive"), a.NewPerson(t, "asoscar")
	a.Deploy(t, olive, "shop")
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/shop/files", rkFiles("v2"), rkKey(olive.Key)); r.Status != http.StatusOK {
		t.Fatalf("v2: %d %s", r.Status, r.Body)
	}
	a.Deploy(t, oscar, "blog")
	shop := a.SiteID(t, olive, "shop")
	admin := rkKey(a.Admin)

	if r := a.At(t, "GET", PcSiteDomain, "/v1/sites/shop/versions", nil, admin); r.Status != http.StatusNotFound {
		t.Fatalf("bare owner route as admin: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct {
		key, path string
		want      int
	}{
		{oscar.Key, "/v1/admin/sites/" + shop + "/versions", 404},
		{olive.Key, "/v1/admin/sites/" + shop + "/versions", 404},
		{a.Admin, "/v1/admin/sites/not-a-uuid/versions", 404},
		{a.Admin, "/v1/admin/sites/00000000-0000-0000-0000-000000000000/versions", 404},
		{a.Admin, "/v1/admin/sites/" + strings.ToUpper(shop) + "/versions", 200},
	} {
		if r := a.At(t, "GET", PcSiteDomain, c.path, nil, rkKey(c.key)); r.Status != c.want {
			t.Errorf("GET %s: %d, want %d (%s)", c.path, r.Status, c.want, r.Body)
		}
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/admin/sites/"+shop+"/active-version", map[string]int{"version_number": 1}, admin); r.Status != http.StatusOK {
		t.Fatalf("make active: %d %s", r.Status, r.Body)
	}
	var active int
	_ = a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, shop).Scan(&active)
	if active != 1 {
		t.Fatalf("active version %d", active)
	}

	// The id rides along: a lookup by owner and name that lands on another
	// site (a rename, or a new site under the old name) is refused.
	oliveID, _ := a.UserID(t, olive)
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), web.AdminSiteKey{}, "00000000-0000-0000-0000-000000000000"))
	if _, err := a.Sites.SiteForCaller(req, oliveID, "shop"); err != sql.ErrNoRows {
		t.Fatalf("mismatched id: %v", err)
	}
	req = req.WithContext(context.WithValue(req.Context(), web.AdminSiteKey{}, shop))
	if s, err := a.Sites.SiteForCaller(req, oliveID, "shop"); err != nil || s.ID != shop {
		t.Fatalf("matching id: %v", err)
	}

	// A held site stays as it is, for the admin as for its owner.
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+shop+"/suspend", map[string]string{"reason": "test"}, admin); r.Status != http.StatusOK {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/admin/sites/"+shop, nil, admin); r.Status != http.StatusForbidden {
		t.Fatalf("admin delete of a held site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/admin/sites/"+shop+"/restore", nil, admin); r.Status != http.StatusOK {
		t.Fatalf("lift: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/admin/sites/"+shop, nil, rkKey(oscar.Key)); r.Status != http.StatusNotFound {
		t.Fatalf("non-admin delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/admin/sites/"+shop, nil, admin); r.Status != http.StatusNoContent {
		t.Fatalf("admin delete: %d %s", r.Status, r.Body)
	}
	var deleted bool
	_ = a.Database.QueryRow(`SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, shop).Scan(&deleted)
	if !deleted {
		t.Fatal("site not in Recently deleted")
	}
}

// PUT to a site that was never created refuses without create=1 and creates
// it with the opt-in; a POST onto an existing site says to use PUT.
func TestRkPutCreateIfMissing(t *testing.T) {
	a := NewPrivateApp(t)
	p := a.NewPerson(t, "upsert")
	h := rkKey(p.Key)
	r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/fresh/files", rkFiles("hi"), h)
	if r.Status != http.StatusNotFound || !strings.Contains(string(r.Body), "create=1") {
		t.Fatalf("PUT without create on a missing site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/fresh/files?create=maybe", rkFiles("hi"), h); r.Status != http.StatusBadRequest {
		t.Fatalf("create=maybe: %d", r.Status)
	}
	rkRefusal(t, "create=1 with a reserved name", a.At(t, "PUT", PcSiteDomain, "/v1/sites/admin/files?create=1", rkFiles("hi"), h), http.StatusBadRequest, "name_reserved")
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/fresh/files?create=1&publish=false", rkFiles("hi"), h); r.Status != http.StatusBadRequest {
		t.Fatalf("create=1 with publish=false on a missing site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/fresh/files?create=1", rkFiles("hi"), h); r.Status != http.StatusCreated {
		t.Fatalf("create=1: %d %s", r.Status, r.Body)
	}
	r = a.At(t, "POST", PcSiteDomain, "/v1/sites/fresh/files", rkFiles("hi"), h)
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "site_exists" || !strings.Contains(string(r.Body), "use PUT") {
		t.Fatalf("POST onto an existing site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", PcSiteDomain, "/v1/sites/fresh/files?create=1&publish=false", rkFiles("hi"), h); r.Status != http.StatusOK {
		t.Fatalf("create=1 on an existing site keeps publish=false: %d %s", r.Status, r.Body)
	}
}
