package servertest

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
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// Regression tests for the cp/integrate review (sections A and C). Needs
// DB_DSN (host/db/schema.sql applied).

// waitBlocked gives a request started in a goroutine time to reach the lock
// the test is holding.
func waitBlocked() { time.Sleep(300 * time.Millisecond) }

// A1: a deploy or rollback that waited for the lock while the site was
// deleted must not bring it back.
func TestDeployAndRollbackAfterDeleteDoNotResurrect(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, olive, "blog")
	uid, _ := a.UserID(t, olive)
	okey := map[string]string{"X-API-Key": olive.Key}
	// A second version of blog to roll back from.
	if r := a.At(t, "PUT", "simple-host.test", "/v1/sites/blog/files", map[string]any{"files": map[string]string{"index.html": "v2"}}, okey); r.Status != 200 {
		t.Fatalf("blog v2: %d %s", r.Status, r.Body)
	}

	for _, c := range []struct {
		site, method, path string
		body               any
	}{
		{"shop", "PUT", "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "late"}}},
		{"blog", "PUT", "/v1/sites/blog/active-version", map[string]int{"version_number": 1}},
	} {
		siteID := a.SiteID(t, olive, c.site)
		unlock := a.Sites.LockSite(uid, c.site)
		done := make(chan Resp, 1)
		go func() { done <- a.At(t, c.method, "simple-host.test", c.path, c.body, okey) }()
		waitBlocked()
		a.deleteBehindLock(t, uid, siteID, c.site)
		unlock()
		r := <-done
		if r.Status != http.StatusNotFound {
			t.Fatalf("%s after delete: %d %s", c.path, r.Status, r.Body)
		}
		if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, c.site)); !os.IsNotExist(err) {
			t.Fatalf("%s: site folder came back: %v", c.site, err)
		}
		if _, err := sitesrepo.GetDeletedSiteByUser(context.Background(), a.Database, uid, c.site); err != nil {
			t.Fatalf("%s no longer in Recently deleted: %v", c.site, err)
		}
	}
	// The row lock itself refuses a deleted site.
	tx, err := a.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := sitesrepo.LockSiteForUpdate(context.Background(), tx, a.SiteIDAny(t, uid, "shop")); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LockSiteForUpdate on a deleted site: %v", err)
	}
}

// A2: an admin take-down that waited while the site was renamed marks the
// renamed folder and never recreates the old one.
func TestTakedownFollowsRenameUnderLock(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	uid, _ := a.UserID(t, olive)
	siteID := a.SiteID(t, olive, "shop")
	admin := map[string]string{"X-API-Key": a.Admin}

	unlock := a.Sites.LockSite(uid, "shop")
	done := make(chan Resp, 1)
	go func() {
		done <- a.At(t, "POST", "simple-host.test", "/v1/admin/sites/"+siteID+"/suspend", map[string]string{"reason": "spam"}, admin)
	}()
	waitBlocked()
	if err := a.Sites.Disk.RenameSite(uid, "shop", "store"); err != nil {
		t.Fatal(err)
	}
	if err := sitesrepo.RenameSite(context.Background(), a.Database, siteID, "store", ""); err != nil {
		t.Fatal(err)
	}
	unlock()
	if r := <-done; r.Status != 200 {
		t.Fatalf("suspend: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("old folder recreated: %v", err)
	}
	if !a.Sites.Disk.IsSuspended(uid, "store") {
		t.Fatal("renamed site not marked")
	}
	// A marker is never written into a folder that does not exist.
	if err := a.Sites.Disk.SetSuspended(uid, "ghost", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "ghost")); !os.IsNotExist(err) {
		t.Fatalf("SetSuspended created a folder: %v", err)
	}
}

