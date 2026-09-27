package handler

import (
	"context"
	"net/http"
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
