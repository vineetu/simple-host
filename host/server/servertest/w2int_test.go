package servertest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// How the batch-2 features meet: the idle-site cleanup against the offline
// switch, preview sites, renames (Keep flag, old-name redirects) and the
// Recently deleted purge. Needs DB_DSN.
func TestIdleCleanupMeetsOfflineRenameAndPreview(t *testing.T) {
	a, dir := NewSiteApp(t, "canonical")
	ctx := context.Background()
	a.Sites.SetIdleCleanup(true)

	olive := a.NewPerson(t, "olive")
	uid, oh := a.UserID(t, olive)
	MarkReady(t, dir, oh)
	apex := PcSiteDomain
	person := oh + "." + PcSiteDomain
	okey := map[string]string{"X-API-Key": olive.Key}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.Database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Keep this test's old sites out of later runs, whatever happens.
	t.Cleanup(func() { _, _ = a.Database.Exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid) })

	names := []string{"offsite", "prevsite", "warnoff", "oldname", "keepme"}
	ids := map[string]string{}
	for _, n := range names {
		a.Deploy(t, olive, n)
		ids[n] = a.SiteID(t, olive, n)
		exec(`UPDATE sites SET created_at = now() - interval '130 days', updated_at = now() - interval '130 days' WHERE id = $1`, ids[n])
		exec(`UPDATE versions SET created_at = now() - interval '130 days' WHERE site_id = $1`, ids[n])
	}
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, ids["keepme"])
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('w2int-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)

	// Offline: the owner acted on it on purpose, so it is never idle.
	if r := a.At(t, "PATCH", apex, "/v1/sites/offsite", map[string]bool{"offline": true}, okey); r.Status != 200 {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	// A preview site (PREVIEW_ACCOUNTS) expires on its own.
	exec(`UPDATE sites SET expires_at = now() + interval '1 day' WHERE id = $1`, ids["prevsite"])
	// The Keep flag is on the site, not its name: it survives a rename.
	if r := a.At(t, "PUT", apex, "/v1/sites/keepme/keep", map[string]any{"keep": true}, okey); r.Status != 200 {
		t.Fatalf("keep: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "PATCH", apex, "/v1/sites/keepme", map[string]string{"name": "keptnew"}, okey); r.Status != 200 {
		t.Fatalf("rename kept: %d %s", r.Status, r.Body)
	}
	var keepFlag bool
	if err := a.Database.QueryRow(`SELECT idle_keep FROM sites WHERE id = $1 AND name = 'keptnew'`, ids["keepme"]).Scan(&keepFlag); err != nil || !keepFlag {
		t.Fatalf("keep flag lost in the rename: %v %v", keepFlag, err)
	}

	eligible := func() map[string]bool {
		t.Helper()
		list, err := sitesrepo.ListIdleSitesToRemove(ctx, a.Database, model.IdleExempt{}, time.Now().Add(-120*24*time.Hour), 0)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, s := range list {
			if s.UserID == uid {
				out[s.Name] = true
			}
		}
		return out
	}
	if got := eligible(); !got["warnoff"] || !got["oldname"] || got["offsite"] || got["prevsite"] || got["keptnew"] {
		t.Fatalf("idle candidates: %v", got)
	}

	// Renamed: the owner acting on a site counts as activity; backdated here
	// so it is idle again, under the site's current name.
	if r := a.At(t, "PATCH", apex, "/v1/sites/oldname", map[string]string{"name": "newname"}, okey); r.Status != 200 {
		t.Fatalf("rename: %d %s", r.Status, r.Body)
	}
	exec(`UPDATE sites SET updated_at = now() - interval '130 days' WHERE id = $1`, ids["oldname"])
	// The cleanup removes it, and a site taken offline after being idle stays.
	if r := a.At(t, "PATCH", apex, "/v1/sites/warnoff", map[string]bool{"offline": true}, okey); r.Status != 200 {
		t.Fatalf("offline: %d %s", r.Status, r.Body)
	}
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, ids["keepme"])
	a.Sites.RunIdleCleanup(ctx, time.Now())
	var offDeleted, renamedDeleted bool
	if err := a.Database.QueryRow(`SELECT (SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1), (SELECT deleted_at IS NOT NULL FROM sites WHERE id = $2)`, ids["warnoff"], ids["oldname"]).Scan(&offDeleted, &renamedDeleted); err != nil {
		t.Fatal(err)
	}
	if offDeleted || !renamedDeleted {
		t.Fatalf("offline site deleted=%v, renamed idle site deleted=%v", offDeleted, renamedDeleted)
	}

	// In Recently deleted its old name stops redirecting; the Restore link
	// brings the site and the redirect back.
	oldHost := "oldname." + person
	if r := a.At(t, "GET", oldHost, "/a?x=1", nil, nil); r.Status == http.StatusFound {
		t.Fatalf("old name of a removed site still redirects to %q", r.Header.Get("Location"))
	}
	if r := a.At(t, "POST", apex, "/v1/sites/newname/restore", nil, okey); r.Status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", oldHost, "/a?x=1", nil, nil); r.Status != http.StatusFound || r.Header.Get("Location") != "https://newname."+person+"/a?x=1" {
		t.Fatalf("old name after restore: %d %q", r.Status, r.Header.Get("Location"))
	}

	// Purged from Recently deleted: its old names go with it.
	if r := a.At(t, "DELETE", apex, "/v1/sites/newname", nil, okey); r.Status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.Status, r.Body)
	}
	exec(`UPDATE sites SET deleted_at = now() - interval '8 days', purge_at = now() - interval '1 day' WHERE id = $1`, ids["oldname"])
	a.Sites.PurgeDeletedSites(ctx)
	var aliases int
	if err := a.Database.QueryRow(`SELECT count(*) FROM site_name_aliases WHERE site_id = $1`, ids["oldname"]).Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("old names left after purge: %d (%v)", aliases, err)
	}
	if r := a.At(t, "GET", oldHost, "/", nil, nil); r.Status == http.StatusFound {
		t.Fatalf("purged site's old name still redirects")
	}
}

// A sign-in alert goes to the account's current address: after a sign-in
// email change, to the new one, and never to the old.
func TestSignInAlertFollowsEmailChange(t *testing.T) {
	a, mb := NewAccountApp(t)
	p := a.NewPerson(t, "switcher")
	newAddr := Uniq("switched") + "@example.org"
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.Key}

	code, cur := RequestEmailChange(t, a, mb, key, p.Email, newAddr)
	if r := a.At(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code, "current_code": cur}, key); r.Status != 200 {
		t.Fatalf("verify: %d %s", r.Status, r.Body)
	}
	before := len(mb.To(p.Email))

	SignInWith(t, a, mb, newAddr, UaMacChrome)
	alerts := 0
	for _, n := range mb.To(newAddr) {
		if strings.Contains(n, "New sign-in") {
			alerts++
		}
	}
	if alerts != 1 {
		t.Fatalf("alerts to the new address: %d (%v)", alerts, mb.To(newAddr))
	}
	if n := len(mb.To(p.Email)); n != before {
		t.Fatalf("old address got %d more notice(s) after the change", n-before)
	}
}
