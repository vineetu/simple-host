package handler

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

	db "github.com/vsriram/simple-host/internal/db"
)

// Regression tests for the cp/integrate review (sections A and C). Needs
// DB_DSN (db/schema.sql applied).

// waitBlocked gives a request started in a goroutine time to reach the lock
// the test is holding.
func waitBlocked() { time.Sleep(300 * time.Millisecond) }

// deleteBehindLock does what a delete does (row marked, files moved out)
// while the test holds the site's lock, as a delete that finished first.
func (a *privateApp) deleteBehindLock(t *testing.T, uid, siteID, name string) {
	t.Helper()
	if err := db.MarkSiteDeleted(context.Background(), a.database, siteID); err != nil {
		t.Fatal(err)
	}
	if err := a.sites.disk.TrashSite(uid, name, siteID); err != nil {
		t.Fatal(err)
	}
}

// A1: a deploy or rollback that waited for the lock while the site was
// deleted must not bring it back.
func TestDeployAndRollbackAfterDeleteDoNotResurrect(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	a.deploy(t, olive, "blog")
	uid, _ := a.userID(t, olive)
	okey := map[string]string{"X-API-Key": olive.key}
	// A second version of blog to roll back from.
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, okey); r.status != 200 {
		t.Fatalf("blog v2: %d %s", r.status, r.body)
	}

	for _, c := range []struct {
		site, method, path string
		body               any
	}{
		{"shop", "PUT", "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "late"}}},
		{"blog", "PUT", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}},
	} {
		siteID := a.siteID(t, olive, c.site)
		unlock := a.sites.lockSite(uid, c.site)
		done := make(chan resp, 1)
		go func() { done <- a.at(t, c.method, "simple-host.test", c.path, c.body, okey) }()
		waitBlocked()
		a.deleteBehindLock(t, uid, siteID, c.site)
		unlock()
		r := <-done
		if r.status != http.StatusNotFound {
			t.Fatalf("%s after delete: %d %s", c.path, r.status, r.body)
		}
		if _, err := os.Stat(a.sites.disk.SiteDir(uid, c.site)); !os.IsNotExist(err) {
			t.Fatalf("%s: site folder came back: %v", c.site, err)
		}
		if _, err := db.GetDeletedSiteByUser(context.Background(), a.database, uid, c.site); err != nil {
			t.Fatalf("%s no longer in Recently deleted: %v", c.site, err)
		}
	}
	// The row lock itself refuses a deleted site.
	tx, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := db.LockSiteForUpdate(context.Background(), tx, a.siteIDAny(t, uid, "shop")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LockSiteForUpdate on a deleted site: %v", err)
	}
}

