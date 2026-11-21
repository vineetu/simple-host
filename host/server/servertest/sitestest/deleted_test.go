package sitestest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// Recently deleted: a deleted site goes offline at once, keeps its files,
// versions, saved data and claimed name for 7 days with its name held, comes
// back whole on restore, and is purged after the window.
func TestRecentlyDeletedAndRestore(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	a.Deploy(t, oscar, "home")
	uid, oh := a.UserID(t, olive)
	servertest.MarkReady(t, dir, oh)
	apex := servertest.PcSiteDomain
	shop := "shop." + oh + "." + servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	ctx := context.Background()
	siteID := a.SiteID(t, olive, "shop")
	claimed := "olv" + strings.ToLower(strings.TrimPrefix(oh, "olive-")) + "." + servertest.PcSiteDomain

	// Saved data and a claimed free address that must survive the round trip.
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/resources/guests", map[string]string{"kind": "kv"}, okey); r.Status/100 != 2 {
		t.Fatalf("storage resource: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PUT", apex, "/v1/sites/shop/storage/kv/guests/keys/ann", map[string]any{"value": map[string]string{"name": "Ann"}}, okey); r.Status != 200 {
		t.Fatalf("storage value: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status != 200 {
		t.Fatalf("claimed host before delete: %d", r.Status)
	}
	// With a claimed address the site host redirects there; drop it again so
	// the site host itself serves, and claim it back after the restore.
	if r := a.At(t, "DELETE", apex, "/v1/sites/shop/domain", nil, okey); r.Status/100 != 2 {
		t.Fatalf("unclaim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": claimed}, okey); r.Status != 200 {
		t.Fatalf("reclaim: %d %s", r.Status, r.Body)
	}

	// ---- delete: offline at once, listed as recently deleted -----------------
	if r := a.At(t, "DELETE", apex, "/v1/sites/shop", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", shop, "/", nil, nil); r.Status != http.StatusNotFound {
		t.Fatalf("site host after delete: %d", r.Status)
	}
	// While the site is in Recently deleted its claimed name is held and
	// answers "removed", never another site of that name.
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status == 200 || !strings.Contains(string(r.Body), "This site was removed") {
		t.Fatalf("claimed host after delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites/shop/storage/kv/guests/keys/ann", nil, okey); r.Status != http.StatusNotFound {
		t.Fatalf("saved data after delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites", nil, okey); strings.Contains(string(r.Body), `"name":"shop"`) {
		t.Fatalf("deleted site still listed: %s", r.Body)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("site dir still served: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.Sites.Disk.TrashDir(uid, siteID), "current", "index.html")); err != nil {
		t.Fatalf("files not kept: %v", err)
	}
	r := a.At(t, "GET", apex, "/v1/me/deleted-sites", nil, okey)
	sites, _ := r.JSON(t)["sites"].([]any)
	if r.Status != 200 || len(sites) != 1 || sites[0].(map[string]any)["name"] != "shop" {
		t.Fatalf("deleted-sites: %d %s", r.Status, r.Body)
	}
	purgeAt, _ := time.Parse(time.RFC3339, sites[0].(map[string]any)["purge_at"].(string))
	if d := time.Until(purgeAt); d < 6*24*time.Hour || d > 7*24*time.Hour+time.Minute {
		t.Fatalf("purge_at %v", purgeAt)
	}
	if r := a.At(t, "GET", apex, "/v1/me/deleted-sites", nil, map[string]string{"X-API-Key": oscar.Key}); strings.Contains(string(r.Body), "shop") {
		t.Fatalf("someone else's deleted site listed: %s", r.Body)
	}

	// ---- the name is held -----------------------------------------------------
	r = a.At(t, "POST", apex, "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "new"}}, okey)
	if r.Status != http.StatusConflict || r.JSON(t)["recently_deleted"] != true || !strings.Contains(r.JSON(t)["error"].(string), "Recently deleted") {
		t.Fatalf("create over a deleted name: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", apex, "/v1/sites/blog", map[string]string{"name": "shop"}, okey); r.Status != http.StatusConflict || r.JSON(t)["recently_deleted"] != true {
		t.Fatalf("rename onto a deleted name: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/home/domain", map[string]string{"domain": claimed}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
		t.Fatalf("claimed name of a deleted site taken by someone else: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/restore", nil, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusNotFound {
		t.Fatalf("restore someone else's site: %d %s", r.Status, r.Body)
	}

	// ---- restore: back whole ---------------------------------------------------
	r = a.At(t, "POST", apex, "/v1/sites/shop/restore", nil, okey)
	if r.Status != 200 || r.JSON(t)["name"] != "shop" {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("claimed host after restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites/shop/storage/kv/guests/keys/ann", nil, okey); r.Status != 200 || !strings.Contains(string(r.Body), "Ann") {
		t.Fatalf("data after restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/sites/shop/versions", nil, okey); r.Status != 200 || !strings.Contains(string(r.Body), `"version_number":1`) {
		t.Fatalf("versions after restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/shop/restore", nil, okey); r.Status != http.StatusConflict {
		t.Fatalf("restore a live site: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", apex, "/v1/sites/nothing/restore", nil, okey); r.Status != http.StatusNotFound {
		t.Fatalf("restore unknown: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", apex, "/v1/me/deleted-sites", nil, okey); strings.Contains(string(r.Body), "shop") {
		t.Fatalf("restored site still in deleted list: %s", r.Body)
	}

	// ---- purge after the window --------------------------------------------------
	if r := a.At(t, "DELETE", apex, "/v1/sites/shop", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete again: %d", r.Status)
	}
	a.Sites.PurgeDeletedSites(ctx) // inside the window: kept
	if _, err := sitesrepo.GetDeletedSiteByUser(ctx, a.Database, uid, "shop"); err != nil {
		t.Fatalf("purged inside the window: %v", err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.Sites.PurgeDeletedSites(ctx)
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, siteID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("row not purged: %d %v", n, err)
	}
	if _, err := os.Stat(a.Sites.Disk.TrashDir(uid, siteID)); !os.IsNotExist(err) {
		t.Fatalf("files not purged: %v", err)
	}
	// The purge retires the claimed name (legacy_hostnames): it keeps saying
	// the site was removed and never passes to someone else's site.
	if r := a.At(t, "POST", apex, "/v1/sites/home/domain", map[string]string{"domain": claimed}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
		t.Fatalf("retired claimed name taken by someone else after the purge: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status == 200 || !strings.Contains(string(r.Body), "This site was removed") {
		t.Fatalf("claimed host after purge: %d %s", r.Status, r.Body)
	}
	if _, err := sitesrepo.GetRetiredName(ctx, a.Database, claimed, accountsrepo.HostForms(claimed)); err != nil {
		t.Fatalf("claimed name not retired by the purge: %v", err)
	}
	a.Deploy(t, olive, "shop") // the site name is free again
}

// A handle can change after publishing: the old handle stays as an alias, so
// every old address redirects; the new handle's certificate is requested and
// the person-path address is handed out until it is ready.
func TestHandleChangeAfterPublishing(t *testing.T) {
	a, dir := servertest.NewSiteApp(t, "canonical")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "home")
	uid, oh := a.UserID(t, olive)
	_, sh := a.UserID(t, oscar)
	servertest.MarkReady(t, dir, oh)
	apex := servertest.PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	nh := oh + "-new"
	domain := "shop-" + strings.TrimPrefix(oh, "olive-") + ".example.test"
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": domain}, okey); r.Status != 200 {
		t.Fatalf("bind domain: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", apex, "/v1/sites/shop/domain", nil, okey); r.Status/100 != 2 {
		t.Fatalf("unbind: %d", r.Status)
	}

	// Taken names are refused: someone else's handle, and a handle that is taken as an address.
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": sh}, okey); r.Status != http.StatusConflict {
		t.Fatalf("someone else's handle: %d %s", r.Status, r.Body)
	}

	r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": nh}, okey)
	if r.Status != 200 || r.JSON(t)["handle"] != nh {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "requests", nh)); err != nil {
		t.Fatalf("no certificate request for the new handle: %v", err)
	}
	// Until the certificate is ready: the working person-path address.
	if r := a.At(t, "GET", apex, "/v1/sites", nil, okey); !strings.Contains(string(r.Body), "https://"+nh+"."+servertest.PcSiteDomain+"/shop/") {
		t.Fatalf("site_url before cert: %s", r.Body)
	}
	if r := a.At(t, "GET", nh+"."+servertest.PcSiteDomain, "/shop/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("new person path: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "shop."+oh+"."+servertest.PcSiteDomain, "/a?b=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://"+nh+"."+servertest.PcSiteDomain+"/shop/a?b=1" {
		t.Fatalf("old site host before cert: %d %q", r.Status, r.Header.Get("Location"))
	}
	servertest.MarkReady(t, dir, nh)
	for _, tc := range []struct{ host, path, want string }{
		{"shop." + oh + "." + servertest.PcSiteDomain, "/a?b=1", "https://shop." + nh + "." + servertest.PcSiteDomain + "/a?b=1"},
		{oh + "." + servertest.PcSiteDomain, "/shop/a", "https://" + nh + "." + servertest.PcSiteDomain + "/shop/a"}, // then on to the site host
		{nh + "." + servertest.PcSiteDomain, "/shop/a", "https://shop." + nh + "." + servertest.PcSiteDomain + "/a"},
		{oh + "." + servertest.PcSiteDomain, "/", "https://" + nh + "." + servertest.PcSiteDomain + "/"},
		{servertest.PcContentHost, "/internal/site-redirect/" + oh + "/shop/a", "https://shop." + nh + "." + servertest.PcSiteDomain + "/a"},
		{servertest.PcContentHost, "/internal/showcase/" + oh, "/" + nh},
	} {
		r := a.At(t, "GET", tc.host, tc.path, nil, nil)
		if r.Status/100 != 3 || r.Header.Get("Location") != tc.want {
			t.Errorf("%s%s: %d %q want %q", tc.host, tc.path, r.Status, r.Header.Get("Location"), tc.want)
		}
	}
	if r := a.At(t, "GET", "shop."+nh+"."+servertest.PcSiteDomain, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("new site host: %d %s", r.Status, r.Body)
	}
	// The owner app at <apex>/<old> follows the account.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "https://"+apex+"/"+oh, nil)
	a.Sites.OwnerAppOrStatic(http.NotFoundHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/"+nh {
		t.Fatalf("owner app alias: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// A custom domain stays attached, and the files follow the new handle on disk.
	if r := a.At(t, "POST", apex, "/v1/sites/shop/domain", map[string]string{"domain": domain}, okey); r.Status != 200 {
		t.Fatalf("domain after rename: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(filepath.Join(a.Sites.Disk.DataDir(), "handles", nh, "shop", "current", "index.html")); err != nil {
		t.Fatalf("new handle link: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.Sites.Disk.DataDir(), "handles", oh, "shop", "current", "index.html")); err != nil {
		t.Fatalf("old handle link kept for old content-host paths: %v", err)
	}

	// Nobody else can take the old handle.
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh}, map[string]string{"X-API-Key": oscar.Key}); r.Status != http.StatusConflict {
		t.Fatalf("old handle taken by someone else: %d %s", r.Status, r.Body)
	}
	// Once per 30 days after publishing.
	r = a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh + "-third"}, okey)
	if r.Status != http.StatusTooManyRequests || r.JSON(t)["next_change_after"] == nil {
		t.Fatalf("second change inside the window: %d %s", r.Status, r.Body)
	}
	if _, err := a.Database.Exec(`UPDATE handle_aliases SET created_at = now() - interval '31 days' WHERE user_id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET handle_changed_at = now() - interval '31 days' WHERE id = $1`, uid); err != nil {
		t.Fatal(err)
	}
	// Going back to one's own old handle is allowed, and both old names keep redirecting.
	if r := a.At(t, "PATCH", apex, "/v1/me", map[string]string{"handle": oh}, okey); r.Status != 200 || r.JSON(t)["handle"] != oh {
		t.Fatalf("back to the old handle: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "shop."+nh+"."+servertest.PcSiteDomain, "/", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://shop."+oh+"."+servertest.PcSiteDomain+"/" {
		t.Fatalf("second alias: %d %q", r.Status, r.Header.Get("Location"))
	}
}

// Before anything is published the handle changes freely and leaves no alias.
func TestHandleChangeBeforePublishingIsFree(t *testing.T) {
	a, _ := servertest.NewSiteApp(t, "canonical")
	pat := a.NewPerson(t, "pat")
	var uid string
	if err := a.Database.QueryRow(`SELECT id FROM users WHERE username = $1`, pat.Email).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	ph := "pat-" + uid[:8]
	key := map[string]string{"X-API-Key": pat.Key}
	for _, h := range []string{ph + "-a", ph + "-b"} {
		if r := a.At(t, "PATCH", servertest.PcSiteDomain, "/v1/me", map[string]string{"handle": h}, key); r.Status != 200 || r.JSON(t)["handle"] != h {
			t.Fatalf("change to %s: %d %s", h, r.Status, r.Body)
		}
	}
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM handle_aliases WHERE user_id = $1`, uid).Scan(&n); err != nil || n != 0 {
		t.Fatalf("aliases before publishing: %d %v", n, err)
	}
}
