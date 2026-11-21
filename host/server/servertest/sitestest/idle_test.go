package sitestest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/server/servertest"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
)

// The idle clean-up with nobody emailed: a site unused for IDLE_AFTER_DAYS +
// IDLE_GRACE_DAYS (120 days) moves to Recently deleted; one that is younger,
// visited, marked Keep, restored, owned by an exempt account or with its own
// name stays; and nothing happens while the clean-up is off.
func TestIdleSiteCleanup(t *testing.T) {
	a := servertest.NewPersonApp(t, "canonical")
	ctx := context.Background()
	a.Sites.SetIdleCleanup(true)

	olive := a.NewPerson(t, "olive")
	okey := map[string]string{"X-API-Key": olive.Key}
	uid, handle := a.UserID(t, olive)
	t.Cleanup(func() { _, _ = a.Database.Exec(`UPDATE sites SET idle_keep = true WHERE user_id = $1`, uid) })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.Database.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	old := func(site string, days int) string {
		t.Helper()
		a.Deploy(t, olive, site)
		id := a.SiteID(t, olive, site)
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
	if r := a.At(t, "PUT", "simple-host.test", "/v1/sites/kept/keep", map[string]any{"keep": true}, okey); r.Status != http.StatusOK {
		t.Fatalf("keep flag: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "simple-host.test", "/v1/sites/named/domain", map[string]any{"domain": servertest.Uniq("idlename") + "." + servertest.PcSiteDomain}, okey); r.Status != http.StatusOK {
		t.Fatalf("claim name: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "simple-host.test", "/v1/sites", nil, okey); !strings.Contains(string(r.Body), `"keep":true`) || strings.Contains(string(r.Body), "idle_removal_at") {
		t.Fatalf("site list: %s", r.Body)
	}

	// The admin dry run lists only quiet.
	r := a.At(t, "GET", "simple-host.test", "/v1/admin/idle-sites", nil, map[string]string{"X-API-Key": a.Admin})
	for _, other := range []string{"young", "kept", "visited", "listed", "named"} {
		if strings.Contains(string(r.Body), `"site":"`+other+`"`) {
			t.Fatalf("dry run lists %s: %s", other, r.Body)
		}
	}
	if r.Status != http.StatusOK || !strings.Contains(string(r.Body), `"site":"quiet"`) {
		t.Fatalf("dry run: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "GET", "simple-host.test", "/v1/admin/idle-sites", nil, okey); r.Status != http.StatusNotFound {
		t.Fatalf("dry run as a non-admin: %d", r.Status)
	}

	// Off: nothing happens.
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
	for name, id := range map[string]string{"young": young, "kept": kept, "visited": visited, "listed": listed, "named": named} {
		if deleted(id) {
			t.Fatalf("%s was removed", name)
		}
	}

	// Restoring restarts the clock.
	if r := a.At(t, "POST", "simple-host.test", "/v1/sites/quiet/restore", nil, okey); r.Status != http.StatusOK {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	a.Sites.RunIdleCleanup(ctx, time.Now())
	if deleted(quiet) {
		t.Fatal("restored site removed again")
	}

	// Exempt handle, reviewer account and event account are never listed.
	exec(`UPDATE sites SET idle_kept_at = NULL, created_at = now() - interval '130 days', updated_at = now() - interval '130 days' WHERE id = $1`, quiet)
	exec(`UPDATE sites SET idle_keep = false WHERE id = $1`, kept)
	mine := func() int {
		t.Helper()
		list, err := sitesrepo.ListIdleSitesToRemove(ctx, a.Database, a.Sites.IdleExempt, time.Now().Add(-120*24*time.Hour), 0)
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
	a.Sites.SetIdleExempt([]string{" Other ", strings.ToUpper(handle)}, "")
	if mine() != 0 {
		t.Fatal("exempt handle listed")
	}
	a.Sites.SetIdleExempt(nil, strings.ToUpper(olive.Email))
	if mine() != 0 {
		t.Fatal("reviewer account listed")
	}
	a.Sites.SetIdleExempt(nil, "")
	exec(`UPDATE api_keys SET name = $2 WHERE user_id = $1`, uid, model.KeyNameEvent)
	if mine() == 0 {
		t.Fatal("a key named like the event key exempted the account")
	}
	exec(`UPDATE users SET event_account = TRUE WHERE id = $1`, uid)
	if mine() != 0 {
		t.Fatal("event account listed")
	}
}
