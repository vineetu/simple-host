package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A renamed site keeps its old links: every old address 302s to the new one
// with path and query, chains and renaming back work, a new site of the old
// name wins, and delete / purge stop the redirect. Needs DB_DSN.
func TestRenameKeepsOldLinks(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "rsvp")
	_, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	person := oh + "." + pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	host := func(site string) string { return site + "." + person }
	siteID := a.siteID(t, olive, "rsvp")

	rename := func(from, to string) {
		t.Helper()
		r := a.at(t, "PATCH", apex, "/v1/sites/"+from, map[string]string{"name": to}, okey)
		if r.status != 200 || r.json(t)["old_url_status"] != "redirects" {
			t.Fatalf("rename %s -> %s: %d %s", from, to, r.status, r.body)
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
			{"legacy route", pcContentHost, "/internal/site-redirect/" + oh + "/" + old + "/a/b?x=1", nil},
			{"content-host miss", pcContentHost, "/internal/notfound", map[string]string{"X-Original-URI": "/" + oh + "/" + old + "/a/b?x=1"}},
		} {
			r := a.at(t, "GET", tc.host, tc.path, nil, tc.hdr)
			if r.status != http.StatusFound || r.header.Get("Location") != want {
				t.Errorf("%s of %s: %d %q, want %q", tc.name, old, r.status, r.header.Get("Location"), want)
			}
		}
	}

	rename("rsvp", "wedding")
	wantMoved("rsvp", "wedding")
	if r := a.at(t, "GET", host("wedding"), "/", nil, nil); r.status != 200 || string(r.body) != "<h1>rsvp</h1>" {
		t.Fatalf("new address: %d %s", r.status, r.body)
	}
	// The API on the old host is not redirected.
	if r := a.at(t, "GET", host("rsvp"), "/v1/sites/rsvp/state", nil, nil); r.status == http.StatusFound {
		t.Fatalf("old host API redirected")
	}
	// A plain miss on the content host is still the branded 404.
	if r := a.at(t, "GET", pcContentHost, "/internal/notfound", nil, map[string]string{"X-Original-URI": "/" + oh + "/nothing/"}); r.status != 404 {
		t.Fatalf("content-host miss: %d", r.status)
	}

	// A chain: both old names follow the site.
	rename("wedding", "party")
	wantMoved("rsvp", "party")
	wantMoved("wedding", "party")

	// Renaming back: the name is the site's own again.
	rename("party", "rsvp")
	if r := a.at(t, "GET", host("rsvp"), "/", nil, nil); r.status != 200 || string(r.body) != "<h1>rsvp</h1>" {
		t.Fatalf("renamed back: %d %s", r.status, r.body)
	}
	wantMoved("party", "rsvp")
	wantMoved("wedding", "rsvp")

	// A new site of an old name wins, for good.
	a.deploy(t, olive, "wedding")
	if r := a.at(t, "GET", host("wedding"), "/", nil, nil); r.status != 200 || string(r.body) != "<h1>wedding</h1>" {
		t.Fatalf("new site of the old name: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/sites/wedding", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete new wedding: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host("wedding"), "/", nil, nil); r.status != 404 {
		t.Fatalf("old name came back after the new site went: %d %q", r.status, r.header.Get("Location"))
	}

	// Deleting the renamed site stops its redirects; restoring brings them back.
	if r := a.at(t, "DELETE", apex, "/v1/sites/rsvp", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", host("party"), "/", nil, nil); r.status != 404 {
		t.Fatalf("old name of a deleted site: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "POST", apex, "/v1/sites/rsvp/restore", nil, okey); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	wantMoved("party", "rsvp")

	// Purge removes the old names with the site.
	if r := a.at(t, "DELETE", apex, "/v1/sites/rsvp", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete again: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.sites.purgeDeletedSites(context.Background())
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM site_name_aliases WHERE site_id = $1`, siteID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old names after purge: %d %v", n, err)
	}
}

// The owner's offline switch: every address shows the offline page (503),
// visitor saves are refused, the owner's key still deploys, reads and writes,
// the operator's take-down wins, and the switch survives rename, delete and
// restore. Needs DB_DSN.
func TestSiteOffline(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, oscar, vic := a.newPerson(t, "olive"), a.newPerson(t, "oscar"), a.newPerson(t, "vic")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	oliveID, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	person := oh + "." + pcSiteDomain
	shop := "shop." + person
	okey := map[string]string{"X-API-Key": olive.key}
	shopID := a.siteID(t, olive, "shop")
	cookie := a.session(t, vic, shopID, shop)
	save := func() resp {
		return a.at(t, "POST", shop, "/v1/sites/shop/collections/rsvps", map[string]string{"name": "Ann"}, browser(shop, cookie))
	}
	if r := save(); r.status != http.StatusCreated {
		t.Fatalf("visitor save while online: %d %s", r.status, r.body)
	}
	isOffline := func(host, path string) {
		t.Helper()
		r := a.at(t, "GET", host, path, nil, nil)
		if r.status != http.StatusServiceUnavailable || !strings.Contains(string(r.body), "This site is offline") || r.header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s%s: %d %s", host, path, r.status, r.body)
		}
	}

	// Only the owner, and one change per request.
	if r := a.at(t, "PATCH", apex, "/v1/sites/shop", map[string]bool{"offline": true}, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
		t.Fatalf("someone else's site: %d %s", r.status, r.body)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"name": "x", "offline": true}, map[string]any{"offline": "yes"}} {
		if r := a.at(t, "PATCH", apex, "/v1/sites/shop", body, okey); r.status != 400 {
			t.Fatalf("PATCH %v: %d %s", body, r.status, r.body)
		}
	}
	r := a.at(t, "PATCH", apex, "/v1/sites/shop", map[string]bool{"offline": true}, okey)
	if r.status != 200 || r.json(t)["offline"] != true {
		t.Fatalf("offline: %d %s", r.status, r.body)
	}
	if !a.sites.disk.IsOffline(oliveID, "shop") {
		t.Fatal("no offline marker")
	}
	isOffline(shop, "/")
	isOffline(shop, "/sub/")
	isOffline(shop, "/missing.png")
	isOffline(apex, "/internal/offline")
	if r := a.at(t, "GET", "blog."+person, "/", nil, nil); r.status != 200 {
		t.Fatalf("other site: %d", r.status)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.body), `"offline":true`) {
		t.Fatalf("site list: %s", r.body)
	}

	// Visitors cannot save; the owner's key still writes, deploys and reads.
	if r := save(); r.status != http.StatusForbidden || r.json(t)["code"] != "site_offline" {
		t.Fatalf("visitor save while offline: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", shop, "/v1/sites/shop/state", map[string]any{"n": 1}, map[string]string{"X-API-Key": olive.key, "Origin": "https://" + shop}); r.status != 200 {
		t.Fatalf("owner state write: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, okey); r.status != 200 {
		t.Fatalf("owner deploy: %d %s", r.status, r.body)
	}
	isOffline(shop, "/")
	if r := a.at(t, "GET", apex, "/v1/sites/shop/versions/2/files/index.html", nil, okey); r.status != 200 || string(r.body) != "v2" {
		t.Fatalf("owner read: %d %s", r.status, r.body)
	}

	// A claimed name serves the offline page too.
	claimed := "olive-off-" + oh + "." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	isOffline(claimed, "/")
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop/domain?domain="+claimed, nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("unclaim: %d %s", r.status, r.body)
	}

	// The operator's take-down wins while both hold.
	admin := map[string]string{"X-API-Key": a.admin}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "test"}, admin); r.status != 200 {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, "/", nil, nil); r.status != http.StatusGone {
		t.Fatalf("take-down does not win: %d", r.status)
	}
	if r := a.at(t, "PATCH", apex, "/v1/sites/shop", map[string]bool{"offline": false}, okey); r.status != http.StatusForbidden {
		t.Fatalf("switch while taken down: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/restore", nil, admin); r.status != 200 {
		t.Fatalf("restore take-down: %d %s", r.status, r.body)
	}
	isOffline(shop, "/")

	// The switch follows a rename, a delete and a restore.
	if r := a.at(t, "PATCH", apex, "/v1/sites/shop", map[string]string{"name": "store"}, okey); r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	isOffline("store."+person, "/")
	if r := a.at(t, "DELETE", apex, "/v1/sites/store", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/store/restore", nil, okey); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	isOffline("store."+person, "/")

	// A marker lost on disk comes back at boot.
	if err := a.sites.disk.SetOffline(oliveID, "store", false); err != nil {
		t.Fatal(err)
	}
	a.sites.SyncSuspendMarkers(context.Background())
	isOffline("store."+person, "/")

	// Back online.
	if r := a.at(t, "PATCH", apex, "/v1/sites/store", map[string]bool{"offline": false}, okey); r.status != 200 || r.json(t)["offline"] == true {
		t.Fatalf("online: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "store."+person, "/", nil, nil); r.status != 200 || string(r.body) != "v2" {
		t.Fatalf("back online: %d %s", r.status, r.body)
	}
	if a.sites.disk.IsOffline(oliveID, "store") {
		t.Fatal("marker left behind")
	}
}

// Look before it goes live: publish=false stores a version without making it
// live, an owner-only preview link opens any kept version on the site's own
// host (noindex, no saves), and "make live" is the rollback. Needs DB_DSN.
func TestPreviewBeforeLive(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, oscar, vic := a.newPerson(t, "olive"), a.newPerson(t, "oscar"), a.newPerson(t, "vic")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "blog")
	_, oh := a.userID(t, olive)
	_, sh := a.userID(t, oscar)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	shop := "shop." + oh + "." + pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	shopID := a.siteID(t, olive, "shop")
	v2 := map[string]any{"files": map[string]string{"index.html": "v2", "sub/index.html": "sub2"}}

	// Only updates can be stored without going live.
	if r := a.at(t, "POST", apex, "/v1/sites/fresh/files?publish=false", v2, okey); r.status != 400 {
		t.Fatalf("create unpublished: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", apex, "/v1/sites/shop/files?publish=maybe", v2, okey); r.status != 400 {
		t.Fatalf("bad publish: %d %s", r.status, r.body)
	}
	r := a.at(t, "PUT", apex, "/v1/sites/shop/files?publish=false", v2, okey)
	body := r.json(t)
	link, _ := body["preview_url"].(string)
	if r.status != 200 || body["unpublished_version"] != float64(2) || body["active_version"] != float64(1) || !strings.HasPrefix(link, "https://"+shop+"/__preview/2/") {
		t.Fatalf("unpublished deploy: %d %s", r.status, r.body)
	}
	path := strings.TrimPrefix(link, "https://"+shop)
	if r := a.at(t, "GET", shop, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("live changed: %d %s", r.status, r.body)
	}
	vs := a.at(t, "GET", apex, "/v1/sites/shop/versions", nil, okey)
	if !strings.Contains(string(vs.body), `"status":"ready","version_number":2`) {
		t.Fatalf("versions: %s", vs.body)
	}

	// The preview: that version, not cached, not indexed.
	r = a.at(t, "GET", shop, path, nil, nil)
	if r.status != 200 || string(r.body) != "v2" || r.header.Get("Cache-Control") != "no-store" || !strings.Contains(r.header.Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("preview: %d %s %v", r.status, r.body, r.header)
	}
	if r := a.at(t, "GET", shop, path+"sub", nil, nil); r.status != http.StatusMovedPermanently || r.header.Get("Location") != path+"sub/" {
		t.Fatalf("preview dir: %d %q", r.status, r.header.Get("Location"))
	}
	if r := a.at(t, "GET", shop, path+"sub/", nil, nil); r.status != 200 || string(r.body) != "sub2" {
		t.Fatalf("preview sub: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, strings.TrimSuffix(path, "/"), nil, nil); r.status != http.StatusFound || r.header.Get("Location") != path {
		t.Fatalf("preview no slash: %d %q", r.status, r.header.Get("Location"))
	}
	// Wrong version, tampered, expired, or another site's link: nothing.
	expired := "/__preview/2/" + a.sites.signPreviewToken(shopID, 2, time.Now().Add(-time.Minute)) + "/"
	blogID := a.siteID(t, oscar, "blog")
	for _, p := range []string{
		strings.Replace(path, "/__preview/2/", "/__preview/1/", 1),
		path[:len(path)-3] + "x/",
		expired,
		"/__preview/1/" + a.sites.signPreviewToken(blogID, 1, time.Now().Add(time.Minute)) + "/",
		"/__preview/2/nonsense/",
	} {
		if r := a.at(t, "GET", shop, p, nil, nil); r.status != 404 {
			t.Errorf("GET %s: %d %s", p, r.status, r.body)
		}
	}

	// Saves from a preview page are refused; from the live page they work.
	cookie := a.session(t, vic, shopID, shop)
	item := map[string]string{"name": "Ann"}
	if r := a.at(t, "POST", shop, "/v1/sites/shop/collections/rsvps", item, browser(shop, cookie, "Referer", "https://"+shop+path)); r.status != http.StatusForbidden || r.json(t)["code"] != "preview_read_only" {
		t.Fatalf("save from preview: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", shop, "/v1/sites/shop/collections/rsvps", item, browser(shop, cookie, "Referer", "https://"+shop+"/")); r.status != http.StatusCreated {
		t.Fatalf("save from live page: %d %s", r.status, r.body)
	}

	// Preview links for any kept version, owner only.
	r = a.at(t, "POST", apex, "/v1/sites/shop/versions/1/preview-link", nil, okey)
	if r.status != 200 || r.json(t)["live"] != true || !strings.HasPrefix(r.json(t)["url"].(string), "https://"+shop+"/__preview/1/") {
		t.Fatalf("preview link v1: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, strings.TrimPrefix(r.json(t)["url"].(string), "https://"+shop), nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("preview v1: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/versions/9/preview-link", nil, okey); r.status != 404 {
		t.Fatalf("missing version: %d", r.status)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/versions/1/preview-link", nil, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
		t.Fatalf("someone else's site: %d", r.status)
	}

	// A person without a certificate yet previews on the person path.
	r = a.at(t, "POST", apex, "/v1/sites/blog/versions/1/preview-link", nil, map[string]string{"X-API-Key": oscar.key})
	oscarPerson := sh + "." + pcSiteDomain
	plink, _ := r.json(t)["url"].(string)
	if r.status != 200 || !strings.HasPrefix(plink, "https://"+oscarPerson+"/blog/__preview/1/") {
		t.Fatalf("person-path link: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", oscarPerson, strings.TrimPrefix(plink, "https://"+oscarPerson), nil, nil); r.status != 200 || string(r.body) != "<h1>blog</h1>" {
		t.Fatalf("person-path preview: %d %s", r.status, r.body)
	}

	// A site on a claimed name still previews on its site host.
	claimed := "olive-pv-" + oh + "." + pcSiteDomain
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, path, nil, nil); r.status != 200 || string(r.body) != "v2" {
		t.Fatalf("preview with a domain: %d %s", r.status, r.body)
	}

	// Make live: the existing rollback.
	if r := a.at(t, "PUT", apex, "/v1/sites/shop/active-version", map[string]int{"version_number": 2}, okey); r.status != 200 {
		t.Fatalf("make live: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", claimed, "/", nil, nil); r.status != 200 || string(r.body) != "v2" {
		t.Fatalf("live after make live: %d %s", r.status, r.body)
	}
	vs = a.at(t, "GET", apex, "/v1/sites/shop/versions", nil, okey)
	if !strings.Contains(string(vs.body), `"status":"active","version_number":2`) {
		t.Fatalf("versions after make live: %s", vs.body)
	}

	// A taken-down site previews nothing.
	if r := a.at(t, "POST", apex, "/v1/admin/sites/"+shopID+"/suspend", map[string]string{"reason": "test"}, map[string]string{"X-API-Key": a.admin}); r.status != 200 {
		t.Fatalf("suspend: %d", r.status)
	}
	if r := a.at(t, "GET", shop, path, nil, nil); r.status != http.StatusGone {
		t.Fatalf("preview of a taken-down site: %d", r.status)
	}
}