// A3: the purge keeps a taken-down site, and a site of a suspended person.
func TestPurgeKeepsTakenDownSites(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "home")
	okey := map[string]string{"X-API-Key": olive.Key}
	shopID, homeID := a.SiteID(t, olive, "shop"), a.SiteID(t, oscar, "home")
	oscarID, _ := a.UserID(t, oscar)
	for _, p := range []struct {
		p    Person
		site string
	}{{olive, "shop"}, {oscar, "home"}} {
		if r := a.At(t, "DELETE", "simple-host.test", "/v1/sites/"+p.site, nil, map[string]string{"X-API-Key": p.p.Key}); r.Status != 204 {
			t.Fatalf("delete %s: %d %s", p.site, r.Status, r.Body)
		}
	}
	_ = okey
	if err := sitesrepo.SetSiteSuspended(context.Background(), a.Database, shopID, "evidence"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE users SET suspended_at = now(), suspended_reason = 'x' WHERE id = $1`, oscarID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id IN ($1, $2)`, shopID, homeID); err != nil {
		t.Fatal(err)
	}
	a.Sites.PurgeDeletedSites(context.Background())
	for _, id := range []string{shopID, homeID} {
		var n int
		if err := a.Database.QueryRow(`SELECT count(*) FROM sites WHERE id = $1`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("taken-down site %s purged (n=%d, %v)", id, n, err)
		}
		// Bypass the sweep filter to exercise the transaction and locking query
		// shared by app/sites, including its suspension recheck.
		if _, err := sitesrepo.PurgeDeletedSite(context.Background(), a.Database, id, 7*24*time.Hour); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("PurgeDeletedSite re-check let %s through: %v", id, err)
		}
	}
}

// A6: a rename re-points the earlier address a site still serves while its
// new domain is pending, not only the new domain.
func TestRenameRepointsPreviousDomain(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	uid, _ := a.UserID(t, olive)
	key := map[string]string{"X-API-Key": olive.Key}
	free := Uniq("olv") + "." + PcSiteDomain
	custom := Uniq("shop") + ".example.test"
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": free}, key); r.Status != 200 {
		t.Fatalf("claim: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": custom}, key); r.Status != 200 {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", PcSiteDomain, "/v1/sites/shop", map[string]string{"name": "store"}, key); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	want := filepath.Join("..", "by-id", uid, "store")
	for _, d := range []string{free, custom} {
		got, err := os.Readlink(filepath.Join(a.Sites.Disk.DataDir(), "domains", d))
		if err != nil || got != want {
			t.Fatalf("domains/%s -> %q (%v), want %q", d, got, err, want)
		}
	}
	if r := a.At(t, "GET", free, "/", nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), "shop") {
		t.Fatalf("earlier address after rename: %d %s", r.Status, r.Body)
	}
}

// A7: restoring a site taken down while deleted puts the marker in place
// before the folder is served; if it cannot, the restore fails and the site
// stays deleted.
func TestRestoreTakenDownSiteMarksFirst(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	uid, _ := a.UserID(t, olive)
	siteID := a.SiteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.Key}
	if r := a.At(t, "DELETE", "simple-host.test", "/v1/sites/shop", nil, key); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	if err := sitesrepo.SetSiteSuspended(context.Background(), a.Database, siteID, "evidence"); err != nil {
		t.Fatal(err)
	}
	trash := a.Sites.Disk.TrashDir(uid, siteID)
	if err := os.Chmod(trash, 0o555); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "POST", "simple-host.test", "/v1/sites/shop/restore", nil, key)
	_ = os.Chmod(trash, 0o755)
	if r.Status != http.StatusInternalServerError {
		t.Fatalf("restore with unwritable marker: %d %s", r.Status, r.Body)
	}
	if _, err := os.Stat(a.Sites.Disk.SiteDir(uid, "shop")); !os.IsNotExist(err) {
		t.Fatalf("served without its marker: %v", err)
	}
	if r := a.At(t, "POST", "simple-host.test", "/v1/sites/shop/restore", nil, key); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if !a.Sites.Disk.IsSuspended(uid, "shop") {
		t.Fatal("restored taken-down site has no marker")
	}
}

// A8: the purge never unlinks a domain someone else has bound since.
func TestPurgeLeavesAnotherSitesDomainLink(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	siteID := a.SiteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.Key}
	custom := Uniq("shop") + ".example.test"
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": custom}, key); r.Status != 200 {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "DELETE", "simple-host.test", "/v1/sites/shop", nil, key); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	link := filepath.Join(a.Sites.Disk.DataDir(), "domains", custom)
	other := filepath.Join("..", "by-id", "someone-else", "site")
	_ = os.Remove(link)
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.Exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.Sites.PurgeDeletedSites(context.Background())
	if got, err := os.Readlink(link); err != nil || got != other {
		t.Fatalf("other site's link: %q %v", got, err)
	}
}

