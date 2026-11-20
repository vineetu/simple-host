package handler

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// The idle clean-up with nobody emailed: a site unused for IDLE_AFTER_DAYS +
// IDLE_GRACE_DAYS (120 days) moves to Recently deleted; one that is younger,
// visited, marked Keep, restored, owned by an exempt account or with its own
// name stays; and nothing happens while the clean-up is off.
func TestIdleSiteCleanup(t *testing.T) {
	a := newPersonApp(t, "canonical")
	ctx := context.Background()
	a.sites.SetIdleCleanup(true)

	olive := a.newPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.key}
	uid, handle := a.userID(t, olive)
	t.Cleanup(func() { _, _ = a.database.Exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid) })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	old := func(site string, days int) string {
		t.Helper()
		a.deploy(t, olive, site)
		id := a.siteID(t, olive, site)
		exec(`UPDATE sites SET created_at = now() - make_interval(days => $2), updated_at = now() - make_interval(days => $2) WHERE id = $1`, id, days)
		exec(`UPDATE versions SET created_at = now() - make_interval(days => $2) WHERE site_id = $1`, id, days)
		return id
	}
	quiet, young, kept, visited, listed, named := old("quiet", 130), old("young", 100), old("kept", 130), old("visited", 130), old("listed", 130), old("named", 130)

	// Visit records reach back 200 days and the ingester ran just now.
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, visited)
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '7 days'), 'person', 3) ON CONFLICT DO NOTHING`, visited)
	exec(`INSERT INTO collection_items (site_id, collection, data, created_at) VALUES ($1, 'rsvp', '{}', now() - interval '3 days')`, listed)
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)
	if r := a.at(t, "PUT", "simple-host.test", "/v1/sites/kept/keep", map[string]any{"keep": true}, okey); r.status != http.StatusOK {
		t.Fatalf("keep flag: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/named/domain", map[string]any{"domain": uniq("idlename") + "." + pcSiteDomain}, okey); r.status != http.StatusOK {
		t.Fatalf("claim name: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "simple-host.test", "/v1/sites", nil, okey); !strings.Contains(string(r.body), `"keep":true`) || strings.Contains(string(r.body), "idle_removal_at") {
		t.Fatalf("site list: %s", r.body)
	}

	// The admin dry run lists only quiet.
	r := a.at(t, "GET", "simple-host.test", "/v1/admin/idle-sites", nil, map[string]string{"X-API-Key": a.admin})
	for _, other := range []string{"young", "kept", "visited", "listed", "named"} {
		if strings.Contains(string(r.body), `"site":"`+other+`"`) {
			t.Fatalf("dry run lists %s: %s", other, r.body)
		}
	}
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"site":"quiet"`) {
		t.Fatalf("dry run: %d %s", r.status, r.body)
	}
	if r := a.at(t, "GET", "simple-host.test", "/v1/admin/idle-sites", nil, okey); r.status != http.StatusNotFound {
		t.Fatalf("dry run as a non-admin: %d", r.status)
	}

	// Off: nothing happens.
	deleted := func(id string) bool {
		t.Helper()
		var d bool
		if err := a.database.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, id).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	a.sites.SetIdleCleanup(false)
	a.sites.runIdleCleanup(ctx, time.Now())
	if deleted(quiet) {
		t.Fatal("ran while off")
	}
	a.sites.SetIdleCleanup(true)
	a.sites.runIdleCleanup(ctx, time.Now())
	if !deleted(quiet) {
		t.Fatal("idle site not moved to Recently deleted")
	}
	for name, id := range map[string]string{"young": young, "kept": kept, "visited": visited, "listed": listed, "named": named} {
		if deleted(id) {
			t.Fatalf("%s was removed", name)
		}
	}

	// Restoring restarts the clock.
	if r := a.at(t, "POST", "simple-host.test", "/v1/sites/quiet/restore", nil, okey); r.status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.status, r.body)
	}
	a.sites.runIdleCleanup(ctx, time.Now())
	if deleted(quiet) {
		t.Fatal("restored site removed again")
	}

	// Exempt handle, reviewer account and event account are never listed.
	exec(`UPDATE sites SET idle_kept_at = NULL, created_at = now() - interval '130 days', updated_at = now() - interval '130 days' WHERE id = $1`, quiet)
	exec(`UPDATE sites SET idle_keep = false WHERE id = $1`, kept)
	mine := func() int {
		t.Helper()
		list, err := sitesrepo.ListIdleSitesToRemove(ctx, a.database, a.sites.idleExempt, time.Now().Add(-120*24*time.Hour), 0)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range list {
			if s.UserID == uid {
				n++
			}
		}
		return n
	}
	if mine() == 0 {
		t.Fatal("setup: nothing idle")
	}
	a.sites.SetIdleExempt([]string{" Other ", strings.ToUpper(handle)}, "")
	if mine() != 0 {
		t.Fatal("exempt handle listed")
	}
	a.sites.SetIdleExempt(nil, strings.ToUpper(olive.email))
	if mine() != 0 {
		t.Fatal("reviewer account listed")
	}
	a.sites.SetIdleExempt(nil, "")
	exec(`UPDATE api_keys SET name = $2 WHERE user_id = $1`, uid, model.KeyNameEvent)
	if mine() == 0 {
		t.Fatal("a key named like the event key exempted the account")
	}
	exec(`UPDATE users SET event_account = TRUE WHERE id = $1`, uid)
	if mine() != 0 {
		t.Fatal("event account listed")
	}
}
