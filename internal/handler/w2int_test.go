package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	db "github.com/vsriram/simple-host/internal/db"
)

// How the batch-2 features meet: the idle-site cleanup against the offline
// switch, preview sites, renames (Keep flag, old-name redirects) and the
// Recently deleted purge. Needs DB_DSN.
func TestIdleCleanupMeetsOfflineRenameAndPreview(t *testing.T) {
	a, dir := newSiteApp(t, "canonical")
	ctx := context.Background()
	m := &replyMailer{}
	a.sites.mailer = m
	a.sites.SetIdleCleanup(true, 50)

	olive := a.newPerson(t, "olive")
	uid, oh := a.userID(t, olive)
	markReady(t, dir, oh)
	apex := pcSiteDomain
	person := oh + "." + pcSiteDomain
	okey := map[string]string{"X-API-Key": olive.key}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Keep this test's old sites out of later runs, whatever happens.
	t.Cleanup(func() { _, _ = a.database.Exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid) })

	names := []string{"offsite", "prevsite", "warnoff", "oldname", "keepme"}
	ids := map[string]string{}
	for _, n := range names {
		a.deploy(t, olive, n)
		ids[n] = a.siteID(t, olive, n)
		exec(`UPDATE sites SET created_at = now() - interval '100 days', updated_at = now() - interval '100 days' WHERE id = $1`, ids[n])
		exec(`UPDATE versions SET created_at = now() - interval '100 days' WHERE site_id = $1`, ids[n])
	}
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, ids["keepme"])
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('w2int-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)

	// Offline: the owner acted on it on purpose, so it is never idle.
	if r := a.at(t, "PATCH", apex, "/v1/sites/offsite", map[string]bool{"offline": true}, okey); r.status != 200 {
		t.Fatalf("offline: %d %s", r.status, r.body)
	}
	// A preview site (PREVIEW_ACCOUNTS) expires on its own.
	exec(`UPDATE sites SET expires_at = now() + interval '1 day' WHERE id = $1`, ids["prevsite"])
	// The Keep flag is on the site, not its name: it survives a rename.
	if r := a.at(t, "PUT", apex, "/v1/sites/keepme/keep", map[string]any{"keep": true}, okey); r.status != 200 {
		t.Fatalf("keep: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", apex, "/v1/sites/keepme", map[string]string{"name": "keptnew"}, okey); r.status != 200 {
		t.Fatalf("rename kept: %d %s", r.status, r.body)
	}
	var keepFlag bool
	if err := a.database.QueryRow(`SELECT idle_keep FROM sites WHERE id = $1 AND name = 'keptnew'`, ids["keepme"]).Scan(&keepFlag); err != nil || !keepFlag {
		t.Fatalf("keep flag lost in the rename: %v %v", keepFlag, err)
	}

	eligible := func() map[string]bool {
		t.Helper()
		list, err := db.ListIdleSitesToWarn(ctx, a.database, db.IdleExempt{}, time.Now().Add(-idleAfter), 0)
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

	a.sites.runIdleCleanup(ctx, time.Now())
	if m.last("warnoff") == "" || m.last("oldname") == "" || m.last("offsite") != "" || m.last("prevsite") != "" || m.last("keptnew") != "" {
		t.Fatalf("warnings: %v", m.sent)
	}

	// Taken offline after the warning: the warning is dropped and the site
	// is never removed.
	if r := a.at(t, "PATCH", apex, "/v1/sites/warnoff", map[string]bool{"offline": true}, okey); r.status != 200 {
		t.Fatalf("offline after warning: %d %s", r.status, r.body)
	}
	// Renamed after the warning: the owner acting on a site counts as
	// activity; backdated here so the removal still comes, under the site's
	// current name.
	if r := a.at(t, "PATCH", apex, "/v1/sites/oldname", map[string]string{"name": "newname"}, okey); r.status != 200 {
		t.Fatalf("rename warned: %d %s", r.status, r.body)
	}
	exec(`UPDATE sites SET idle_warned_at = now() - interval '31 days', updated_at = now() - interval '100 days' WHERE id = ANY($1::uuid[])`, "{"+ids["warnoff"]+","+ids["oldname"]+"}")
	a.sites.runIdleCleanup(ctx, time.Now())
	var warned, deleted bool
	if err := a.database.QueryRow(`SELECT idle_warned_at IS NOT NULL, deleted_at IS NOT NULL FROM sites WHERE id = $1`, ids["warnoff"]).Scan(&warned, &deleted); err != nil {
		t.Fatal(err)
	}
	if warned || deleted {
		t.Fatalf("offline site: warned=%v deleted=%v", warned, deleted)
	}
	gone := m.last("newname")
	if !strings.Contains(gone, "moved to Recently deleted") {
		t.Fatalf("renamed idle site not removed: %v", m.sent)
	}

	// In Recently deleted its old name stops redirecting; the Restore link
	// brings the site and the redirect back.
	oldHost := "oldname." + person
	if r := a.at(t, "GET", oldHost, "/a?x=1", nil, nil); r.status == http.StatusFound {
		t.Fatalf("old name of a removed site still redirects to %q", r.header.Get("Location"))
	}
	restore := idleLinks(gone)["restore"]
	if r := idleAct(t, a, restore); r.status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", oldHost, "/a?x=1", nil, nil); r.status != http.StatusFound || r.header.Get("Location") != "https://newname."+person+"/a?x=1" {
		t.Fatalf("old name after restore: %d %q", r.status, r.header.Get("Location"))
	}

	// Purged from Recently deleted: its old names go with it.
	if r := a.at(t, "DELETE", apex, "/v1/sites/newname", nil, okey); r.status != http.StatusNoContent {
		t.Fatalf("delete: %d %s", r.status, r.body)
	}
	exec(`UPDATE sites SET deleted_at = now() - interval '8 days' WHERE id = $1`, ids["oldname"])
	a.sites.purgeDeletedSites(ctx)
	var aliases int
	if err := a.database.QueryRow(`SELECT count(*) FROM site_name_aliases WHERE site_id = $1`, ids["oldname"]).Scan(&aliases); err != nil || aliases != 0 {
		t.Fatalf("old names left after purge: %d (%v)", aliases, err)
	}
	if r := a.at(t, "GET", oldHost, "/", nil, nil); r.status == http.StatusFound {
		t.Fatalf("purged site's old name still redirects")
	}
}

// A sign-in alert goes to the account's current address: after a sign-in
// email change, to the new one, and never to the old.
func TestSignInAlertFollowsEmailChange(t *testing.T) {
	a, mb := newAccountApp(t)
	p := a.newPerson(t, "switcher")
	newAddr := uniq("switched") + "@example.org"
	host := "simple-host.test"
	key := map[string]string{"X-API-Key": p.key}

	if r := a.at(t, "POST", host, "/v1/me/email", map[string]string{"email": newAddr}, key); r.status != 202 {
		t.Fatalf("request change: %d %s", r.status, r.body)
	}
	got := mb.to(newAddr)
	if len(got) != 1 {
		t.Fatalf("code notice: %v", got)
	}
	code := confirmCodeRe.FindStringSubmatch(got[0])
	if code == nil {
		t.Fatalf("no code in %q", got[0])
	}
	if r := a.at(t, "POST", host, "/v1/me/email/verify", map[string]string{"code": code[1]}, key); r.status != 200 {
		t.Fatalf("verify: %d %s", r.status, r.body)
	}
	before := len(mb.to(p.email))

	signInWith(t, a, mb, newAddr, uaMacChrome)
	alerts := 0
	for _, n := range mb.to(newAddr) {
		if strings.Contains(n, "New sign-in") {
			alerts++
		}
	}
	if alerts != 1 {
		t.Fatalf("alerts to the new address: %d (%v)", alerts, mb.to(newAddr))
	}
	if n := len(mb.to(p.email)); n != before {
		t.Fatalf("old address got %d more notice(s) after the change", n-before)
	}
}