func (a *privateApp) siteIDAny(t *testing.T, uid, name string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT id FROM sites WHERE user_id = $1 AND name = $2`, uid, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// A2: an admin take-down that waited while the site was renamed marks the
// renamed folder and never recreates the old one.
func TestTakedownFollowsRenameUnderLock(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "shop")
	admin := map[string]string{"X-API-Key": a.admin}

	unlock := a.sites.lockSite(uid, "shop")
	done := make(chan resp, 1)
	go func() {
		done <- a.at(t, "POST", "simple-host.test", "/v1/admin/sites/"+siteID+"/suspend", map[string]string{"reason": "spam"}, admin)
	}()
	waitBlocked()
	if err := a.sites.disk.RenameSite(uid, "shop", "store"); err != nil {
		t.Fatal(err)
	}
	if err := db.RenameSite(context.Background(), a.database, siteID, "store", ""); err != nil {
		t.Fatal(err)
	}
	unlock()
	if r := <-done; r.status != 200 {
		t.Fatalf("suspend: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(a.sites.disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("old folder recreated: %v", err)
	}
	if !a.sites.disk.IsSuspended(uid, "store") {
		t.Fatal("renamed site not marked")
	}
	// A marker is never written into a folder that does not exist.
	if err := a.sites.disk.SetSuspended(uid, "ghost", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.sites.disk.SiteDir(uid, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("SetSuspended created a folder: %v", err)
	}
}

// A3: the purge keeps a taken-down site, and a site of a suspended person.
func TestPurgeKeepsTakenDownSites(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "home")
	okey := map[string]string{"X-API-Key": olive.key}
	shopID, homeID := a.siteID(t, olive, "shop"), a.siteID(t, oscar, "home")
	oscarID, _ := a.userID(t, oscar)
	for _, p := range []struct {
		p    person
		site string
	}{{olive, "shop"}, {oscar, "home"}} {
		if r := a.at(t, "DELETE", "simple-host.test", "/v1/sites/"+p.site, nil, map[string]string{"X-API-Key": p.p.key}); r.status != 204 {
			t.Fatalf("delete %s: %d %s", p.site, r.status, r.body)
		}
	}
	_ = okey
	if err := db.SetSiteSuspended(context.Background(), a.database, shopID, "evidence"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE users SET suspended_at = now(), suspended_reason = 'x' WHERE id = $1`, oscarID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id IN ($1, $2)`, shopID, homeID); err != nil {
		t.Fatal(err)
	}
	a.sites.purgeDeletedSites(context.Background())
	for _, id := range []string{shopID, homeID} {
		var n int
		if err := a.database.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("taken-down site %s purged (n=%d, %v)", id, n, err)
		}
		if _, err := db.PurgeDeletedSite(context.Background(), a.database, id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("PurgeDeletedSite re-check let %s through: %v", id, err)
		}
	}
}

// A6: a rename re-points the earlier address a site still serves while its
// new domain is pending, not only the new domain.
func TestRenameRepointsPreviousDomain(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	uid, _ := a.userID(t, olive)
	key := map[string]string{"X-API-Key": olive.key}
	free := uniq("olv") + "." + pcSiteDomain
	custom := uniq("shop") + ".example.test"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free}, key); r.status != 200 {
		t.Fatalf("claim: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": custom}, key); r.status != 200 {
		t.Fatalf("bind: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", pcSiteDomain, "/v1/sites/shop", map[string]string{"name": "store"}, key); r.status != 200 {
		t.Fatalf("rename: %d %s", r.status, r.body)
	}
	want := filepath.Join("..", "by-id", uid, "store")
	for _, d := range []string{free, custom} {
		got, err := os.Readlink(filepath.Join(a.sites.disk.DataDir(), "domains", d))
		if err != nil || got != want {
			t.Fatalf("domains/%s -> %q (%v), want %q", d, got, err, want)
		}
	}
	if r := a.at(t, "GET", free, "/", nil, nil); r.status != 200 || !strings.Contains(string(r.body), "shop") {
		t.Fatalf("earlier address after rename: %d %s", r.status, r.body)
	}
}

// A7: restoring a site taken down while deleted puts the marker in place
// before the folder is served; if it cannot, the restore fails and the site
// stays deleted.
func TestRestoreTakenDownSiteMarksFirst(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	uid, _ := a.userID(t, olive)
	siteID := a.siteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	if r := a.at(t, "DELETE", "simple-host.test", "/v1/sites/shop", nil, key); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	if err := db.SetSiteSuspended(context.Background(), a.database, siteID, "evidence"); err != nil {
		t.Fatal(err)
	}
	trash := a.sites.disk.TrashDir(uid, siteID)
	if err := os.Chmod(trash, 0o555); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/shop/restore", nil, key)
	_ = os.Chmod(trash, 0o755)
	if r.status != http.StatusInternalServerError {
		t.Fatalf("restore with unwritable marker: %d %s", r.status, r.body)
	}
	if _, err := os.Stat(a.sites.disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("served without its marker: %v", err)
	}
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/shop/restore", nil, key); r.status != 200 {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if !a.sites.disk.IsSuspended(uid, "shop") {
		t.Fatal("restored taken-down site has no marker")
	}
}

// A8: the purge never unlinks a domain someone else has bound since.
func TestPurgeLeavesAnotherSitesDomainLink(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	siteID := a.siteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	custom := uniq("shop") + ".example.test"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": custom}, key); r.status != 200 {
		t.Fatalf("bind: %d %s", r.status, r.body)
	}
	if r := a.at(t, "DELETE", "simple-host.test", "/v1/sites/shop", nil, key); r.status != 204 {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	link := filepath.Join(a.sites.disk.DataDir(), "domains", custom)
	other := filepath.Join("..", "by-id", "someone-else", "site")
	_ = os.Remove(link)
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.sites.purgeDeletedSites(context.Background())
	if got, err := os.Readlink(link); err != nil || got != other {
		t.Fatalf("other site's link: %q %v", got, err)
	}
}

// A9: the earlier address is checked on its own clock and let go once it has
// failed for 72 hours; a binding whose certificate keeps failing is released
// after a week and the earlier address comes back.
func TestPreviousDomainLapsesAndStuckBindingExpires(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	siteID := a.siteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	first := uniq("first") + ".invalid"
	second := uniq("second") + ".invalid"
	ctx := context.Background()
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": first}, key); r.status != 200 {
		t.Fatalf("bind first: %d %s", r.status, r.body)
	}
	a.verify(t, siteID, first)
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": second}, key); r.status != 200 {
		t.Fatalf("bind second: %d %s", r.status, r.body)
	}
	if got := a.domainInfo(t, siteID).PreviousDomain; got != first {
		t.Fatalf("previous: %q", got)
	}

	// Week-old binding with a failing certificate: released, first comes back.
	if _, err := a.database.Exec(`UPDATE sites SET domain_cert_status = 'failed', domain_bound_at = now() - interval '8 days' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	released, err := db.ReleaseExpiredDomains(ctx, a.database)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range released {
		if r.SiteID == siteID && r.Domain == second && r.PreviousDomain == first {
			found = true
		}
	}
	if info := a.domainInfo(t, siteID); !found || info.Domain != first || info.PreviousDomain != "" {
		t.Fatalf("stuck binding not released: %v %+v", found, info)
	}

	// Pending again, with first as the earlier address. Its checks fail
	// (.invalid never resolves): the clock starts, and past 72 h it goes.
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": second}, key); r.status != 200 {
		t.Fatalf("rebind second: %d %s", r.status, r.body)
	}
	bd := db.BoundDomain{SiteID: siteID, Domain: second, Status: "pending", PreviousDomain: first}
	a.sites.checkPreviousDomain(ctx, bd, map[string]bool{})
	var since sql.NullTime
	if err := a.database.QueryRow(`SELECT previous_domain_failing_since FROM sites WHERE id = $1`, siteID).Scan(&since); err != nil || !since.Valid {
		t.Fatalf("clock not started: %v %v", since, err)
	}
	if a.domainInfo(t, siteID).PreviousDomain != first {
		t.Fatal("let go too early")
	}
	if _, err := a.database.Exec(`UPDATE sites SET previous_domain_failing_since = now() - interval '73 hours' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.sites.checkPreviousDomain(ctx, bd, map[string]bool{})
	if info := a.domainInfo(t, siteID); info.PreviousDomain != "" || info.Domain != second {
		t.Fatalf("earlier address not let go: %+v", info)
	}
	if _, err := os.Lstat(filepath.Join(a.sites.disk.DataDir(), "domains", first)); !os.IsNotExist(err) {
		t.Fatalf("earlier address still linked: %v", err)
	}
}

// A10: DELETE /domain with ?domain= drops only that domain; the bind
// unbinds the pending domain it really replaced.
func TestDomainDeleteNamesTheDomain(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	a.deploy(t, olive, "shop")
	siteID := a.siteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.key}
	one := uniq("one") + ".example.test"
	two := uniq("two") + ".example.test"
	if r := a.at(t, "POST", pcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": one}, key); r.status != 200 {
		t.Fatalf("bind one: %d %s", r.status, r.body)
	}
	released, replaced, err := db.BindCustomDomain(context.Background(), a.database, siteID, two)
	if err != nil || released != nil || replaced != one {
		t.Fatalf("bind two: %v %v %q", released, err, replaced)
	}
	r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain?domain="+one, nil, key)
	if r.status != http.StatusConflict || !strings.Contains(string(r.body), "domain_changed") {
		t.Fatalf("delete stale domain: %d %s", r.status, r.body)
	}
	if got := a.domainInfo(t, siteID).Domain; got != two {
		t.Fatalf("domain changed by a refused delete: %q", got)
	}
	if r := a.at(t, "DELETE", pcSiteDomain, "/v1/sites/shop/domain?domain="+two, nil, key); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
}

// C1: sites in Recently deleted count toward the per-account cap.
func TestDeletedSitesCountTowardQuota(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive := a.newPerson(t, "olive")
	uid, _ := a.userID(t, olive)
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
		SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, maxSitesPerUser()); err != nil {
		t.Fatal(err)
	}
	r := a.at(t, "POST", "simple-host.test", "/v1/sites/fresh/files",
		map[string]any{"files": map[string]string{"index.html": "x"}}, map[string]string{"X-API-Key": olive.key})
	if r.status != http.StatusForbidden || !strings.Contains(string(r.body), "site_quota_reached") {
		t.Fatalf("create over the cap: %d %s", r.status, r.body)
	}
}

// C4: the per-site lock is per account, and a delete or restore of a name
// the account does not have takes no lock at all.
func TestSiteLocksPerAccount(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	a.deploy(t, olive, "shop")
	a.deploy(t, oscar, "shop")
	oliveID, _ := a.userID(t, olive)
	oscarID, _ := a.userID(t, oscar)
	unlock := a.sites.lockSite(oliveID, "shop")
	done := make(chan resp, 1)
	go func() {
		done <- a.at(t, "PUT", "simple-host.test", "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "o2"}}, map[string]string{"X-API-Key": oscar.key})
	}()
	select {
	case r := <-done:
		if r.status != 200 {
			t.Fatalf("other account's deploy: %d %s", r.status, r.body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("another account's site of the same name waited on this lock")
	}
	unlock()
	for _, c := range []struct{ method, path string }{{"DELETE", "/v1/sites/nope"}, {"POST", "/v1/sites/nope/restore"}} {
		if r := a.at(t, c.method, "simple-host.test", c.path, nil, map[string]string{"X-API-Key": oscar.key}); r.status != 404 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.status, r.body)
		}
	}
	if _, ok := a.sites.uploadLocks.Load(oscarID + "/nope"); ok {
		t.Fatal("a lock was made for a name the account does not have")
	}
}

// C3/C6: a handle rename holds the old handle's namespace lock, and never
// removes another account's alias.
func TestHandleRenameLocksOldNameAndKeepsOthersAliases(t *testing.T) {
	a := newPersonApp(t, "serve")
	olive, oscar := a.newPerson(t, "olive"), a.newPerson(t, "oscar")
	oliveID, oh := a.userID(t, olive)
	oscarID, _ := a.userID(t, oscar)
	ctx := context.Background()

	// Another transaction holds <old>.<domain>: the rename must wait for it.
	hold, err := a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, strings.ToLower(oh)+"."+pcSiteDomain); err != nil {
		t.Fatal(err)
	}
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	_, err = db.RenameHandle(tctx, a.database, oliveID, uniq("olnew"))
	cancel()
	hold.Rollback()
	if err == nil {
		t.Fatal("rename did not wait for the old handle's namespace lock")
	}

	// With the namespace checks off, the new handle may equal another
	// account's alias; that alias stays.
	alias := uniq("alias")
	if _, err := a.database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, alias, oscarID); err != nil {
		t.Fatal(err)
	}
	db.SetPlatformDomain("")
	defer db.SetPlatformDomain(pcSiteDomain)
	if _, err := db.RenameHandle(ctx, a.database, oliveID, alias); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := a.database.QueryRow(`SELECT user_id FROM handle_aliases WHERE handle = $1`, alias).Scan(&owner); err != nil || owner != oscarID {
		t.Fatalf("other account's alias: %q %v", owner, err)
	}
}
