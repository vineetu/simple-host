package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	rt "github.com/vsriram/simple-host/host/server/servertest/routetest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// The idle clean-up: a site unused for IDLE_AFTER_DAYS + IDLE_GRACE_DAYS
// (120 days) moves to Recently deleted; one that is younger, visited (the
// owner's own visits count), marked Keep, restored, owned by an exempt
// account or with its own name stays; and nothing happens while it is off.
func TestIdleSiteCleanup(t *testing.T) {
	a := NewApp(t)
	ctx := context.Background()
	a.Sites.SetIdleCleanup(true)
	olive := a.NewPerson(t, "olive")
	t.Cleanup(func() { _, _ = a.Database.Exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, olive.ID) })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.Database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	old := func(site string, days int) string {
		t.Helper()
		id := a.Deploy(t, olive, site)
		exec(`UPDATE sites SET created_at = now() - make_interval(days => $2), updated_at = now() - make_interval(days => $2) WHERE id = $1`, id, days)
		exec(`UPDATE versions SET created_at = now() - make_interval(days => $2) WHERE site_id = $1`, id, days)
		return id
	}
	quiet, young, kept, visited, owned, listed, named := old("quiet", 130), old("young", 100), old("kept", 130), old("visited", 130), old("owned", 130), old("listed", 130), old("named", 130)
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '7 days'), 'person', 3) ON CONFLICT DO NOTHING`, visited)
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '7 days'), 'owner', 2) ON CONFLICT DO NOTHING`, owned)
	exec(`INSERT INTO site_view_hourly (site_id, hour, class, views) VALUES ($1, date_trunc('hour', now() - interval '200 days'), 'person', 1) ON CONFLICT DO NOTHING`, quiet)
	exec(`INSERT INTO collection_items (site_id, collection, data, created_at) VALUES ($1, 'rsvp', '{}', now() - interval '3 days')`, listed)
	exec(`INSERT INTO analytics_ingest_state (logfile, updated_at) VALUES ('idle-test', now()) ON CONFLICT (logfile) DO UPDATE SET updated_at = now()`)
	if r := a.api(t, olive, "PUT", "/v1/sites/kept/keep", map[string]any{"keep": true}); r.Status != http.StatusOK {
		t.Fatalf("keep flag: %d %s", r.Status, r.Body)
	}
	if r := a.api(t, olive, "POST", "/v1/sites/named/domain", map[string]any{"domain": uniq("idlename") + "." + rt.SiteDomain}); r.Status != http.StatusOK {
		t.Fatalf("claim name: %d %s", r.Status, r.Body)
	}

	// The admin dry run lists only quiet, and only for the admin.
	r := a.admin(t, "GET", "/v1/admin/idle-sites", nil)
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"site":"quiet"`) {
		t.Fatalf("dry run: %d %s", r.Status, r.Body)
	}
	for _, other := range []string{"young", "kept", "visited", "owned", "listed", "named"} {
		if strings.Contains(string(r.Body), `"site":"`+other+`"`) {
			t.Fatalf("dry run lists %s: %s", other, r.Body)
		}
	}
	if r := a.api(t, olive, "GET", "/v1/admin/idle-sites", nil); r.Status != http.StatusNotFound {
		t.Fatalf("dry run as a non-admin: %d", r.Status)
	}

	deleted := func(id string) bool {
		t.Helper()
		var d bool
		if err := a.Database.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL FROM sites WHERE id = $1`, id).Scan(&d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	a.Sites.SetIdleCleanup(false)
	a.Sites.RunIdleCleanup(ctx, time.Now())
	if deleted(quiet) {
		t.Fatal("ran while off")
	}
	a.Sites.SetIdleCleanup(true)
	a.Sites.RunIdleCleanup(ctx, time.Now())
	if !deleted(quiet) {
		t.Fatal("idle site not moved to Recently deleted")
	}
	for name, id := range map[string]string{"young": young, "kept": kept, "visited": visited, "owned": owned, "listed": listed, "named": named} {
		if deleted(id) {
			t.Fatalf("%s was removed", name)
		}
	}
	// Restoring restarts the clock.
	if r := a.api(t, olive, "POST", "/v1/sites/quiet/restore", nil); r.Status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	a.Sites.RunIdleCleanup(ctx, time.Now())
	if deleted(quiet) {
		t.Fatal("restored site removed again")
	}

	// An exempt handle, the reviewer account and an event account are never
	// listed; a key merely named like the event key exempts nothing.
	exec(`UPDATE sites SET idle_kept_at = NULL, created_at = now() - interval '130 days', updated_at = now() - interval '130 days' WHERE id = $1`, quiet)
	mine := func() int {
		t.Helper()
		list, err := sitesrepo.ListIdleSitesToRemove(ctx, a.Database, a.Sites.IdleExempt, time.Now().Add(-120*24*time.Hour), 0)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, s := range list {
			if s.UserID == olive.ID {
				n++
			}
		}
		return n
	}
	if mine() == 0 {
		t.Fatal("setup: nothing idle")
	}
	a.Sites.SetIdleExempt([]string{" Other ", strings.ToUpper(olive.Handle)}, "")
	if mine() != 0 {
		t.Fatal("exempt handle listed")
	}
	a.Sites.SetIdleExempt(nil, strings.ToUpper(olive.Email))
	if mine() != 0 {
		t.Fatal("reviewer account listed")
	}
	a.Sites.SetIdleExempt(nil, "")
	exec(`UPDATE api_keys SET name = $2 WHERE user_id = $1`, olive.ID, model.KeyNameEvent)
	if mine() == 0 {
		t.Fatal("a key named like the event key exempted the account")
	}
	exec(`UPDATE users SET event_account = TRUE WHERE id = $1`, olive.ID)
	if mine() != 0 {
		t.Fatal("event account listed")
	}
}
