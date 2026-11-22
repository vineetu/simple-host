package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// Archive cleanup (M4) and the starter rubric seeded when an event is created.

// cancelCleanupMailer models provider acceptance racing worker shutdown.
type cancelCleanupMailer struct {
	*hacktest.ReplyMailer
	cancel context.CancelFunc
}

func (m *cancelCleanupMailer) SendNoticeReplyToCtx(ctx context.Context, to, replyTo, subject, text string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.cancel()
	return m.SendNoticeReplyTo(to, replyTo, subject, text)
}

func TestHackAdminKeepSites(t *testing.T) {
	a := newHackApp(t)
	org := a.NewPerson(t, "kps")
	slug := hacktest.UniqueSlug()
	if r := a.CreateEvent(t, org, slug, nil); r.Status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	path := "/v1/admin/hack/events/" + slug + "/keep-sites"
	if r := a.At(t, "POST", path, map[string]any{"keep": true}, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", path, map[string]any{"keep": true}, a.Key(org)); r.Status != http.StatusNotFound {
		t.Fatalf("organiser: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", path, map[string]any{}, a.AdminH()); r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_request" {
		t.Fatalf("missing keep: %d %s", r.Status, r.Body)
	}
	if r := a.At(t, "POST", "/v1/admin/hack/events/no-such-event/keep-sites", map[string]any{"keep": true}, a.AdminH()); r.Status != http.StatusNotFound {
		t.Fatalf("missing event: %d %s", r.Status, r.Body)
	}

	for _, keep := range []bool{true, false} {
		r := a.At(t, "POST", path, map[string]any{"keep": keep}, a.AdminH())
		if r.Status != http.StatusOK {
			t.Fatalf("keep %v: %d %s", keep, r.Status, r.Body)
		}
		var body map[string]any
		if err := json.Unmarshal(r.Body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["keep_sites"] != keep {
			t.Fatalf("keep %v response: %s", keep, r.Body)
		}
		var stored bool
		if err := a.DB.QueryRow(`SELECT keep_sites FROM events WHERE slug = $1`, slug).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != keep {
			t.Fatalf("stored keep_sites %v, want %v", stored, keep)
		}
	}
}

// cleanupSites records TrashTeamSite. fail maps a team slug to an error.
type cleanupSites struct {
	hacktest.SiteStub
	mu    sync.Mutex
	calls []siteCall
	fail  map[string]error
}

type siteCall struct{ account, slug string }

func (s *cleanupSites) TrashTeamSite(_ context.Context, accountID, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, siteCall{accountID, name})
	if err := s.fail[name]; err != nil {
		return err
	}
	return nil
}

func (s *cleanupSites) called() []siteCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]siteCall(nil), s.calls...)
}

func (a *hackApp) makeCleanupEvent(t *testing.T, org hacktest.Person, title, contact string, nTeams int) (string, []string) {
	t.Helper()
	slug := hacktest.UniqueSlug()
	extra := map[string]any{"title": title}
	if contact != "" {
		extra["contact_email"] = contact
	}
	if r := a.CreateEvent(t, org, slug, extra); r.Status != http.StatusCreated {
		t.Fatalf("create %s: %d %s", title, r.Status, r.Body)
	}
	if nTeams == 0 {
		return slug, nil
	}
	a.OpenEvent(t, org, slug)
	join := a.At(t, "GET", "/v1/hack/events/"+slug, nil, a.Key(org)).JSON(t)["organiser"].(map[string]any)["join_code"].(string)
	teams := make([]string, 0, nTeams)
	for i := 0; i < nTeams; i++ {
		name := fmt.Sprintf("Crew %d", i+1)
		p := a.NewPerson(t, "ct")
		if r := a.At(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": name}, a.Key(p)); r.Status != http.StatusOK {
			t.Fatalf("join %s: %d %s", name, r.Status, r.Body)
		}
		if r := a.At(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.Key(p)); r.Status != http.StatusCreated {
			t.Fatalf("team %s: %d %s", name, r.Status, r.Body)
		}
		var teamSlug string
		if err := a.DB.QueryRow(`
			SELECT t.slug FROM event_teams t JOIN events e ON e.id = t.event_id
			 WHERE e.slug = $1 AND t.name = $2`, slug, name).Scan(&teamSlug); err != nil {
			t.Fatal(err)
		}
		teams = append(teams, teamSlug)
	}
	return slug, teams
}

func (a *hackApp) setEventClock(t *testing.T, slug, stage string, closed time.Time, keep bool) {
	t.Helper()
	res, err := a.DB.Exec(`UPDATE events SET stage = $2, closed_at = $3, keep_sites = $4 WHERE slug = $1`, slug, stage, closed, keep)
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		t.Fatalf("clock %s: %d %v", slug, n, err)
	}
}

func (a *hackApp) eventStamps(t *testing.T, slug string) (sql.NullTime, sql.NullTime) {
	t.Helper()
	var warned, removed sql.NullTime
	if err := a.DB.QueryRow(`SELECT removal_warned_at, sites_removed_at FROM events WHERE slug = $1`, slug).Scan(&warned, &removed); err != nil {
		t.Fatal(err)
	}
	return warned, removed
}

// eventRest is every column the sweep must not write, including updated_at.
func (a *hackApp) eventRest(t *testing.T, slug string) string {
	t.Helper()
	var s string
	err := a.DB.QueryRow(`
		SELECT concat_ws(E'\n',
			stage, title, organiser_name, organisation, contact_email, purpose,
			coalesce(tagline, ''), coalesce(about, ''), coalesce(rules, ''), coalesce(prizes, ''),
			keep_sites::text, coalesce(closed_at::text, ''), coalesce(taken_down_reason, ''),
			results_visibility, gallery_open::text, judge_assignment_mode, judges_per_team::text,
			coalesce(judging_lock_reason, ''), team_size_max::text, join_code, judge_code,
			updated_at::text, coalesce(results_published_at::text, ''), coalesce(submission_deadline::text, ''))
		  FROM events WHERE slug = $1`, slug).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (a *hackApp) resultsBlob(t *testing.T, slug string) string {
	t.Helper()
	var s string
	err := a.DB.QueryRow(`
		SELECT full_ranking::text || '|' || snapshot::text || '|' || coalesce(published_by::text, '') || '|' || published_at::text
		  FROM event_results r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (a *hackApp) rubricBlob(t *testing.T, slug string) string {
	t.Helper()
	rows, err := a.DB.Query(`
		SELECT c.id::text || '|' || c.position::text || '|' || c.name || '|' || c.description || '|' || c.weight::text || '|' || c.max_points::text
		  FROM rubric_criteria c JOIN events e ON e.id = c.event_id
		 WHERE e.slug = $1 ORDER BY c.position, c.id`, slug)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func (a *hackApp) wantUntouched(t *testing.T, slug, rest string) {
	t.Helper()
	if a.eventRest(t, slug) != rest {
		t.Fatalf("%s event row changed\n got %s\nwant %s", slug, a.eventRest(t, slug), rest)
	}
	if warned, removed := a.eventStamps(t, slug); warned.Valid || removed.Valid {
		t.Fatalf("%s was stamped: warned %v removed %v", slug, warned, removed)
	}
}
