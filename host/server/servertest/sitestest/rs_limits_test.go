package sitestest

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vsriram/simple-host/host/edition"
	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/domains"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/sitesroute/fixture"
)

func rsNear(got, want time.Time) bool {
	d := got.Sub(want)
	return d > -time.Minute && d < time.Minute
}

// A deleted site keeps the purge date promised when it was deleted: a shorter
// DELETED_RETENTION_DAYS set later applies to new deletions only. A row deleted
// before the date was stored falls back to the setting.
func TestRsDeletedSiteKeepsPromisedPurgeDate(t *testing.T) {
	a := NewApp(t, edition.Default(fixture.Limits(t, map[string]string{"DELETED_RETENTION_DAYS": "30"})))
	ctx := context.Background()
	olive := a.NewPerson(t, "promised")
	for _, name := range []string{"kept", "legacy"} {
		a.Deploy(t, olive, name)
		if r := a.api(t, olive, "DELETE", "/v1/sites/"+name, nil); r.Status != http.StatusNoContent {
			t.Fatalf("delete %s: %d %s", name, r.Status, r.Body)
		}
	}
	id := func(name string) string {
		d, err := sitesrepo.GetDeletedSiteByUser(ctx, a.Database, olive.ID, name)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	kept, legacy := id("kept"), id("legacy")
	short := fixture.Limits(t, map[string]string{"DELETED_RETENTION_DAYS": "1"})
	if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET deleted_at = now() - interval '2 days' WHERE id IN ($1, $2)`, kept, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET purge_at = NULL WHERE id = $1`, legacy); err != nil {
		t.Fatal(err)
	}
	purgeable, err := sitesrepo.ListPurgeableSites(ctx, a.Database, short.DeletedRetention)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, d := range purgeable {
		found[d.ID] = true
	}
	if found[kept] || !found[legacy] {
		t.Fatalf("purgeable: kept %v (want false), legacy %v (want true)", found[kept], found[legacy])
	}
	d, err := sitesrepo.GetDeletedSiteByUser(ctx, a.Database, olive.ID, "kept")
	if want := time.Now().Add(30 * 24 * time.Hour); err != nil || !rsNear(d.PurgeAt(short.DeletedRetention), want) {
		t.Fatalf("kept purges at %v (%v), want the promised 30 days after deletion", d.PurgeAt(short.DeletedRetention), err)
	}
	if _, err := sitesrepo.PurgeDeletedSite(ctx, a.Database, kept, short.DeletedRetention); err == nil {
		t.Fatal("purged a site before its promised date")
	}
}

// A failing domain whose owner was emailed is disconnected on the date the
// email gave, not earlier when DOMAIN_LAPSE_HOURS is shortened afterwards.
func TestRsDomainLapseKeepsPromisedDate(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	olive := a.NewPerson(t, "lapsedate")
	id := a.Deploy(t, olive, "shop")
	domain := fixture.Uniq("shop") + ".example.test"
	if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET custom_domain = $2, domain_status = 'error', domain_verified_at = now() - interval '10 days',
		domain_failing_since = now() - interval '100 hours', domain_lapse_notified_at = now() - interval '76 hours',
		domain_release_at = now() + interval '1 hour' WHERE id = $1`, id, domain); err != nil {
		t.Fatal(err)
	}
	bd := model.BoundDomain{SiteID: id, Domain: domain, Status: "error", Verified: true}
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(ctx, bd, "error", "no answer", "")
	var cur *string
	if err := a.Database.QueryRowContext(ctx, `SELECT custom_domain FROM sites WHERE id = $1`, id).Scan(&cur); err != nil || cur == nil || *cur != domain {
		t.Fatalf("disconnected before the emailed date: %v %v", cur, err)
	}
	if _, err := a.Database.ExecContext(ctx, `UPDATE sites SET domain_release_at = now() - interval '1 minute' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	(domains.Handler{Handler: a.Sites.Handler}).ApplyDomainCheck(ctx, bd, "error", "no answer", "")
	if err := a.Database.QueryRowContext(ctx, `SELECT custom_domain FROM sites WHERE id = $1`, id).Scan(&cur); err != nil || cur != nil {
		t.Fatalf("still connected after the emailed date: %v %v", cur, err)
	}
}

