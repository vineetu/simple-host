package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/config"
)

// Archive cleanup (M4) and the starter rubric seeded when an event is created.

func TestHackStarterRubric(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "rub")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	wantStarterRubric(t, decodeRubric(t, a.at(t, "GET", "/v1/hack/events/"+slug+"/rubric", nil, a.key(org))))
	if n := a.countQuery(t, `SELECT COUNT(*) FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, slug); n != 4 {
		t.Fatalf("seeded rows: %d", n)
	}

	// The organiser replaces the seed through the existing rubric route.
	replaced := decodeRubric(t, a.at(t, "PUT", "/v1/hack/events/"+slug+"/rubric",
		rubricBody(criterion("Clarity of purpose", "Clear.", 100, 5)), a.key(org)))
	if len(replaced.Criteria) != 1 || replaced.Criteria[0].Name != "Clarity of purpose" || replaced.Criteria[0].Weight != 100 {
		t.Fatalf("replaced: %+v", replaced.Criteria)
	}

	// A second new event gets the seed. The first event's rubric stays as the organiser left it.
	slug2 := uniqueSlug()
	if r := a.createEvent(t, org, slug2, map[string]any{"title": "Second"}); r.status != http.StatusCreated {
		t.Fatalf("create 2: %d %s", r.status, r.body)
	}
	wantStarterRubric(t, decodeRubric(t, a.at(t, "GET", "/v1/hack/events/"+slug2+"/rubric", nil, a.key(org))))
	again := decodeRubric(t, a.at(t, "GET", "/v1/hack/events/"+slug+"/rubric", nil, a.key(org)))
	if len(again.Criteria) != 1 || again.Criteria[0].Name != "Clarity of purpose" {
		t.Fatalf("first event's rubric changed: %+v", again.Criteria)
	}
}

func wantStarterRubric(t *testing.T, v rubricView) {
	t.Helper()
	want := defaultEventRubric()
	if len(v.Criteria) != len(want) {
		t.Fatalf("criteria: %+v", v.Criteria)
	}
	for i, c := range want {
		g := v.Criteria[i]
		if g.ID == "" || g.Position != i || g.Name != c.Name || g.Description != c.Description || g.Weight != c.Weight || g.MaxPoints != c.MaxPoints {
			t.Fatalf("criterion %d: %+v", i, g)
		}
	}
}

func TestHackArchiveCleanup(t *testing.T) {
	a := newHackApp(t)
	old := *config.Active()
	l := old
	l.EventCreatePerDay = 40
	l.EventMaxActivePerOrganiser = 40
	config.SetActive(l)
	t.Cleanup(func() { config.SetActive(old) })

	org := a.newPerson(t, "cln")
	now := time.Date(2026, 6, 15, 15, 4, 5, 0, time.UTC)
	keep := config.Active().EventSitesKeep
	warnFor := config.Active().EventRemovalWarn
	pastRemove := now.Add(-keep - 36*time.Hour)
	inWarn := now.Add(-keep + warnFor/2)

	removalSlug, removalTeams := a.makeCleanupEvent(t, org, "Removal Hack", "removal@example.com", 2)
	warnSlug, _ := a.makeCleanupEvent(t, org, "Harbour Hack", "harbour@example.com", 1)
	keepSlug, _ := a.makeCleanupEvent(t, org, "Keep Hack", "keep@example.com", 1)
	var live []string
	for _, stage := range []string{"open", "building", "closed", "judging", "results"} {
		slug, _ := a.makeCleanupEvent(t, org, "Stage "+stage, "stage-"+stage+"@example.com", 1)
		a.setEventClock(t, slug, stage, pastRemove, false)
		live = append(live, slug)
	}
	a.setEventClock(t, removalSlug, "archived", pastRemove, false)
	a.setEventClock(t, warnSlug, "archived", inWarn, false)
	a.setEventClock(t, keepSlug, "archived", pastRemove, true)

	// The organiser's own rubric and a published snapshot must survive removal.
	custom := decodeRubric(t, a.at(t, "PUT", "/v1/hack/events/"+removalSlug+"/rubric",
		rubricBody(criterion("Impact", "Did it matter?", 60, 5), criterion("Craft", "Is it well made?", 40, 10)), a.key(org)))
	if len(custom.Criteria) != 2 {
		t.Fatalf("custom rubric: %+v", custom.Criteria)
	}
	if _, err := a.database.Exec(`
		INSERT INTO event_results (event_id, full_ranking, snapshot)
		SELECT id, false, '{"place":1,"note":"keep me"}'::jsonb FROM events WHERE slug = $1`, removalSlug); err != nil {
		t.Fatal(err)
	}
	// An archived event with no rubric rows must not gain any from the sweep.
	if _, err := a.database.Exec(`DELETE FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, warnSlug); err != nil {
		t.Fatal(err)
	}

	rest := map[string]string{}
	for _, slug := range append([]string{removalSlug, warnSlug, keepSlug}, live...) {
		rest[slug] = a.eventRest(t, slug)
	}
	resultsBefore := a.resultsBlob(t, removalSlug)
	rubricBefore := a.rubricBlob(t, removalSlug)

	sites := &cleanupSites{fail: map[string]error{removalTeams[0]: errors.New("disk full")}}
	a.hack.SetSites(sites)
	ctx := context.Background()

	// No mailer: the whole pass, including removal, does nothing.
	a.hack.runEventCleanup(ctx, now)
	if calls := sites.called(); len(calls) != 0 {
		t.Fatalf("trashed with no mailer: %+v", calls)
	}
	if warned, removed := a.eventStamps(t, removalSlug); warned.Valid || removed.Valid {
		t.Fatalf("stamped with no mailer: warned %v removed %v", warned, removed)
	}

	// A mailer that cannot send still removes (removal sends no email). A failed
	// warning is not recorded.
	a.hack.SetMailer(&failingReplyMailer{})
	a.hack.runEventCleanup(ctx, now)
	account := a.eventAccount(t, removalSlug)
	calls := sites.called()
	if len(calls) != 2 || calls[0].account != account || calls[0].slug != removalTeams[0] || calls[1].account != account || calls[1].slug != removalTeams[1] {
		t.Fatalf("trash calls: %+v want %s / %v", calls, account, removalTeams)
	}
	warned, removed := a.eventStamps(t, removalSlug)
	if warned.Valid || !removed.Valid || !removed.Time.Equal(now) {
		t.Fatalf("removal stamps: warned %v removed %v want removed %v", warned, removed, now)
	}
	if a.eventRest(t, removalSlug) != rest[removalSlug] {
		t.Fatalf("removal changed the event row\n got %s\nwant %s", a.eventRest(t, removalSlug), rest[removalSlug])
	}
	if a.resultsBlob(t, removalSlug) != resultsBefore {
		t.Fatalf("event_results changed: %s want %s", a.resultsBlob(t, removalSlug), resultsBefore)
	}
	if a.rubricBlob(t, removalSlug) != rubricBefore {
		t.Fatalf("rubric changed:\n got %s\nwant %s", a.rubricBlob(t, removalSlug), rubricBefore)
	}
	if w, r := a.eventStamps(t, warnSlug); w.Valid || r.Valid {
		t.Fatalf("failed warning was recorded: warned %v removed %v", w, r)
	}
	if n := a.countQuery(t, `SELECT COUNT(*) FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, warnSlug); n != 0 {
		t.Fatalf("sweep inserted rubric rows: %d", n)
	}
	a.wantUntouched(t, keepSlug, rest[keepSlug])
	for _, slug := range live {
		a.wantUntouched(t, slug, rest[slug])
	}

	mail := &replyMailer{}
	a.hack.SetMailer(mail)
	a.hack.runEventCleanup(ctx, now)
	if more := sites.called(); len(more) != 2 {
		t.Fatalf("trashed again on the warning pass: %+v", more)
	}
	warned, removed = a.eventStamps(t, warnSlug)
	if !warned.Valid || !warned.Time.Equal(now) || removed.Valid {
		t.Fatalf("warn stamps: warned %v removed %v", warned, removed)
	}
	if a.eventRest(t, warnSlug) != rest[warnSlug] {
		t.Fatalf("warning changed the event row\n got %s\nwant %s", a.eventRest(t, warnSlug), rest[warnSlug])
	}
	a.wantUntouched(t, keepSlug, rest[keepSlug])
	for _, slug := range live {
		a.wantUntouched(t, slug, rest[slug])
	}
	if w, r := a.eventStamps(t, removalSlug); w.Valid || !r.Time.Equal(now) {
		t.Fatalf("removal event changed on the warning pass: warned %v removed %v", w, r)
	}

	var closed time.Time
	if err := a.database.QueryRow(`SELECT closed_at FROM events WHERE slug = $1`, warnSlug).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	removeOn := closed.UTC().Add(keep).Format("2 January 2006")
	subject := "Harbour Hack: team sites will be removed on " + removeOn
	text := fmt.Sprintf(`Harbour Hack was archived. Its team sites will be removed on %s.

The event page and the results are not affected.

If you need more time, write to support@simple-host.app.

Simple Hack
`, removeOn)
	wantMail := "harbour@example.com|" + idleReplyTo() + "|" + subject + "|" + text
	msgs := mail.messages()
	if len(msgs) != 1 || msgs[0] != wantMail {
		t.Fatalf("warning email:\n got %q\nwant %q", msgs, wantMail)
	}

	// A second pass does not send the warning again, and does not remove the sites yet.
	a.hack.runEventCleanup(ctx, now)
	if again := mail.messages(); len(again) != 1 {
		t.Fatalf("warning sent again: %d", len(again))
	}
	if _, removed := a.eventStamps(t, warnSlug); removed.Valid {
		t.Fatal("warned event was removed")
	}
	if more := sites.called(); len(more) != 2 {
		t.Fatalf("trash calls after the second pass: %+v", more)
	}
}

func TestHackAdminKeepSites(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "kps")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	path := "/v1/admin/hack/events/" + slug + "/keep-sites"
	if r := a.at(t, "POST", path, map[string]any{"keep": true}, nil); r.status != http.StatusUnauthorized {
		t.Fatalf("no key: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", path, map[string]any{"keep": true}, a.key(org)); r.status != http.StatusNotFound {
		t.Fatalf("organiser: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", path, map[string]any{}, a.adminH()); r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_request" {
		t.Fatalf("missing keep: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/admin/hack/events/no-such-event/keep-sites", map[string]any{"keep": true}, a.adminH()); r.status != http.StatusNotFound {
		t.Fatalf("missing event: %d %s", r.status, r.body)
	}

	for _, keep := range []bool{true, false} {
		r := a.at(t, "POST", path, map[string]any{"keep": keep}, a.adminH())
		if r.status != http.StatusOK {
			t.Fatalf("keep %v: %d %s", keep, r.status, r.body)
		}
		var body map[string]any
		if err := json.Unmarshal(r.body, &body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 1 || body["keep_sites"] != keep {
			t.Fatalf("keep %v response: %s", keep, r.body)
		}
		var stored bool
		if err := a.database.QueryRow(`SELECT keep_sites FROM events WHERE slug = $1`, slug).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != keep {
			t.Fatalf("stored keep_sites %v, want %v", stored, keep)
		}
	}
}

// cleanupSites records TrashTeamSite. fail maps a team slug to an error.
type cleanupSites struct {
	entrySiteStub
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

func (m *replyMailer) messages() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.sent...)
}

func (a *hackApp) makeCleanupEvent(t *testing.T, org person, title, contact string, nTeams int) (string, []string) {
	t.Helper()
	slug := uniqueSlug()
	extra := map[string]any{"title": title}
	if contact != "" {
		extra["contact_email"] = contact
	}
	if r := a.createEvent(t, org, slug, extra); r.status != http.StatusCreated {
		t.Fatalf("create %s: %d %s", title, r.status, r.body)
	}
	if nTeams == 0 {
		return slug, nil
	}
	a.openEvent(t, org, slug)
	join := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)["organiser"].(map[string]any)["join_code"].(string)
	teams := make([]string, 0, nTeams)
	for i := 0; i < nTeams; i++ {
		name := fmt.Sprintf("Crew %d", i+1)
		p := a.newPerson(t, "ct")
		if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": name}, a.key(p)); r.status != http.StatusOK {
			t.Fatalf("join %s: %d %s", name, r.status, r.body)
		}
		if r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.key(p)); r.status != http.StatusCreated {
			t.Fatalf("team %s: %d %s", name, r.status, r.body)
		}
		var teamSlug string
		if err := a.database.QueryRow(`
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
	res, err := a.database.Exec(`UPDATE events SET stage = $2, closed_at = $3, keep_sites = $4 WHERE slug = $1`, slug, stage, closed, keep)
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil || n != 1 {
		t.Fatalf("clock %s: %d %v", slug, n, err)
	}
}

func (a *hackApp) countQuery(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := a.database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (a *hackApp) eventAccount(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *hackApp) eventStamps(t *testing.T, slug string) (sql.NullTime, sql.NullTime) {
	t.Helper()
	var warned, removed sql.NullTime
	if err := a.database.QueryRow(`SELECT removal_warned_at, sites_removed_at FROM events WHERE slug = $1`, slug).Scan(&warned, &removed); err != nil {
		t.Fatal(err)
	}
	return warned, removed
}

// eventRest is every column the sweep must not write, including updated_at.
func (a *hackApp) eventRest(t *testing.T, slug string) string {
	t.Helper()
	var s string
	err := a.database.QueryRow(`
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
	err := a.database.QueryRow(`
		SELECT full_ranking::text || '|' || snapshot::text || '|' || coalesce(published_by::text, '') || '|' || published_at::text
		  FROM event_results r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&s)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (a *hackApp) rubricBlob(t *testing.T, slug string) string {
	t.Helper()
	rows, err := a.database.Query(`
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
