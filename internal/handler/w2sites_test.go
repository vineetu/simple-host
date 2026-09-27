package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
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
