package sitestest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// waitBlocked gives a request started in a goroutine time to reach the lock
// the test is holding.
func waitBlocked() { time.Sleep(300 * time.Millisecond) }

func (a *App) purgeNow(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	a.Sites.PurgeDeletedSites(context.Background())
}

func (a *App) rowCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Recently deleted: a deleted site stops serving at once, keeps its files,
// versions and claimed name for 7 days with its name held, comes back whole
// on restore (owner only), and is purged after the window with its claimed
// name retired.
func TestRecentlyDeletedAndRestore(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	siteID := a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	a.Deploy(t, oscar, "home")
	ctx := context.Background()
	claimed := uniq("olv") + "." + rt.SiteDomain
	if r := a.bind(t, olive, "shop", claimed); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	for _, host := range []string{olive.SiteHost("shop"), claimed} {
		if r := a.At(t, "GET", host, "/", nil, nil); r.Status != http.StatusNotFound || strings.Contains(string(r.Body), "<h1>shop</h1>") {
			t.Fatalf("%s after delete: %d %s", host, r.Status, r.Body)
		}
	}
	if _, err := os.Stat(filepath.Join(a.Disk.TrashDir(olive.ID, siteID), "current", "index.html")); err != nil {
		t.Fatalf("files not kept: %v", err)
	}
	r := a.api(t, olive, "GET", "/v1/me/deleted-sites", nil)
	sites, _ := r.JSON(t)["sites"].([]any)
	if r.Status != 200 || len(sites) != 1 || sites[0].(map[string]any)["name"] != "shop" {
		t.Fatalf("deleted-sites: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, oscar, "GET", "/v1/me/deleted-sites", nil); strings.Contains(string(r.Body), "shop") {
		t.Fatalf("someone else's deleted site listed: %s", r.Body)
	}
	// The name is held: no create, no rename onto it, no claim by another, no restore by another.
	if r := a.files(t, olive, "POST", "shop", map[string]string{"index.html": "new"}); r.Status != http.StatusConflict || r.JSON(t)["recently_deleted"] != true {
		t.Fatalf("create over a deleted name: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "PATCH", "/v1/sites/blog", map[string]string{"name": "shop"}); r.Status != http.StatusConflict {
		t.Fatalf("rename onto a deleted name: %d %s", r.Status, r.Body)
	}
	if r := a.bind(t, oscar, "home", claimed); r.Status != http.StatusConflict {
		t.Fatalf("claimed name of a deleted site taken by someone else: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, oscar, "POST", "/v1/sites/shop/restore", nil); r.Status != http.StatusNotFound {
		t.Fatalf("restore someone else's site: %d %s", r.Status, r.Body)
	}
	// Restore: back whole.
	if r := a.api(t, olive, "POST", "/v1/sites/shop/restore", nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", claimed, "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>shop</h1>" {
		t.Fatalf("claimed host after restore: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "GET", "/v1/sites/shop/versions", nil); !strings.Contains(string(r.Body), `"version_number":1`) {
		t.Fatalf("versions after restore: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "POST", "/v1/sites/shop/restore", nil); r.Status != http.StatusConflict {
		t.Fatalf("restore a live site: %d %s", r.Status, r.Body)
	}
	// Purge: kept inside the window, gone after it; the claimed name stays retired.
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete again: %d", r.Status)
	}
	a.Sites.PurgeDeletedSites(ctx)
	if a.rowCount(t, siteID) != 1 {
		t.Fatal("purged inside the window")
	}
	a.purgeNow(t, siteID)
	if a.rowCount(t, siteID) != 0 {
		t.Fatal("row not purged")
	}
	if _, err := os.Stat(a.Disk.TrashDir(olive.ID, siteID)); !os.IsNotExist(err) {
		t.Fatalf("files not purged: %v", err)
	}
	if r := a.bind(t, oscar, "home", claimed); r.Status != http.StatusConflict {
		t.Fatalf("retired claimed name taken after the purge: %d %s", r.Status, r.Body)
	}
	if _, err := sitesrepo.GetRetiredName(ctx, a.Database, claimed, accountsrepo.HostForms(claimed)); err != nil {
		t.Fatalf("claimed name not retired by the purge: %v", err)
	}
	a.Deploy(t, olive, "shop") // the site name is free again
}

// A1: a deploy or rollback that waited for the lock while the site was
// deleted does not bring it back.
func TestDeployAndRollbackAfterDeleteDoNotResurrect(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	if r := a.files(t, olive, "PUT", "blog", map[string]string{"index.html": "v2"}); r.Status != 200 {
		t.Fatalf("blog v2: %d %s", r.Status, r.Body)
	}
	for _, c := range []struct {
		site, path string
		body       any
	}{
		{"shop", "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "late"}}},
		{"blog", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}},
	} {
		siteID := a.SiteID(t, olive, c.site)
		unlock := a.Sites.LockSite(olive.ID, c.site)
		done := make(chan rt.Resp, 1)
		go func() { done <- a.api(t, olive, "PUT", c.path, c.body) }()
		waitBlocked()
		if err := sitesrepo.MarkSiteDeleted(context.Background(), a.Database, siteID, 7*24*time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := a.Disk.TrashSite(olive.ID, c.site, siteID); err != nil {
			t.Fatal(err)
		}
		unlock()
		if r := <-done; r.Status != http.StatusNotFound {
			t.Fatalf("%s after delete: %d %s", c.path, r.Status, r.Body)
		}
		if _, err := os.Stat(a.siteDir(olive, c.site)); !os.IsNotExist(err) {
			t.Fatalf("%s: site folder came back: %v", c.site, err)
		}
	}
	tx, err := a.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var shopID string
	_ = a.Database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = 'shop'`, olive.ID).Scan(&shopID)
	if err := sitesrepo.LockSiteForUpdate(context.Background(), tx, shopID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LockSiteForUpdate on a deleted site: %v", err)
	}
}

// A2: a take-down that waited while the site was renamed marks the renamed
// folder and never recreates the old one; a renamed site that is taken
// down stays down on its new address.
func TestTakedownFollowsRenameUnderLock(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	siteID := a.Deploy(t, olive, "shop")
	unlock := a.Sites.LockSite(olive.ID, "shop")
	done := make(chan error, 1)
	go func() {
		_, err := a.Sites.LifecycleSvc().SuspendSite(context.Background(), siteID, "spam", "", true)
		done <- err
	}()
	waitBlocked()
	if err := a.Disk.RenameSite(olive.ID, "shop", "store"); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.RenameSite(context.Background(), a.Database, siteID, "store", ""); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatalf("suspend: %v", err)
	}
	if _, err := os.Stat(a.siteDir(olive, "shop")); !os.IsNotExist(err) {
		t.Fatalf("old folder recreated: %v", err)
	}
	if r := a.At(t, "GET", olive.SiteHost("store"), "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("renamed taken-down site: %d", r.Status)
	}
	if err := a.Disk.SetSuspended(olive.ID, "ghost", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.siteDir(olive, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("SetSuspended created a folder: %v", err)
	}
}

// A3/A7: the purge keeps a taken-down site and a suspended person's site;
// restoring a site taken down while deleted marks it before it is served,
// and fails (staying deleted) if it cannot.
func TestPurgeAndRestoreKeepTakeDowns(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shopID, homeID := a.Deploy(t, olive, "shop"), a.Deploy(t, oscar, "home")
	for _, c := range []struct {
		p    rt.Person
		site string
	}{{olive, "shop"}, {oscar, "home"}} {
		if r := a.api(t, c.p, "DELETE", "/v1/sites/"+c.site, nil); r.Status != 204 {
			t.Fatalf("delete %s: %d %s", c.site, r.Status, r.Body)
		}
	}
	ctx := context.Background()
	if err := sitesrepo.SetSiteSuspended(ctx, a.Database, shopID, "evidence"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at = now(), suspended_reason = 'x' WHERE id = $1`, oscar.ID); err != nil {
		t.Fatal(err)
	}
	a.purgeNow(t, shopID, homeID)
	for _, id := range []string{shopID, homeID} {
		if a.rowCount(t, id) != 1 {
			t.Fatalf("taken-down site %s purged", id)
		}
		if _, err := sitesrepo.PurgeDeletedSite(ctx, a.Database, id, 7*24*time.Hour); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("PurgeDeletedSite re-check let %s through: %v", id, err)
		}
	}
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now(), purge_at = now() + interval '7 days' WHERE id = $1`, shopID); err != nil {
		t.Fatal(err)
	}
	trash := a.Disk.TrashDir(olive.ID, shopID)
	if err := os.Chmod(trash, 0o555); err != nil {
		t.Fatal(err)
	}
	r := a.api(t, olive, "POST", "/v1/sites/shop/restore", nil)
	_ = os.Chmod(trash, 0o755)
	if r.Status != http.StatusInternalServerError {
		t.Fatalf("restore with unwritable marker: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.siteDir(olive, "shop")); !os.IsNotExist(err) {
		t.Fatalf("served without its marker: %v", err)
	}
	if r := a.api(t, olive, "POST", "/v1/sites/shop/restore", nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("shop"), "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("restored taken-down site served: %d", r.Status)
	}
}

// A8: the purge never unlinks a domain someone else has bound since.
func TestPurgeLeavesAnotherSitesDomainLink(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	siteID := a.Deploy(t, olive, "shop")
	custom := uniq("shop") + ".example.test"
	if r := a.bind(t, olive, "shop", custom); r.Status != 200 {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop", nil); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	link := filepath.Join(a.Disk.DataDir(), "domains", custom)
	other := filepath.Join("..", "by-id", "someone-else", "site")
	_ = os.Remove(link)
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	a.purgeNow(t, siteID)
	if got, err := os.Readlink(link); err != nil || got != other {
		t.Fatalf("other site's link: %q %v", got, err)
	}
}

// A6/A9: a rename re-points the earlier address a site still serves while
// its new domain is pending; the earlier address is let go after 72 hours of
// failed checks, and a binding stuck for a week gives it back.
func TestPreviousDomainFollowsRenameAndLapses(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	siteID := a.Deploy(t, olive, "shop")
	ctx := context.Background()
	first := uniq("first") + ".invalid"
	second := uniq("second") + ".invalid"
	a.bind(t, olive, "shop", first)
	a.verify(t, siteID, first)
	a.bind(t, olive, "shop", second)
	if r := a.api(t, olive, "PATCH", "/v1/sites/shop", map[string]string{"name": "store"}); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	want := filepath.Join("..", "by-id", olive.ID, "store")
	for _, d := range []string{first, second} {
		if got, err := os.Readlink(filepath.Join(a.Disk.DataDir(), "domains", d)); err != nil || got != want {
			t.Fatalf("domains/%s -> %q (%v), want %q", d, got, err, want)
		}
	}

	if _, err := a.Database.Exec(`UPDATE sites SET domain_cert_status = 'failed', domain_bound_at = now() - interval '8 days' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	if _, err := sitesrepo.ReleaseExpiredDomains(ctx, a.Database, 7*24*time.Hour, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if info := a.domainInfo(t, siteID); info.Domain != first || info.PreviousDomain != "" {
		t.Fatalf("stuck binding not released: %+v", info)
	}
	a.bind(t, olive, "store", second)
	bd := model.BoundDomain{SiteID: siteID, Domain: second, Status: "pending", PreviousDomain: first}
	a.Sites.CheckPreviousDomain(ctx, bd, map[string]bool{})
	if a.domainInfo(t, siteID).PreviousDomain != first {
		t.Fatal("let go too early")
	}
	if _, err := a.Database.Exec(`UPDATE sites SET previous_domain_failing_since = now() - interval '73 hours' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.Sites.CheckPreviousDomain(ctx, bd, map[string]bool{})
	if info := a.domainInfo(t, siteID); info.PreviousDomain != "" || info.Domain != second {
		t.Fatalf("earlier address not let go: %+v", info)
	}
	if _, err := os.Lstat(filepath.Join(a.Disk.DataDir(), "domains", first)); !os.IsNotExist(err) {
		t.Fatalf("earlier address still linked: %v", err)
	}
}

// C4: the per-site lock is per account, and a delete or restore of a name
// the account does not have takes no lock at all.
func TestSiteLocksPerAccount(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "shop")
	unlock := a.Sites.LockSite(olive.ID, "shop")
	done := make(chan rt.Resp, 1)
	go func() { done <- a.files(t, oscar, "PUT", "shop", map[string]string{"index.html": "o2"}) }()
	select {
	case r := <-done:
		if r.Status != 200 {
			t.Fatalf("other account's deploy: %d %s", r.Status, r.Body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("another account's site of the same name waited on this lock")
	}
	unlock()
	for _, c := range []struct{ method, path string }{{"DELETE", "/v1/sites/nope"}, {"POST", "/v1/sites/nope/restore"}} {
		if r := a.api(t, oscar, c.method, c.path, nil); r.Status != 404 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if _, ok := a.Sites.UploadLocks.Load(oscar.ID + "/nope"); ok {
		t.Fatal("a lock was made for a name the account does not have")
	}
}

// L5: a rollback whose commit fails leaves the served files as they were.
func TestRollbackServesOnlyWhatCommitted(t *testing.T) {
	a := NewApp(t)
	ann := a.NewPerson(t, "ann")
	siteID := a.Deploy(t, ann, "blog")
	if r := a.files(t, ann, "PUT", "blog", map[string]string{"index.html": "v2"}); r.Status != 200 {
		t.Fatalf("v2: %d %s", r.Status, r.Body)
	}
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION sitestest_fail_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'commit refused'; END $$`,
		`DROP TRIGGER IF EXISTS sitestest_fail_commit ON sites`,
		`CREATE CONSTRAINT TRIGGER sitestest_fail_commit AFTER UPDATE ON sites DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.id = '` + siteID + `') EXECUTE FUNCTION sitestest_fail_commit()`,
	} {
		if _, err := a.Database.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = a.Database.Exec(`DROP TRIGGER IF EXISTS sitestest_fail_commit ON sites`) })
	if r := a.api(t, ann, "PUT", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}); r.Status != 500 {
		t.Fatalf("rollback with a failing commit: %d %s", r.Status, r.Body)
	}
	var active int
	_ = a.Database.QueryRow(`SELECT active_version FROM sites WHERE id = $1`, siteID).Scan(&active)
	if r := a.At(t, "GET", ann.SiteHost("blog"), "/", nil, nil); active != 2 || string(r.Body) != "v2" {
		t.Fatalf("after a failed commit: active_version %d, serving %q", active, r.Body)
	}
}

// The admin's download-all leaves out sites in Recently deleted.
func TestAdminExportLeavesOutDeletedSites(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "keep")
	a.Deploy(t, olive, "gone")
	if r := a.api(t, olive, "DELETE", "/v1/sites/gone", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	r := a.admin(t, "GET", "/v1/admin/export.tar.gz", nil)
	if r.Status != 200 {
		t.Fatalf("download all: %d %s", r.Status, r.Body)
	}
	var hasKeep, hasGone bool
	for name := range tarNames(t, r.Body) {
		hasKeep = hasKeep || strings.HasPrefix(name, olive.Handle+"/keep/")
		hasGone = hasGone || strings.HasPrefix(name, olive.Handle+"/gone/")
	}
	if !hasKeep || hasGone {
		t.Fatalf("download all: keep %v, deleted %v", hasKeep, hasGone)
	}
	if r := a.api(t, olive, "GET", "/v1/admin/export.tar.gz", nil); r.Status != 404 {
		t.Fatalf("download all as a non-admin: %d", r.Status)
	}
}

// A renamed site keeps its old links: every old address 302s to the new one
// with path and query, chains and renaming back work, a new site of the old
// name wins, and delete stops the redirect (restore brings it back, purge
// removes it).
func TestRenameKeepsOldLinks(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	siteID := a.Deploy(t, olive, "rsvp")
	person := olive.Handle + "." + rt.SiteDomain
	rename := func(from, to string) {
		t.Helper()
		if r := a.api(t, olive, "PATCH", "/v1/sites/"+from, map[string]string{"name": to}); r.Status != 200 || r.JSON(t)["old_url_status"] != "redirects" {
			t.Fatalf("rename %s -> %s: %d %s", from, to, r.Status, r.Body)
		}
	}
	wantMoved := func(old, now string) {
		t.Helper()
		want := "https://" + olive.SiteHost(now) + "/a/b?x=1"
		for _, tc := range []struct{ host, path string }{
			{olive.SiteHost(old), "/a/b?x=1"},
			{person, "/" + old + "/a/b?x=1"},
			{rt.ContentHost, "/internal/site-redirect/" + olive.Handle + "/" + old + "/a/b?x=1"},
		} {
			if r := a.At(t, "GET", tc.host, tc.path, nil, nil); r.Status != http.StatusFound || location(r) != want {
				t.Errorf("%s%s: %d %q, want %q", tc.host, tc.path, r.Status, location(r), want)
			}
		}
	}
	rename("rsvp", "wedding")
	wantMoved("rsvp", "wedding")
	rename("wedding", "party")
	wantMoved("rsvp", "party")
	wantMoved("wedding", "party")
	rename("party", "rsvp")
	if r := a.At(t, "GET", olive.SiteHost("rsvp"), "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>rsvp</h1>" {
		t.Fatalf("renamed back: %d %s", r.Status, r.Body)
	}
	wantMoved("party", "rsvp")
	// A new site of an old name wins; once it goes, the name does not come back.
	a.Deploy(t, olive, "wedding")
	if r := a.At(t, "GET", olive.SiteHost("wedding"), "/", nil, nil); r.Status != 200 || string(r.Body) != "<h1>wedding</h1>" {
		t.Fatalf("new site of the old name: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/wedding", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete new wedding: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("wedding"), "/", nil, nil); r.Status != 404 {
		t.Fatalf("old name came back: %d %q", r.Status, location(r))
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/rsvp", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("party"), "/", nil, nil); r.Status != 404 {
		t.Fatalf("old name of a deleted site: %d %q", r.Status, location(r))
	}
	if r := a.api(t, olive, "POST", "/v1/sites/rsvp/restore", nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	wantMoved("party", "rsvp")
	if r := a.api(t, olive, "DELETE", "/v1/sites/rsvp", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete again: %d %s", r.Status, r.Body)
	}
	a.purgeNow(t, siteID)
	var n int
	if err := a.Database.QueryRow(`SELECT count(*) FROM site_name_aliases WHERE site_id = $1`, siteID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old names after purge: %d %v", n, err)
	}
}

// The owner's offline switch: only the owner, one change per request; every
// address answers 503 while the owner still deploys; the operator's
// take-down wins; the switch follows rename, delete and restore.
func TestSiteOffline(t *testing.T) {
	a := NewApp(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shopID := a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	isOffline := func(host, path string) {
		t.Helper()
		if r := a.At(t, "GET", host, path, nil, nil); r.Status != http.StatusServiceUnavailable {
			t.Fatalf("%s%s: %d %s", host, path, r.Status, r.Body)
		}
	}
	setOffline := func(on bool) rt.Resp {
		return a.api(t, olive, "PATCH", "/v1/sites/shop", map[string]bool{"offline": on})
	}
	if r := a.api(t, oscar, "PATCH", "/v1/sites/shop", map[string]bool{"offline": true}); r.Status != 404 {
		t.Fatalf("someone else's site: %d %s", r.Status, r.Body)
	}
	for _, body := range []any{map[string]any{}, map[string]any{"name": "x", "offline": true}} {
		if r := a.api(t, olive, "PATCH", "/v1/sites/shop", body); r.Status != 400 {
			t.Fatalf("PATCH %v: %d %s", body, r.Status, r.Body)
		}
	}
	if r := setOffline(true); r.Status != 200 || r.JSON(t)["offline"] != true || !a.Disk.IsOffline(olive.ID, "shop") {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	isOffline(olive.SiteHost("shop"), "/")
	isOffline(olive.SiteHost("shop"), "/missing.png")
	if r := a.At(t, "GET", olive.SiteHost("blog"), "/", nil, nil); r.Status != 200 {
		t.Fatalf("other site: %d", r.Status)
	}
	if r := a.files(t, olive, "PUT", "shop", map[string]string{"index.html": "v2"}); r.Status != 200 {
		t.Fatalf("owner deploy: %d %s", r.Status, r.Body)
	}
	isOffline(olive.SiteHost("shop"), "/")
	claimed := uniq("olive-off") + "." + rt.SiteDomain
	if r := a.bind(t, olive, "shop", claimed); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	isOffline(claimed, "/")
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain", nil); r.Status != http.StatusNoContent {
		t.Fatalf("unclaim: %d %s", r.Status, r.Body)
	}
	// The operator's take-down wins and freezes the switch.
	ctx := context.Background()
	if _, err := a.Sites.LifecycleSvc().SuspendSite(ctx, shopID, "test", "", true); err != nil {
		t.Fatal(err)
	}
	if r := a.At(t, "GET", olive.SiteHost("shop"), "/", nil, nil); r.Status != http.StatusGone {
		t.Fatalf("take-down does not win: %d", r.Status)
	}
	if r := setOffline(false); r.Status != http.StatusForbidden {
		t.Fatalf("switch while taken down: %d %s", r.Status, r.Body)
	}
	if _, err := a.Sites.LifecycleSvc().SuspendSite(ctx, shopID, "", "", false); err != nil {
		t.Fatal(err)
	}
	isOffline(olive.SiteHost("shop"), "/")
	if r := a.api(t, olive, "PATCH", "/v1/sites/shop", map[string]string{"name": "store"}); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	isOffline(olive.SiteHost("store"), "/")
	if r := a.api(t, olive, "DELETE", "/v1/sites/store", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "POST", "/v1/sites/store/restore", nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	isOffline(olive.SiteHost("store"), "/")
	if r := a.api(t, olive, "PATCH", "/v1/sites/store", map[string]bool{"offline": false}); r.Status != 200 {
		t.Fatalf("online: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", olive.SiteHost("store"), "/", nil, nil); r.Status != 200 || string(r.Body) != "v2" || a.Disk.IsOffline(olive.ID, "store") {
		t.Fatalf("back online: %d %s", r.Status, r.Body)
	}
}

// A protected site stays gated through a rename, a delete and a restore,
// and a protected site created by the deploy route (access=specific) is
// gated from its first request on every address.
func TestProtectionFollowsTheSite(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "trip")
	a.protect(t, olive, "trip")
	gated := func(host, path string) {
		t.Helper()
		if r := a.At(t, "GET", host, path, nil, nil); r.Status == 200 || strings.Contains(string(r.Body), "<h1>") {
			t.Fatalf("%s%s: %d %s", host, path, r.Status, r.Body)
		}
	}
	if r := a.api(t, olive, "PATCH", "/v1/sites/trip", map[string]any{"name": "voyage"}); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	gated(olive.SiteHost("voyage"), "/")
	if r := a.api(t, olive, "DELETE", "/v1/sites/voyage", nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "POST", "/v1/sites/voyage/restore", nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	gated(olive.SiteHost("voyage"), "/")
	if !a.Disk.HasPasscodeMarker(olive.ID, "voyage") {
		t.Fatal("restored protected site lost its marker")
	}

	r := a.api(t, olive, "POST", "/v1/sites/private/files", map[string]any{"files": map[string]string{"index.html": "<h1>secret</h1>"}, "access": "specific"})
	if r.Status != 201 || r.JSON(t)["access"] != "specific" || !a.Disk.HasPasscodeMarker(olive.ID, "private") {
		t.Fatalf("create specific: %d %s", r.Status, r.Body)
	}
	for _, x := range []struct{ host, path string }{{olive.SiteHost("private"), "/"}, {olive.Handle + "." + rt.SiteDomain, "/private/"}, {rt.ContentHost, "/internal/passcode/" + olive.Handle + "/private/"}} {
		gated(x.host, x.path)
	}
	// An update with access=anyone does not open it; invalid access is refused.
	if r := a.api(t, olive, "PUT", "/v1/sites/private/files?create=1", map[string]any{"files": map[string]string{"index.html": "x"}, "access": "anyone"}); r.Status != 200 || r.JSON(t)["access"] != "specific" {
		t.Fatalf("anyone changed access: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "POST", "/v1/sites/bad/files", map[string]any{"files": map[string]string{"index.html": "x"}, "access": "invalid"}); r.Status != 400 {
		t.Fatalf("invalid access: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "PUT", "/v1/sites/trip-public/files?create=1", map[string]any{"files": map[string]string{"index.html": "x"}}); r.Status/100 != 2 {
		t.Fatalf("plain create: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "PUT", "/v1/sites/trip-public/files?create=1", map[string]any{"files": map[string]string{"index.html": "x"}, "access": "specific"}); r.Status != 409 || r.Code() != "access_differs" {
		t.Fatalf("differs: %d %s", r.Status, r.Body)
	}
}
