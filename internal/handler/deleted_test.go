package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// Recently deleted: a deleted site goes offline at once, keeps its files,
// versions, saved data and claimed name for 7 days with its name held, comes
// back whole on restore, and is purged after the window.
func TestRecentlyDeletedAndRestore(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	a.deploy(t, oscar, "home")
	uid, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	shop := "shop." + oh + "." + pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	ctx := context.Background()
	siteID := a.siteID(t, olive, "shop")
	claimed := "olv" + strings.ToLower(strings.TrimPrefix(oh, "olive-")) + "." + pcSiteDomain

	// Saved data and a claimed free address that must survive the round trip.
	if _, err := db.AppendCollectionItemByID(ctx, a.database, siteID, "rsvps", []byte(`{"name":"Ann"}`)); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", claimed, "/", nil, nil); r.status != 200 {
		t.Fatalf("claimed host before delete: %d", r.status)
	}
	// With a claimed address the site host redirects there; drop it again so
	// the site host itself serves, and claim it back after the restore.
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop/domain", nil, okey); r.status/100 != 2 {
		t.Fatalf("unclaim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.status != 200 {
		t.Fatalf("reclaim: %d %s", r.status, r.body)
	}

	// ---- delete: offline at once, listed as recently deleted -----------------
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", shop, "/", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("site host after delete: %d", r.status)
	}
	if r := a.at(t, "GET", claimed, "/", nil, nil); r.status == 200 {
		t.Fatalf("claimed host still serves after delete")
	}
	if r := a.at(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/collections/rsvps", nil, nil); r.status != http.StatusNotFound {
		t.Fatalf("collection after delete: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites", nil, okey); strings.Contains(string(r.body), `"name":"shop"`) {
		t.Fatalf("deleted site still listed: %s", r.body)
	}
	if _, err := os.Stat(a.sites.disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("site dir still served: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.TrashDir(uid, siteID), "current", "index.html")); err != nil {
		t.Fatalf("files not kept: %v", err)
	}
	r := a.at(t, "GET", apex, "/v1/me/deleted-sites", nil, okey)
	sites, _ := r.json(t)["sites"].([]any)
	if r.status != 200 || len(sites) != 1 || sites[0].(map[string]any)["name"] != "shop" {
		t.Fatalf("deleted-sites: %d %s", r.status, r.body)
	}
	purgeAt, _ := time.Parse(time.RFC3339, sites[0].(map[string]any)["purge_at"].(string))
	if d := time.Until(purgeAt); d < 6*24*time.Hour || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("purge_at %v", purgeAt)
	}
	if r := a.at(t, "GET", apex, "/v1/me/deleted-sites", nil, map[string]string{"X-API-Key": oscar.key}); strings.Contains(string(r.body), "shop") {
		t.Fatalf("someone else's deleted site listed: %s", r.body)
	}

	// ---- the name is held -----------------------------------------------------
	r = a.at(t, "POST", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "new"}}, okey)
	if r.status != http.StatusConflict || r.json(t)["recently_deleted"] != true || !strings.Contains(r.json(t)["error"].(string), "Recently deleted") {
		t.Fatalf("create over a deleted name: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, "/v1/sites/blog", map[string]string{"name": "shop"}, okey); r.status != http.StatusConflict || r.json(t)["recently_deleted"] != true {
		t.Fatalf("rename onto a deleted name: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/home/domain", map[string]string{"domain": claimed}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("claimed name of a deleted site taken by someone else: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/restore", nil, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusNotFound {
		t.Fatalf("restore someone else's site: %d %s", r.status, r.body)
	}

	// ---- restore: back whole ---------------------------------------------------
	r = a.at(t, "POST", apex, "/v1/sites/shop/restore", nil, okey)
	if r.status != 200 || r.json(t)["name"] != "shop" {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", claimed, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("claimed host after restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/u/"+oh+"/sites/shop/collections/rsvps", nil, okey); r.status != 200 || !strings.Contains(string(r.body), "Ann") {
		t.Fatalf("data after restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/sites/shop/versions", nil, okey); r.status != 200 || !strings.Contains(string(r.body), `"version_number":1`) {
		t.Fatalf("versions after restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/shop/restore", nil, okey); r.status != http.StatusConflict {
		t.Fatalf("restore a live site: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/nothing/restore", nil, okey); r.status != http.StatusNotFound {
		t.Fatalf("restore unknown: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", apex, "/v1/me/deleted-sites", nil, okey); strings.Contains(string(r.body), "shop") {
		t.Fatalf("restored site still in deleted list: %s", r.body)
	}

	// ---- purge after the window --------------------------------------------------
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete again: %d", r.status)
	}
	a.sites.purgeDeletedSites(ctx) // inside the window: kept
	if _, err := db.GetDeletedSiteByUser(ctx, a.database, uid, "shop"); err != nil {
		t.Fatalf("purged inside the window: %v", err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.sites.purgeDeletedSites(ctx)
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, siteID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("row not purged: %d %v", n, err)
	}
	if _, err := os.Stat(a.sites.disk.TrashDir(uid, siteID)); !os.IsNotExist(err) {
		t.Fatalf("files not purged: %v", err)
	}
	if r := a.at(t, "POST", apex, "/v1/sites/home/domain", map[string]string{"domain": claimed}, map[string]string{"X-API-Key": oscar.key}); r.status != 200 {
		t.Fatalf("claimed name not freed by the purge: %d %s", r.status, r.body)
	}
	a.deploy(t, olive, "shop") // the name is free again
}

// A handle can change after publishing: the old handle stays as an alias, so
// every old address redirects; the new handle's certificate is requested and
// the person-path address is handed out until it is ready.
func TestHandleChangeAfterPublishing(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "home")
	uid, oh := a.userID(t, olive)
	_, sh := a.userID(t, oscar)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	nh := oh + "-new"
	domain := "shop-" + strings.TrimPrefix(oh, "olive-") + ".example.test"
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": domain}, okey); r.status != 200 {
		t.Fatalf("bind domain: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", apex, "/v1/sites/shop/domain", nil, okey); r.status/100 != 2 {
		t.Fatalf("unbind: %d", r.status)
	}

	// Taken names are refused: someone else's handle, and a handle that is taken as an address.
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": sh}, okey); r.status != http.StatusConflict {
		t.Fatalf("someone else's handle: %d %s", r.status, r.body)
	}

	r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": nh}, okey)
	if r.status != 200 || r.json(t)["handle"] != nh {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", nh)); err != nil {
		t.Fatalf("no certificate request for the new handle: %v", err)
	}
	// Until the certificate is ready: the working person-path address.
	if r := a.at(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.body), "https://"+nh+"."+pcSiteDomain+"/shop/") {
		t.Fatalf("site_url before cert: %s", r.body)
	}
	if r := a.at(t, "GET", nh+"."+pcSiteDomain, "/shop/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("new person path: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "shop."+oh+"."+pcSiteDomain, "/a?b=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://"+nh+"."+pcSiteDomain+"/shop/a?b=1" {
		t.Fatalf("old site host before cert: %d %q", r.status, r.header.Get("Location"))
	}
	markReady(t, dir, nh)
	for _, tc := range []struct{ host, path, want string }{
		{"shop." + oh + "." + pcSiteDomain, "/a?b=1", "https://shop." + nh + "." + pcSiteDomain + "/a?b=1"},
		{oh + "." + pcSiteDomain, "/shop/a", "https://" + nh + "." + pcSiteDomain + "/shop/a"}, // then on to the site host
		{nh + "." + pcSiteDomain, "/shop/a", "https://shop." + nh + "." + pcSiteDomain + "/a"},
		{oh + "." + pcSiteDomain, "/", "https://" + nh + "." + pcSiteDomain + "/"},
		{pcContentHost, "/internal/site-redirect/" + oh + "/shop/a", "https://shop." + nh + "." + pcSiteDomain + "/a"},
		{pcContentHost, "/internal/showcase/" + oh, "/" + nh},
	} {
		r := a.at(t, "GET", tc.host, tc.path, nil, nil)
		if r.status/100 != 3 || r.header.Get("Location") != tc.want {
			t.Errorf("%s%s: %d %q want %q", tc.host, tc.path, r.status, r.header.Get("Location"), tc.want)
		}
	}
	if r := a.at(t, "GET", "shop."+nh+"."+pcSiteDomain, "/", nil, nil); r.status != 200 || string(r.body) != "<h1>shop</h1>" {
		t.Fatalf("new site host: %d %s", r.status, r.body)
	}
	// The owner app at <apex>/<old> follows the account.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://"+apex+"/"+oh, nil)
	a.sites.ownerAppOrStatic(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/"+nh {
		t.Fatalf("owner app alias: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// A custom domain stays attached, and the files follow the new handle on disk.
	if r := a.at(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": domain}, okey); r.status != 200 {
		t.Fatalf("domain after rename: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.DataDir(), "handles", nh, "shop", "current", "index.html")); err != nil {
		t.Fatalf("new handle link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.sites.disk.DataDir(), "handles", oh, "shop", "current", "index.html")); err != nil {
		t.Fatalf("old handle link kept for old content-host paths: %v", err)
	}

	// Nobody else can take the old handle.
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh}, map[string]string{"X-API-Key": oscar.key}); r.status != http.StatusConflict {
		t.Fatalf("old handle taken by someone else: %d %s", r.status, r.body)
	}
	// Once per 30 days after publishing.
	r = a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh + "-third"}, okey)
	if r.status != http.StatusTooManyRequests || r.json(t)["next_change_after"] == nil {
		t.Fatalf("second change inside the window: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE handle_aliases SET created_at = now() - interval '31 days' WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET handle_changed_at = now() - interval '31 days' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	// Going back to one's own old handle is allowed, and both old names keep redirecting.
	if r := a.at(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh}, okey); r.status != 200 || r.json(t)["handle"] != oh {
		t.Fatalf("back to the old handle: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "shop."+nh+"."+pcSiteDomain, "/", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://shop."+oh+"."+pcSiteDomain+"/" {
		t.Fatalf("second alias: %d %q", r.status, r.header.Get("Location"))
	}
}

// Before anything is published the handle changes freely and leaves no alias.
func TestHandleChangeBeforePublishingIsFree(t *testing.T) {
	a, _ := newSiteApp(t, "canonical")
	pat := a.newPerson(t, "pat")
	var uid string
	if err := a.database.QueryRow(`SELECT id FROM users WHERE username = $1`, pat.email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	ph := "pat-" + uid[:8]
	key := map[string]string{"X-API-Key": pat.key}
	for _, h := range []string{ph + "-a", ph + "-b"} {
		if r := a.at(t, "PATCH", pcSiteDomain, "/v1/me", map[string]string{"handle": h}, key); r.status != 200 || r.json(t)["handle"] != h {
			t.Fatalf("change to %s: %d %s", h, r.status, r.body)
		}
	}
	var n int
	if err := a.database.QueryRow(`SELECT count(*) FROM handle_aliases WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("aliases before publishing: %d %v", n, err)
	}
}
