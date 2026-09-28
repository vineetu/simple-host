package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
)

// Operator take-down of a site and suspension of a person, end to end over
// HTTP against the real router. Needs DB_DSN (db/schema.sql applied).

func TestSuspendSiteEndToEnd(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	shopID := a.siteID(t, olive, "shop")
	oliveID, oh := a.userID(t, olive)
	const apex = "simple-host.test"
	host := oh + "." + pcSiteDomain
	const claimed = "olive-susp.simple-host.test"
	okey := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}
	// State and list writes are Origin-gated; a page on the site's own address.
	okeyO := map[string]string{"X-API-Key": olive.key, "Origin": "https://" + claimed}
	adminO := map[string]string{"X-API-Key": a.admin, "Origin": "https://" + claimed}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", claimed, "/v1/sites/shop/state", map[string]any{"n": 1}, okeyO); r.status != 200 {
		t.Fatalf("seed state: %d %s", r.status, r.body)
	}

	// Only the admin may take a site down, and a reason is required.
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "x"}, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
		t.Fatalf("non-admin suspend: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "  "}, admin); r.status != 400 || r.json(t)["code"] != "reason_required" {
		t.Fatalf("no reason: %d %s", r.status, r.body)
	}
	r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "Phishing report 2026-09-27"}, admin)
	if r.status != 200 || r.json(t)["suspended"] != true || r.json(t)["suspended_by"] != "site" {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if !a.sites.disk.IsSuspended(oliveID, "shop") {
		t.Fatal("suspend wrote no disk marker")
	}

	// Every address answers the take-down page, on every path.
	for _, tc := range []struct{ host, path string }{
		// (The person-path address of a site with its own address redirects
		// there; TestSuspendPersonEndToEnd covers the person path itself.)
		{claimed, "/"}, {claimed, "/sub/"}, {claimed, "/missing.png"},
		{apex, "/internal/suspended"},
	} {
		r := a.at(t, "GET", tc.host, tc.path, nil, nil)
		if r.status != http.StatusGone || !strings.Contains(string(r.body), "This site has been taken down") {
			t.Fatalf("%s%s: %d %s", tc.host, tc.path, r.status, r.body)
		}
	}
	// The owner's other site is untouched.
	if r := a.at(t, "GET", host, "/blog/", nil, nil); r.status != 200 {
		t.Fatalf("other site: %d", r.status)
	}

	// Changes are refused with a code; public reads of its data too.
	deny := []struct {
		method, host, path string
		body               any
		headers            map[string]string
	}{
		{"PUT", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "x"}}, okey},
		{"PUT", claimed, "/v1/sites/shop/state", map[string]any{"n": 2}, okeyO},
		{"PUT", claimed, "/v1/sites/shop/state", map[string]any{"n": 2}, adminO},
		{"POST", claimed, "/v1/sites/shop/collections/guests", map[string]any{"name": "x"}, okeyO},
		{"PUT", apex, "/v1/sites/shop/active-version", map[string]int{"version_number": 1}, okey},
		{"PATCH", apex, "/v1/sites/shop", map[string]string{"name": "shop2"}, okey},
		{"DELETE", apex, "/v1/sites/shop", nil, okey},
		{"PUT", apex, "/v1/sites/shop/visibility", map[string]string{"visibility": "public"}, okey},
		{"DELETE", apex, "/v1/sites/shop/domain", nil, okey},
	}
	for _, d := range deny {
		r := a.at(t, d.method, d.host, d.path, d.body, d.headers)
		if r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" {
			t.Fatalf("%s %s: %d %s", d.method, d.path, r.status, r.body)
		}
	}
	if r := a.at(t, "GET", claimed, "/v1/sites/shop/state", nil, nil); r.status != http.StatusForbidden || r.json(t)["code"] != "site_suspended" {
		t.Fatalf("public state read: %d %s", r.status, r.body)
	}
	// The owner still reads (and can export) it, and sees why it is down.
	if r := a.at(t, "GET", claimed, "/v1/sites/shop/state", nil, okey); r.status != 200 {
		t.Fatalf("owner state read: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites/shop/export.tar.gz", nil, okey); r.status != 200 {
		t.Fatalf("owner export: %d", r.status)
	}
	r = a.at(t, "GET", apex, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.body), `"suspended":true`) || !strings.Contains(string(r.body), "Phishing report 2026-09-27") {
		t.Fatalf("owner list: %s", r.body)
	}
	// The public person page no longer lists it.
	if _, err := a.database.Exec(`UPDATE sites SET visibility = 'public' WHERE user_id = $1`, oliveID); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "GET", host, "/", nil, nil); strings.Contains(string(r.body), `"name":"shop"`) || !strings.Contains(string(r.body), `"name":"blog"`) {
		t.Fatalf("person page lists a taken-down site: %s", r.body)
	}

	// A lost marker is put back from the database at boot.
	_ = a.sites.disk.SetSuspended(oliveID, "shop", false)
	a.sites.SyncSuspendMarkers(t.Context())
	if !a.sites.disk.IsSuspended(oliveID, "shop") {
		t.Fatal("marker sync did not restore the marker")
	}

	// Restore puts it back exactly as it was.
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.status != 200 || r.json(t)["suspended"] != false {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if a.sites.disk.IsSuspended(oliveID, "shop") {
		t.Fatal("restore left the marker")
	}
	if r := a.at(t, "GET", claimed, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("after restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", claimed, "/v1/sites/shop/state", nil, nil); r.status != 200 || !strings.Contains(strings.ReplaceAll(string(r.body), " ", ""), `"n":1`) {
		t.Fatalf("state after restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, okey); r.status != 200 {
		t.Fatalf("deploy after restore: %d %s", r.status, r.body)
	}
}

func TestSuspendPersonEndToEnd(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	shopID := a.siteID(t, olive, "shop")
	oliveID, oh := a.userID(t, olive)
	const apex = "simple-host.test"
	host := oh + "." + pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	admin := map[string]string{"X-API-Key": a.admin}
	token := a.connectResource(t, olive, a.srv.URL)
	bearer := map[string]string{"Authorization": "Bearer " + token}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, bearer); r.status != 200 {
		t.Fatalf("token before: %d %s", r.status, r.body)
	}
	// A site taken down on its own stays down when the person is re-enabled.
	a.deploy(t, olive, "blog")
	blogID := a.siteID(t, olive, "blog")
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "copyright notice"}, admin); r.status != 200 {
		t.Fatalf("suspend blog: %d %s", r.status, r.body)
	}

	if r := a.at(t, "POST", apex, "/v1/admin/users/"+a.siteIDOwner(t, olive)+"/suspend", map[string]string{"reason": "spam"}, admin); r.status != 200 {
		t.Fatalf("suspend person: %d %s", r.status, r.body)
	}
	// Key, connector token and site sessions stop working; nothing is deleted.
	r := a.at(t, "GET", apex, "/v1/sites", nil, okey)
	if r.status != http.StatusForbidden || r.json(t)["code"] != "account_suspended" || r.json(t)["reason"] != "spam" {
		t.Fatalf("key while suspended: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, bearer); r.status != http.StatusForbidden || r.json(t)["code"] != "account_suspended" {
		t.Fatalf("token while suspended: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", host, "/v1/sites/shop/state", map[string]any{"n": 1}, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + host}); r.status != http.StatusForbidden {
		t.Fatalf("state write while suspended: %d %s", r.status, r.body)
	}
	var keys int
	if err := a.database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, oliveID).Scan(&keys); err != nil || keys == 0 {
		t.Fatalf("keys deleted: %d %v", keys, err)
	}
	// Every site of theirs is down.
	for _, p := range []string{"/shop/", "/shop/sub/", "/shop/missing.png"} {
		if r := a.at(t, "GET", host, p, nil, nil); r.status != http.StatusGone {
			t.Fatalf("site %s while person suspended: %d", p, r.status)
		}
	}
	// The admin list shows both switches.
	r = a.at(t, "GET", apex, "/v1/admin/users", nil, admin)
	if !strings.Contains(string(r.body), `"suspended_by":"account"`) || !strings.Contains(string(r.body), `"suspended_by":"site"`) {
		t.Fatalf("admin list: %s", r.body)
	}
	// The admin account cannot be suspended.
	var adminID string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE is_admin ORDER BY created_at LIMIT 1`).Scan(&adminID); err == nil {
		if r := a.at(t, "POST", apex, "/v1/admin/users/"+adminID+"/suspend", map[string]string{"reason": "x"}, admin); r.status != 400 {
			t.Fatalf("suspend admin: %d %s", r.status, r.body)
		}
	}

	// Re-enable brings key, token and sites back, except a site taken down on its own.
	if r := a.at(t, "POST", apex, "/v1/admin/users/"+oliveID+"/enable", nil, admin); r.status != 200 {
		t.Fatalf("enable: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, okey); r.status != 200 {
		t.Fatalf("key after enable: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, bearer); r.status != 200 {
		t.Fatalf("token after enable: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host, "/shop/", nil, nil); r.status != 200 {
		t.Fatalf("site after enable: %d", r.status)
	}
	if r := a.at(t, "GET", host, "/blog/", nil, nil); r.status != http.StatusGone {
		t.Fatalf("separately suspended site after enable: %d", r.status)
	}
	_ = shopID
}

func TestAdminExportAll(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "shop")
	_, oh := a.userID(t, olive)
	_, sh := a.userID(t, oscar)
	const apex = "simple-host.test"
	if r := a.at(t, "GET", apex, "/v1/admin/export.tar.gz", nil, map[string]string{"X-API-Key": olive.key}); r.status != 404 {
		t.Fatalf("non-admin export all: %d", r.status)
	}
	r := a.at(t, "GET", apex, "/v1/admin/export.tar.gz", nil, map[string]string{"X-API-Key": a.admin})
	if r.status != 200 || r.header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("export all: %d %s", r.status, r.header.Get("Content-Type"))
	}
	gz, err := gzip.NewReader(bytes.NewReader(r.body))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	seen := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		seen[hdr.Name] = string(b)
	}
	for _, want := range []string{oh + "/shop/files/index.html", sh + "/shop/files/index.html", oh + "/shop/state.json"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("archive lacks %s; has %d entries", want, len(seen))
		}
	}
	if seen[oh+"/shop/files/index.html"] != "<h1>shop</h1>" {
		t.Fatalf("wrong content: %q", seen[oh+"/shop/files/index.html"])
	}
}

// Wrong keys typed at /admin are rate limited per address, so the sign-in
// form's "Too many tries" answer is real.
func TestAdminUsersWrongKeysRateLimited(t *testing.T) {
	withLimits(t, map[string]string{"RATE_LIMIT_SITE_OPS": "3,1m"})
	a := newPersonApp(t, "serve")
	bad := map[string]string{"X-API-Key": "sh_admin_" + strings.Repeat("0", 48)}
	for i := 0; i < 3; i++ {
		if r := a.at(t, "GET", "simple-host.test", "/v1/admin/users", nil, bad); r.status != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: %d %s", i, r.status, r.body)
		}
	}
	if r := a.at(t, "GET", "simple-host.test", "/v1/admin/users", nil, bad); r.status != http.StatusTooManyRequests {
		t.Fatalf("fourth wrong key: %d %s", r.status, r.body)
	}
}

// The /admin sign-in form is not offered over plain http except on this
// computer: the page's <plainHTTP> block, run under node.
func TestAdminSignInPlainHTTP(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	page, err := staticFiles.ReadFile("static/admin.html")
	if err != nil {
		t.Fatal(err)
	}
	src := string(page)
	i, j := strings.Index(src, "// <plainHTTP>"), strings.Index(src, "// </plainHTTP>")
	if i < 0 || j < i {
		t.Fatal("admin.html lacks the <plainHTTP> block")
	}
	cases := []struct {
		proto, host string
		refuse      bool
	}{
		{"http:", "203.0.113.7", true},
		{"http:", "builds.example.com", true},
		{"http:", "localhost", false},
		{"http:", "app.localhost", false},
		{"http:", "127.0.0.1", false},
		{"http:", "[::1]", false},
		{"https:", "203.0.113.7", false},
		{"https:", "builds.example.com", false},
	}
	var in [][2]string
	for _, c := range cases {
		in = append(in, [2]string{c.proto, c.host})
	}
	b, _ := json.Marshal(in)
	prog := src[i:j] + "\nprocess.stdout.write(JSON.stringify(" + string(b) + ".map(function(c){return keyOverPlainHTTP(c[0],c[1]);})));"
	out, err := exec.Command(node, "-e", prog).Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	var got []bool
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	for k, c := range cases {
		if got[k] != c.refuse {
			t.Errorf("%s//%s: refuse=%v, want %v", c.proto, c.host, got[k], c.refuse)
		}
	}
	if !strings.Contains(src, "if(keyOverPlainHTTP(location.protocol, location.hostname)){") {
		t.Error("renderSignIn does not check keyOverPlainHTTP before the form")
	}
}
