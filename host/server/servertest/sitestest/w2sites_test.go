package sitestest

import (
	"context"
	"encoding/json"
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A renamed site keeps its old links: every old address 302s to the new one
// with path and query, chains and renaming back work, a new site of the old
// name wins, and delete / purge stop the redirect. Needs DB_DSN.
func TestRenameKeepsOldLinks(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "rsvp")
	_, oh := a.UserID(t, olive)
	servertest.MarkReady(t, dir, oh)
	apex := servertest.PcSiteDomain
	person := oh + "." + servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	host := func(site string) string { return site + "." + person }
	siteID := a.SiteID(t, olive, "rsvp")

	rename := func(from, to string) {
		t.Helper()
		r := a.At(t, "PATCH", apex, "/v1/sites/"+from, map[string]string{"name": to}, okey)
		if r.Status != 200 || r.JSON(t)["old_url_status"] != "redirects" {
			t.Fatalf("rename %s -> %s: %d %s", from, to, r.Status, r.Body)
		}
	}
	// wantMoved: every old address of `old` sends /a/b?x=1 to `now`.
	wantMoved := func(old, now string) {
		t.Helper()
		want := "https://" + host(now) + "/a/b?x=1"
		for _, tc := range []struct {
			name, host, path string
			hdr              map[string]string
		}{
			{"site host", host(old), "/a/b?x=1", nil},
			{"person path", person, "/" + old + "/a/b?x=1", nil},
			{"legacy route", servertest.PcContentHost, "/internal/site-redirect/" + oh + "/" + old + "/a/b?x=1", nil},
			{"content-host miss", servertest.PcContentHost, "/internal/notfound", map[string]string{"X-Original-URI": "/" + oh + "/" + old + "/a/b?x=1"}},
		} {
			r := a.At(t, "GET", tc.host, tc.path, nil, tc.hdr)
			if r.Status != http.StatusFound || r.Header.Get("Location") != want {
				t.Errorf("%s of %s: %d %q, want %q", tc.name, old, r.Status, r.Header.Get("Location"), want)
			}
		}
	}

	rename("rsvp", "wedding")
	wantMoved("rsvp", "wedding")
	if r := a.At(t, "GET", host("wedding"), "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>rsvp</h1>" {
		t.Fatalf("new address: %d %s", r.Status, r.Body)
	}
	// The API on the old host is not redirected.
	if r := a.At(t, "GET", host("rsvp"), "/v1/sites/rsvp/me", nil, nil); r.Status == http.StatusFound {
		t.Fatalf("old host API redirected")
	}
	// A plain miss on the content host is still the branded 404.
	if r := a.At(t, "GET", servertest.PcContentHost, "/internal/notfound", nil, map[string]string{"X-Original-URI": "/" + oh + "/nothing/"}); r.Status != 404 {
		t.Fatalf("content-host miss: %d", r.Status)
	}

	// A chain: both old names follow the site.
	rename("wedding", "party")
	wantMoved("rsvp", "party")
	wantMoved("wedding", "party")

	// Renaming back: the name is the site's own again.
	rename("party", "rsvp")
	if r := a.At(t, "GET", host("rsvp"), "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>rsvp</h1>" {
		t.Fatalf("renamed back: %d %s", r.Status, r.Body)
	}
	wantMoved("party", "rsvp")
	wantMoved("wedding", "rsvp")

	// A new site of an old name wins, for good.
	a.Deploy(t, olive, "wedding")
	if r := a.At(t, "GET", host("wedding"), "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>wedding</h1>" {
		t.Fatalf("new site of the old name: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/sites/wedding", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete new wedding: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host("wedding"), "/", nil, nil); r.Status != 404 {
		t.Fatalf("old name came back after the new site went: %d %q", r.Status, r.Header.Get("Location"))
	}

	// Deleting the renamed site stops its redirects; restoring brings them back.
	if r := a.At(t, "DELETE", apex, "/v1/sites/rsvp", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", host("party"), "/", nil, nil); r.Status != 404 {
		t.Fatalf("old name of a deleted site: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "POST", apex, "/v1/sites/rsvp/restore", nil, okey); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	wantMoved("party", "rsvp")

	// Purge removes the old names with the site.
	if r := a.At(t, "DELETE", apex, "/v1/sites/rsvp", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete again: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.Sites.PurgeDeletedSites(context.Background())
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM site_name_aliases WHERE site_id = $1`, siteID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old names after purge: %d %v", n, err)
	}
}

// The owner's offline switch: every address shows the offline page (503),
// visitor saves are refused, the owner's key still deploys, reads and writes,
// the operator's take-down wins, and the switch survives rename, delete and
// restore. Needs DB_DSN.
func TestSiteOffline(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, oscar, vic := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar"), a.NewPerson(t, "vic")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	oliveID, oh := a.UserID(t, olive)
	servertest.MarkReady(t, dir, oh)
	apex := servertest.PcSiteDomain
	person := oh + "." + servertest.PcSiteDomain
	shop := "shop." + person
	okey := map[string]string{"X-API-Key": olive.Key}
	shopID := a.SiteID(t, olive, "shop")
	cookie := a.Session(t, vic, shopID, shop)
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/resources/rsvps", servertest.StorageRules("kv", servertest.RulesBoard), okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	data := "/v1/sites/shop/storage/kv/rsvps/keys/ann"
	save := func() servertest.Resp {
		return a.At(t, "PUT", shop, data, map[string]any{"value": "Ann"}, servertest.Browser(shop, cookie))
	}
	if r := save(); r.Status != http.StatusOK {
		t.Fatalf("visitor save while online: %d %s", r.Status, r.Body)
	}
	isOffline := func(host, path string) {
		t.Helper()
		r := a.At(t, "GET", host, path, nil, nil)
		if r.Status != http.StatusServiceUnavailable || !strings.Contains(string(r.Body), "This site is offline") || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s%s: %d %s", host, path, r.Status, r.Body)
		}
	}

	// Only the owner, and one change per request.
	if r := a.At(t, "PATCH", apex, "/v1/sites/shop", map[string]bool{"offline": true}, map[string]string{"X-API-Key": oscar.Key}); r.Status != 404 {
		t.Fatalf("someone else's site: %d %s", r.Status, r.Body)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"name": "x", "offline": true}, map[string]any{"offline": "yes"}} {
		if r := a.At(t, "PATCH", apex, "/v1/sites/shop", body, okey); r.Status != 400 {
			t.Fatalf("PATCH %v: %d %s", body, r.Status, r.Body)
		}
	}
	r := a.At(t, "PATCH", apex, "/v1/sites/shop", map[string]bool{"offline": true}, okey)
	if r.Status != 200 || r.JSON(t)["offline"] != true {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	if !a.Sites.Disk.IsOffline(oliveID, "shop") {
		t.Fatal("no offline marker")
	}
	isOffline(shop, "/")
	isOffline(shop, "/sub/")
	isOffline(shop, "/missing.png")
	isOffline(apex, "/internal/offline")
	if r := a.At(t, "GET", "blog."+person, "/", nil, nil); r.Status != 200 {
		t.Fatalf("other site: %d", r.Status)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.Body), `"offline":true`) {
		t.Fatalf("site list: %s", r.Body)
	}

	// Visitors cannot save; the owner's key still writes, deploys and reads.
	if r := save(); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_offline" {
		t.Fatalf("visitor save while offline: %d %s", r.Status, r.Body)
	}
	// Offline means offline: visitors' reads of saved data and visitor
	// sign-in stop too.
	if r := a.At(t, "GET", shop, data, nil, servertest.Browser(shop, cookie)); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_offline" {
		t.Fatalf("visitor read while offline: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, data, nil, okey); r.Status != 200 {
		t.Fatalf("owner read while offline: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", shop, "/v1/sites/shop/visitor/auth", map[string]string{"email": "ann@example.com"}, servertest.Browser(shop, "")); r.Status != http.StatusForbidden || r.JSON(t)["code"] != "site_offline" {
		t.Fatalf("visitor sign-in while offline: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", shop, data, map[string]any{"value": "Bo"}, map[string]string{"X-API-Key": olive.Key, "Origin": "https://" + shop}); r.Status != 200 {
		t.Fatalf("owner data write: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, okey); r.Status != 200 {
		t.Fatalf("owner deploy: %d %s", r.Status, r.Body)
	}
	isOffline(shop, "/")
	if r := a.At(t, "GET", apex, "/v1/sites/shop/versions/2/files/index.html", nil, okey); r.Status != 200 || string(r.Body) != "v2" {
		t.Fatalf("owner read: %d %s", r.Status, r.Body)
	}

	// A claimed name serves the offline page too.
	claimed := "olive-off-" + oh + "." + servertest.PcSiteDomain
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	isOffline(claimed, "/")
	if r := a.At(t, "DELETE", apex, "/v1/sites/shop/domain?domain="+claimed, nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("unclaim: %d %s", r.Status, r.Body)
	}

	// The operator's take-down wins while both hold.
	admin := map[string]string{"X-API-Key": a.Admin}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "test"}, admin); r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("take-down does not win: %d", r.Status)
	}
	if r := a.At(t, "PATCH", apex, "/v1/sites/shop", map[string]bool{"offline": false}, okey); r.Status != http.StatusForbidden {
		t.Fatalf("switch while taken down: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.Status != 200 {
		t.Fatalf("restore take-down: %d %s", r.Status, r.Body)
	}
	isOffline(shop, "/")

	// The switch follows a rename, a delete and a restore.
	if r := a.At(t, "PATCH", apex, "/v1/sites/shop", map[string]string{"name": "store"}, okey); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	isOffline("store."+person, "/")
	if r := a.At(t, "DELETE", apex, "/v1/sites/store", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/store/restore", nil, okey); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	isOffline("store."+person, "/")

	// A marker lost on disk comes back at boot.
	if err := a.Sites.Disk.SetOffline(oliveID, "store", false); err != nil {
		t.Fatal(err)
	}
	a.Access.SyncSuspendMarkers(context.Background())
	isOffline("store."+person, "/")

	// Back online.
	if r := a.At(t, "PATCH", apex, "/v1/sites/store", map[string]bool{"offline": false}, okey); r.Status != 200 || r.JSON(t)["offline"] == true {
		t.Fatalf("online: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "store."+person, "/", nil, nil); r.Status != 200 || string(r.Body) != "v2" {
		t.Fatalf("back online: %d %s", r.Status, r.Body)
	}
	if a.Sites.Disk.IsOffline(oliveID, "store") {
		t.Fatal("marker left behind")
	}
}

// Look before it goes live: publish=false stores a version without making it
// live, an owner-only preview link opens any kept version on the site's own
// host (noindex, no saves), and "make live" is the rollback. Needs DB_DSN.
func TestPreviewBeforeLive(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "blog")
	_, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	servertest.MarkReady(t, dir, oh)
	apex := servertest.PcSiteDomain
	shop := "shop." + oh + "." + servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	shopID := a.SiteID(t, olive, "shop")
	v2 := map[string]any{"files": map[string]string{"index.html": "v2", "sub/index.html": "sub2"}}

	// Only updates can be stored without going live.
	if r := a.At(t, "POST", apex, "/v1/sites/fresh/files?publish=false", v2, okey); r.Status != 400 {
		t.Fatalf("create unpublished: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/files?publish=maybe", v2, okey); r.Status != 400 {
		t.Fatalf("bad publish: %d %s", r.Status, r.Body)
	}
	r := a.At(t, "PUT", apex, "/v1/sites/shop/files?publish=false", v2, okey)
	body := r.JSON(t)
	link, _ := body["preview_url"].(string)
	if r.Status != 200 || body["unpublished_version"] != float64(2) || body["active_version"] != float64(1) || !strings.HasPrefix(link, "https://"+shop+"/__preview/2/") {
		t.Fatalf("unpublished deploy: %d %s", r.Status, r.Body)
	}
	path := strings.TrimPrefix(link, "https://"+shop)
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("live changed: %d %s", r.Status, r.Body)
	}
	vs := a.At(t, "GET", apex, "/v1/sites/shop/versions", nil, okey)
	var versions []map[string]any
	if err := json.Unmarshal(vs.Body, &versions); err != nil || len(versions) == 0 || versions[0]["version_number"] != float64(2) || versions[0]["status"] != "ready" {
		t.Fatalf("versions: %s", vs.Body)
	}

	// The preview: that version, not cached, not indexed.
	r = a.At(t, "GET", shop, path, nil, nil)
	if r.Status != 200 || string(r.Body) != "v2" || r.Header.Get("Cache-Control") != "no-store" || !strings.Contains(r.Header.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("preview: %d %s %v", r.Status, r.Body, r.Header)
	}
	checkPageRedirect(t, a, shop, path+"index.html/", path+"index.html", "no-store")
	if r := a.At(t, "GET", shop, path+"sub", nil, nil); r.Status != http.StatusMovedPermanently || r.Header.Get("Location") != path+"sub/" || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("preview dir: %d %q", r.Status, r.Header.Get("Location"))
	}
	if r := a.At(t, "GET", shop, path+"sub/", nil, nil); r.Status != 200 || string(r.Body) != "sub2" {
		t.Fatalf("preview sub: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, strings.TrimSuffix(path, "/"), nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != path {
		t.Fatalf("preview no slash: %d %q", r.Status, r.Header.Get("Location"))
	}
	// Wrong version, tampered, expired, or another site's link: nothing.
	expired := "/__preview/2/" + a.Sites.SignPreviewToken(shopID, 2, time.Now().Add(-time.Minute)) + "/"
	blogID := a.SiteID(t, oscar, "blog")
	for _, p := range []string{
		strings.Replace(path, "/__preview/2/", "/__preview/1/", 1),
		path[:len(path)-3] + "x/",
		expired,
		"/__preview/1/" + a.Sites.SignPreviewToken(blogID, 1, time.Now().Add(time.Minute)) + "/",
		"/__preview/2/nonsense/",
	} {
		if r := a.At(t, "GET", shop, p, nil, nil); r.Status != 404 {
			t.Errorf("GET %s: %d %s", p, r.Status, r.Body)
		}
	}

	// Preview links for any kept version, owner only.
	r = a.At(t, "POST", apex, "/v1/sites/shop/versions/1/preview-link", nil, okey)
	if r.Status != 200 || r.JSON(t)["live"] != true || !strings.HasPrefix(r.JSON(t)["url"].(string), "https://"+shop+"/__preview/1/") {
		t.Fatalf("preview link v1: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, strings.TrimPrefix(r.JSON(t)["url"].(string), "https://"+shop), nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("preview v1: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/versions/9/preview-link", nil, okey); r.Status != 404 {
		t.Fatalf("missing version: %d", r.Status)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/versions/1/preview-link", nil, map[string]string{"X-API-Key": oscar.Key}); r.Status != 404 {
		t.Fatalf("someone else's site: %d", r.Status)
	}

	// A person without a certificate yet previews on the person path.
	r = a.At(t, "POST", apex, "/v1/sites/blog/versions/1/preview-link", nil, map[string]string{"X-API-Key": oscar.Key})
	oscarPerson := sh + "." + servertest.PcSiteDomain
	plink, _ := r.JSON(t)["url"].(string)
	if r.Status != 200 || !strings.HasPrefix(plink, "https://"+oscarPerson+"/blog/__preview/1/") {
		t.Fatalf("person-path link: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", oscarPerson, strings.TrimPrefix(plink, "https://"+oscarPerson), nil, nil); r.Status != 200 || string(r.Body) != "<h1>blog</h1>" {
		t.Fatalf("person-path preview: %d %s", r.Status, r.Body)
	}

	// A site on a claimed name still previews on its site host.
	claimed := "olive-pv-" + oh + "." + servertest.PcSiteDomain
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, path, nil, nil); r.Status != 200 || string(r.Body) != "v2" {
		t.Fatalf("preview with a domain: %d %s", r.Status, r.Body)
	}

	// Make live: the existing rollback.
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/active-version", map[string]int{"version_number": 2}, okey); r.Status != 200 {
		t.Fatalf("make live: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status != 200 || string(r.Body) != "v2" {
		t.Fatalf("live after make live: %d %s", r.Status, r.Body)
	}
	vs = a.At(t, "GET", apex, "/v1/sites/shop/versions", nil, okey)
	versions = nil
	if err := json.Unmarshal(vs.Body, &versions); err != nil || len(versions) == 0 || versions[0]["version_number"] != float64(2) || versions[0]["status"] != "active" {
		t.Fatalf("versions after make live: %s", vs.Body)
	}

	// A taken-down site previews nothing.
	if r := a.At(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "test"}, map[string]string{"X-API-Key": a.Admin}); r.Status != 200 {
		t.Fatalf("suspend: %d", r.Status)
	}
	if r := a.At(t, "GET", shop, path, nil, nil); r.Status != http.StatusGone {
		t.Fatalf("preview of a taken-down site: %d", r.Status)
	}
}