// A9: the earlier address is checked on its own clock and let go once it has
// failed for 72 hours; a binding whose certificate keeps failing is released
// after a week and the earlier address comes back.
func TestPreviousDomainLapsesAndStuckBindingExpires(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	siteID := a.SiteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.Key}
	first := Uniq("first") + ".invalid"
	second := Uniq("second") + ".invalid"
	ctx := context.Background()
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": first}, key); r.Status != 200 {
		t.Fatalf("bind first: %d %s", r.Status, r.Body)
	}
	a.Verify(t, siteID, first)
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": second}, key); r.Status != 200 {
		t.Fatalf("bind second: %d %s", r.Status, r.Body)
	}
	if got := a.DomainInfo(t, siteID).PreviousDomain; got != first {
		t.Fatalf("previous: %q", got)
	}

	// Week-old binding with a failing certificate: released, first comes back.
	if _, err := a.Database.Exec(`UPDATE sites SET domain_cert_status = 'failed', domain_bound_at = now() - interval '8 days' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	released, err := sitesrepo.ReleaseExpiredDomains(ctx, a.Database, 7*24*time.Hour, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range released {
		if r.SiteID == siteID && r.Domain == second && r.PreviousDomain == first {
			found = true
		}
	}
	if info := a.DomainInfo(t, siteID); !found || info.Domain != first || info.PreviousDomain != "" {
		t.Fatalf("stuck binding not released: %v %+v", found, info)
	}

	// Pending again, with first as the earlier address. Its checks fail
	// (.invalid never resolves): the clock starts, and past 72 h it goes.
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": second}, key); r.Status != 200 {
		t.Fatalf("rebind second: %d %s", r.Status, r.Body)
	}
	bd := model.BoundDomain{SiteID: siteID, Domain: second, Status: "pending", PreviousDomain: first}
	a.Sites.CheckPreviousDomain(ctx, bd, map[string]bool{})
	var since sql.NullTime
	if err := a.Database.QueryRow(`SELECT previous_domain_failing_since FROM sites WHERE id = $1`, siteID).Scan(&since); err != nil || !since.Valid {
		t.Fatalf("clock not started: %v %v", since, err)
	}
	if a.DomainInfo(t, siteID).PreviousDomain != first {
		t.Fatal("let go too early")
	}
	if _, err := a.Database.Exec(`UPDATE sites SET previous_domain_failing_since = now() - interval '73 hours' WHERE id = $1`, siteID); err != nil {
		t.Fatal(err)
	}
	a.Sites.CheckPreviousDomain(ctx, bd, map[string]bool{})
	if info := a.DomainInfo(t, siteID); info.PreviousDomain != "" || info.Domain != second {
		t.Fatalf("earlier address not let go: %+v", info)
	}
	if _, err := os.Lstat(filepath.Join(a.Sites.Disk.DataDir(), "domains", first)); !os.IsNotExist(err) {
		t.Fatalf("earlier address still linked: %v", err)
	}
}

// A10: DELETE /domain with ?domain= drops only that domain; the bind
// unbinds the pending domain it really replaced.
func TestDomainDeleteNamesTheDomain(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	a.Deploy(t, olive, "shop")
	siteID := a.SiteID(t, olive, "shop")
	key := map[string]string{"X-API-Key": olive.Key}
	one := Uniq("one") + ".example.test"
	two := Uniq("two") + ".example.test"
	if r := a.At(t, "POST", PcSiteDomain, "/v1/sites/shop/domain", map[string]string{"domain": one}, key); r.Status != 200 {
		t.Fatalf("bind one: %d %s", r.Status, r.Body)
	}
	released, replaced, err := sitesrepo.BindCustomDomain(context.Background(), a.Database, siteID, two)
	if err != nil || released != nil || replaced != one {
		t.Fatalf("bind two: %v %v %q", released, err, replaced)
	}
	r := a.At(t, "DELETE", PcSiteDomain, "/v1/sites/shop/domain?domain="+one, nil, key)
	if r.Status != http.StatusConflict || !strings.Contains(string(r.Body), "domain_changed") {
		t.Fatalf("delete stale domain: %d %s", r.Status, r.Body)
	}
	if got := a.DomainInfo(t, siteID).Domain; got != two {
		t.Fatalf("domain changed by a refused delete: %q", got)
	}
	if r := a.At(t, "DELETE", PcSiteDomain, "/v1/sites/shop/domain?domain="+two, nil, key); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
}

// C1: sites in Recently deleted count toward the per-account cap.
func TestDeletedSitesCountTowardQuota(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive := a.NewPerson(t, "olive")
	uid, _ := a.UserID(t, olive)
	if _, err := a.Database.Exec(`INSERT INTO sites (user_id, name, deleted_at)
		SELECT $1, 'gone-' || g, now() FROM generate_series(1, $2) g`, uid, a.Sites.Limits.MaxSitesPerAccount); err != nil {
		t.Fatal(err)
	}
	r := a.At(t, "POST", "simple-host.test", "/v1/sites/fresh/files",
		map[string]any{"files": map[string]string{"index.html": "x"}}, map[string]string{"X-API-Key": olive.Key})
	if r.Status != http.StatusForbidden || !strings.Contains(string(r.Body), "site_quota_reached") {
		t.Fatalf("create over the cap: %d %s", r.Status, r.Body)
	}
}

// C4: the per-site lock is per account, and a delete or restore of a name
// the account does not have takes no lock at all.
func TestSiteLocksPerAccount(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "shop")
	oliveID, _ := a.UserID(t, olive)
	oscarID, _ := a.UserID(t, oscar)
	unlock := a.Sites.LockSite(oliveID, "shop")
	done := make(chan Resp, 1)
	go func() {
		done <- a.At(t, "PUT", "simple-host.test", "/v1/sites/shop/files", map[string]any{"files": map[string]string{"index.html": "o2"}}, map[string]string{"X-API-Key": oscar.Key})
	}()
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
		if r := a.At(t, c.method, "simple-host.test", c.path, nil, map[string]string{"X-API-Key": oscar.Key}); r.Status != 404 {
			t.Fatalf("%s %s: %d %s", c.method, c.path, r.Status, r.Body)
		}
	}
	if _, ok := a.Sites.UploadLocks.Load(oscarID + "/nope"); ok {
		t.Fatal("a lock was made for a name the account does not have")
	}
}

// C3/C6: a handle rename holds the old handle's namespace lock, and never
// removes another account's alias.
func TestHandleRenameLocksOldNameAndKeepsOthersAliases(t *testing.T) {
	a := NewPersonApp(t, "serve")
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	oliveID, oh := a.UserID(t, olive)
	oscarID, _ := a.UserID(t, oscar)
	ctx := context.Background()

	// Another transaction holds <old>.<domain>: the rename must wait for it.
	hold, err := a.Database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, strings.ToLower(oh)+"."+PcSiteDomain); err != nil {
		t.Fatal(err)
	}
	tctx, cancel := context.WithTimeout(ctx, time.Second)
	_, err = accountsrepo.RenameHandle(tctx, a.Database, oliveID, Uniq("olnew"))
	cancel()
	hold.Rollback()
	if err == nil {
		t.Fatal("rename did not wait for the old handle's namespace lock")
	}

	// With the namespace checks off, the new handle may equal another
	// account's alias; that alias stays.
	alias := Uniq("alias")
	if _, err := a.Database.Exec(`INSERT INTO handle_aliases (handle, user_id) VALUES ($1, $2)`, alias, oscarID); err != nil {
		t.Fatal(err)
	}
	accountsrepo.SetPlatformDomain("")
	defer accountsrepo.SetPlatformDomain(PcSiteDomain)
	if _, err := accountsrepo.RenameHandle(ctx, a.Database, oliveID, alias); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := a.Database.QueryRow(`SELECT user_id FROM handle_aliases WHERE handle = $1`, alias).Scan(&owner); err != nil || owner != oscarID {
		t.Fatalf("other account's alias: %q %v", owner, err)
	}
}