// A site whose only recent view is its owner's own still counts as in use for
// the idle clean-up; one with no views at all does not.
func TestRsIdleOwnerViewCountsAsActivity(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	olive := a.NewPerson(t, "olive")
	quiet, mine := a.Deploy(t, olive, "quiet"), a.Deploy(t, olive, "mine")
	for _, s := range []string{quiet, mine} {
		for _, q := range []string{
			`UPDATE sites SET created_at = now() - interval '100 days', updated_at = now() - interval '100 days' WHERE id = $1`,
			`UPDATE versions SET created_at = now() - interval '100 days' WHERE site_id = $1`} {
			if _, err := a.Database.ExecContext(ctx, q, s); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := a.Database.ExecContext(ctx, `INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '7 days'), 'owner', 2) ON CONFLICT DO NOTHING`, mine); err != nil {
		t.Fatal(err)
	}
	idle, err := sitesrepo.ListIdleSitesToRemove(ctx, a.Database, model.IdleExempt{}, time.Now().AddDate(0, 0, -90), 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, s := range idle {
		found[s.SiteID] = true
	}
	if !found[quiet] || found[mine] {
		t.Fatalf("idle list: quiet %v (want true), owner-viewed %v (want false)", found[quiet], found[mine])
	}
}

// A stale disconnect (naming the domain the page showed) cannot remove the
// domain that has since replaced it; the current one disconnects.
func TestRsDomainDeleteNamesTheDomain(t *testing.T) {
	a := NewApp(t)
	olive := a.NewPerson(t, "olive")
	siteID := a.Deploy(t, olive, "shop")
	one, two := fixture.Uniq("one")+".example.test", fixture.Uniq("two")+".example.test"
	for _, d := range []string{one, two} {
		if r := a.bind(t, olive, "shop", d); r.Status != http.StatusOK {
			t.Fatalf("bind %s: %d %s", d, r.Status, r.Body)
		}
	}
	r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain?domain="+one, nil)
	if r.Status != http.StatusConflict || r.JSON(t)["code"] != "domain_changed" {
		t.Fatalf("delete stale domain: %d %s", r.Status, r.Body)
	}
	if got := a.domainInfo(t, siteID).Domain; got != two {
		t.Fatalf("domain changed by a refused delete: %q", got)
	}
	if r := a.api(t, olive, "DELETE", "/v1/sites/shop/domain?domain="+two, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete current: %d %s", r.Status, r.Body)
	}
	if _, err := os.Lstat(filepath.Join(a.Disk.DataDir(), "domains", two)); !os.IsNotExist(err) {
		t.Fatalf("disconnected domain still linked: %v", err)
	}
}

// Another account cannot bind, re-check or disconnect the owner's domain by
// naming the owner (?owner=) or the admin route; the owner's binding is unchanged.
func TestRsForeignAccountCannotManageDomain(t *testing.T) {
	a := NewApp(t)
	a.domainCertDir(t)
	olive, oscar := a.NewPerson(t, "olive"), a.NewPerson(t, "oscar")
	shopID := a.Deploy(t, olive, "shop")
	a.Deploy(t, oscar, "mine")
	dom := fixture.Uniq("shop") + ".example.test"
	if r := a.bind(t, olive, "shop", dom); r.Status != http.StatusOK {
		t.Fatalf("bind: %d %s", r.Status, r.Body)
	}
	other := fixture.Uniq("evil") + ".example.test"
	for _, tc := range []struct {
		name, method, path string
		body               any
	}{
		{"bind", "POST", "/v1/sites/shop/domain?owner=" + olive.Handle, map[string]string{"domain": other}},
		{"check", "POST", "/v1/sites/shop/domain/check?owner=" + olive.Handle, nil},
		{"disconnect", "DELETE", "/v1/sites/shop/domain?owner=" + olive.Handle, nil},
		{"admin-bind", "POST", "/v1/admin/sites/" + shopID + "/domain", map[string]string{"domain": other}},
		{"admin-disconnect", "DELETE", "/v1/admin/sites/" + shopID + "/domain", nil},
	} {
		if r := a.api(t, oscar, tc.method, tc.path, tc.body); r.Status/100 == 2 {
			t.Fatalf("%s by another account: %d %s", tc.name, r.Status, r.Body)
		}
		if info := a.domainInfo(t, shopID); info.Domain != dom {
			t.Fatalf("%s changed the owner's domain to %q", tc.name, info.Domain)
		}
	}
	// Without a selector the same calls act on oscar's own site, never olive's.
	if r := a.api(t, oscar, "DELETE", "/v1/sites/shop/domain", nil); r.Status/100 == 2 {
		t.Fatalf("disconnect of a site oscar does not have: %d %s", r.Status, r.Body)
	}
	if info := a.domainInfo(t, shopID); info.Domain != dom {
		t.Fatalf("owner's domain is now %q", info.Domain)
	}
}
