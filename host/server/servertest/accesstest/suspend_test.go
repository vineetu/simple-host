package accesstest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/server/servertest"
)

// Operator take-down of a site and suspension of a person, end to end over
// HTTP against the real router. Needs DB_DSN (host/db/schema.sql applied).

func TestSuspendSiteEndToEnd(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	shopID := a.SiteID(t, olive, "shop")
	oliveID, oh := a.UserID(t, olive)
	const apex = "simple-host.test"
	host := oh + "." + servertest.PcSiteDomain
	const claimed = "olive-susp.simple-host.test"
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}
	// Saved-data calls from a page on the site's own address.
	okeyO := map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + claimed}
	adminO := map[string]string{"X-API-Key": a.Admin, "Origin": "https://" + claimed}
	data := "/v1/sites/shop/storage/kv/notes/keys/n"
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/resources/notes", servertest.StorageRules("kv", servertest.RulesPublic), okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", claimed, data, map[string]any{"value": map[string]int{"n": 1}}, okeyO); r.Status != 200 {
		t.Fatalf("seed data: %d %s", r.Status, r.Body)
	}

	// Only the admin may take a site down, and a reason is required.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "x"}, map[string]string{"X-API-Key": oscar.Key}); r.Status != 404 {
		t.Fatalf("non-admin suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "  "}, admin); r.Status != 400 || r.JSON(t)["code"] != "reason_required" {
		t.Fatalf("no reason: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "Phishing report 2026-09-27"}, admin)
	if r.Status != 200 || r.JSON(t)["suspended"] != true || r.JSON(t)["suspended_by"] != "site" {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if !a.Sites.Disk.IsSuspended(oliveID, "shop") {
		t.Fatal("suspend wrote no disk marker")
	}

	// Every address answers the take-down page, on every path.
	for _, tc := range []struct{ host, path string }{
		// (The person-path address of a site with its own address redirects
		// there; TestSuspendPersonEndToEnd covers the person path itself.)
		{claimed, "/"}, {claimed, "/sub/"}, {claimed, "/missing.png"},
		{apex, "/internal/suspended"},
	} {
		r := a.At(t, "GET", tc.host, tc.path, nil, nil)
		if r.Status != http.StatusGone || !strings.Contains(string(r.Body), "This site has been taken down") {
			t.Fatalf("%s%s: %d %s", tc.host, tc.path, r.Status, r.Body)
		}
	}
	// The owner's other site is untouched.
	if r := a.At(t, "GET", host, "/blog/", nil, nil); r.Status != 200 {
		t.Fatalf("other site: %d", r.Status)
	}

	// Changes are refused with a code; public reads of its data too.
	deny := []struct {
		method, host, path string
		body               any
		headers            map[string]string
	}{
		{"PUT", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "x"}}, okey},
		{"PUT", claimed, data, map[string]any{"value": 2}, okeyO},
		{"PUT", claimed, data, map[string]any{"value": 2}, adminO},
		{"PUT", apex, "/v1/sites/shop/active-version", map[string]int{"version_number": 1}, okey},
		{"PATCH", apex, "/v1/sites/shop", map[string]string{"name": "shop2"}, okey},
		{"DELETE", apex, "/v1/sites/shop", nil, okey},
		{"PUT", apex, "/v1/sites/shop/visibility", map[string]string{"visibility": "public"}, okey},
		{"DELETE", apex, "/v1/sites/shop/domain", nil, okey},
	}
	for _, d := range deny {
		r := a.At(t, d.method, d.host, d.path, d.body, d.headers)
		if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
			t.Fatalf("%s %s: %d %s", d.method, d.path, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", claimed, data, nil, nil); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_suspended" {
		t.Fatalf("public data read: %d %s", r.Status, r.Body)
	}
	// The owner can still export it, and sees why it is down.
	if r := a.At(t, "GET", apex, "/v1/sites/shop/export.tar.gz", nil, okey); r.Status != 200 {
		t.Fatalf("owner export: %d", r.Status)
	}
	r = a.At(t, "GET", apex, "/v1/sites", nil, okey)
	if !strings.Contains(string(r.Body), `"suspended":true`) || !strings.Contains(string(r.Body), "Phishing report 2026-09-27") {
		t.Fatalf("owner list: %s", r.Body)
	}
	// The public person page no longer lists it.
	if _, err := a.Database.Exec(`UPDATE sites SET visibility = 'public' WHERE user_id = $1`, oliveID); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", host, "/", nil, nil); strings.Contains(string(r.Body), `"name":"shop"`) || !strings.Contains(string(r.Body), `"name":"blog"`) {
		t.Fatalf("person page lists a taken-down site: %s", r.Body)
	}

	// A lost marker is put back from the database at boot.
	_ = a.Sites.Disk.SetSuspended(oliveID, "shop", false)
	a.Access.SyncSuspendMarkers(t.Context())
	if !a.Sites.Disk.IsSuspended(oliveID, "shop") {
		t.Fatal("marker sync did not restore the marker")
	}

	// Restore puts it back exactly as it was.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.Status != 200 || r.JSON(t)["suspended"] != false {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if a.Sites.Disk.IsSuspended(oliveID, "shop") {
		t.Fatal("restore left the marker")
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("after restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", claimed, data, nil, nil); r.Status != 200 || !strings.Contains(strings.ReplaceAll(string(r.Body), " ", ""), `"n":1`) {
		t.Fatalf("data after restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, okey); r.Status != 200 {
		t.Fatalf("deploy after restore: %d %s", r.Status, r.Body)
	}
}

func TestSuspendPersonEndToEnd(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	shopID := a.SiteID(t, olive, "shop")
	oliveID, oh := a.UserID(t, olive)
	const apex = "simple-host.test"
	host := oh + "." + servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	admin := map[string]string{"X-API-Key": a.Admin}
	token := a.ConnectResource(t, olive, a.Srv.URL)
	bearer := map[string]string{"Authorization": "Bearer " + token}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, bearer); r.Status != 200 {
		t.Fatalf("token before: %d %s", r.Status, r.Body)
	}
	// A site taken down on its own stays down when the person is re-enabled.
	a.Deploy(t, olive, "blog")
	blogID := a.SiteID(t, olive, "blog")
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+blogID+"/suspend", map[string]string{"reason": "copyright notice"}, admin); r.Status != 200 {
		t.Fatalf("suspend blog: %d %s", r.Status, r.Body)
	}

	if r := a.At(t, "POST", apex, "/v1/admin/users/"+a.SiteIDOwner(t, olive)+"/suspend", map[string]string{"reason": "spam"}, admin); r.Status != 200 {
		t.Fatalf("suspend person: %d %s", r.Status, r.Body)
	}
	// Key, connector token and site sessions stop working; nothing is deleted.
	r := a.At(t, "GET", apex, "/v1/sites", nil, okey)
	if r.Status != http.StatusForbidden || r.JSON(t)["code"] != "account_suspended" || r.JSON(t)["reason"] != "spam" {
		t.Fatalf("key while suspended: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, bearer); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "account_suspended" {
		t.Fatalf("token while suspended: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", host, "/v1/sites/shop/storage/kv/notes/keys/n", map[string]any{"value": 1}, map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + host}); r.Status != http.StatusForbidden {
		t.Fatalf("data write while suspended: %d %s", r.Status, r.Body)
	}
	var keys int
	if err := a.Database.QueryRow(`SELECT count(*) FROM api_keys WHERE user_id = $1`, oliveID).Scan(&keys); err != nil || keys == 0 {
		t.Fatalf("keys deleted: %d %v", keys, err)
	}
	// Every site of theirs is down.
	for _, p := range []string{"/shop/", "/shop/sub/", "/shop/missing.png"} {
		if r := a.At(t, "GET", host, p, nil, nil); r.Status != http.StatusGone {
			t.Fatalf("site %s while person suspended: %d", p, r.Status)
		}
	}
	// The admin list shows both switches.
	r = a.At(t, "GET", apex, "/v1/admin/users", nil, admin)
	if !strings.Contains(string(r.Body), `"suspended_by":"account"`) || !strings.Contains(string(r.Body), `"suspended_by":"site"`) {
		t.Fatalf("admin list: %s", r.Body)
	}
	// The admin account cannot be suspended.
	var adminID string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE is_admin ORDER BY created_at LIMIT 1`).Scan(&adminID); err == nil {
		if r := a.At(t, "POST", apex, "/v1/admin/users/"+adminID+"/suspend", map[string]string{"reason": "x"}, admin); r.Status != 400 {
			t.Fatalf("suspend admin: %d %s", r.Status, r.Body)
		}
	}

	// Re-enable brings key, token and sites back, except a site taken down on its own.
	if r := a.At(t, "POST", apex, "/v1/admin/users/"+oliveID+"/enable", nil, admin); r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, okey); r.Status != 200 {
		t.Fatalf("key after enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, bearer); r.Status != 200 {
		t.Fatalf("token after enable: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host, "/shop/", nil, nil); r.Status != 200 {
		t.Fatalf("site after enable: %d", r.Status)
	}
	if r := a.At(t, "GET", host, "/blog/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("separately suspended site after enable: %d", r.Status)
	}
	_ = shopID
}

func TestAdminExportAll(t *testing.T) {
	a := servertest.NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "shop")
	_, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	const apex = "simple-host.test"
	if r := a.At(t, "GET", apex, "/v1/admin/export.tar.gz", nil, map[string]string{"X-API-Key": olive.Key}); r.Status != 404 {
		t.Fatalf("non-admin export all: %d", r.Status)
	}
	r := a.At(t, "GET", apex, "/v1/admin/export.tar.gz", nil, map[string]string{"X-API-Key": a.Admin})
	if r.Status != 200 || r.Header.Get("Content-Type") != "application/gzip" {
		t.Fatalf("export all: %d %s", r.Status, r.Header.Get("Content-Type"))
	}
	gz, err := gzip.NewReader(bytes.NewReader(r.Body))
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
	for _, want := range []string{oh + "/shop/files/index.html", sh + "/shop/files/index.html"} {
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
	l := servertest.WithLimits(t, map[string]string{"RATE_LIMIT_SITE_OPS": "3,1m"})
	a := servertest.NewPersonApp(t, "serve", edition.Default(l))
	bad := map[string]string{"X-API-Key": "sh_admin_" + strings.Repeat("0", 48)}
	for i := 0; i < 3; i++ {
		if r := a.At(t, "GET", "simple-host.test", "/v1/admin/users", nil, bad); r.Status != http.StatusUnauthorized {
			t.Fatalf("wrong key %d: %d %s", i, r.Status, r.Body)
		}
	}
	if r := a.At(t, "GET", "simple-host.test", "/v1/admin/users", nil, bad); r.Status != http.StatusTooManyRequests {
		t.Fatalf("fourth wrong key: %d %s", r.Status, r.Body)
	}
}
