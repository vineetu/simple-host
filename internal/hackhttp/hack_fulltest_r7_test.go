package hackhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
)

// Tests for the fixes from the 2026-11-02 full production test of Simple
// Hack (findings 1, 2, 5, 6, 8) and security review finding F1.

// liveSiteStub is entrySiteStub with the team sites named in live reported
// as live (a published version), so the settle hook sees them.
type liveSiteStub struct {
	entrySiteStub
	live map[string]bool
}

func (s liveSiteStub) TeamSiteInfo(_ context.Context, _ string, teamSlug string) (model.TeamSite, db.Site, error) {
	if s.live[teamSlug] {
		return model.TeamSite{Exists: true, ActiveVersion: 1}, db.Site{Name: teamSlug, ActiveVersion: 1}, nil
	}
	return model.TeamSite{}, db.Site{}, nil
}

// publishTeamSite gives the event's holding account a live site named after
// the team (what loadTeamList reads), at version 1.
func publishTeamSite(t *testing.T, a *hackApp, slug, teamSlug string) {
	t.Helper()
	var accountID string
	if err := a.database.QueryRow(`SELECT account_id FROM events WHERE slug = $1`, slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO sites (user_id, name, site_url, active_version) VALUES ($1, $2, $3, 1)`,
		accountID, teamSlug, "https://example.test/"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`INSERT INTO versions (site_id, version_number, disk_path, status)
		SELECT id, 1, 'x', 'active' FROM sites WHERE user_id = $1 AND name = $2`, accountID, teamSlug); err != nil {
		t.Fatal(err)
	}
}

func judgeQueueByTeam(t *testing.T, w *rulesWorld) (map[string]map[string]any, map[string]any) {
	t.Helper()
	q := w.a.at(t, "GET", "/v1/hack/events/"+w.slug+"/judge/queue", nil, w.a.key(w.judge))
	if q.status != http.StatusOK {
		t.Fatalf("queue: %d %s", q.status, q.body)
	}
	out := map[string]map[string]any{}
	body := q.json(t)
	for _, raw := range body["queue"].([]any) {
		item := raw.(map[string]any)
		out[item["team_id"].(string)] = item
	}
	return out, body
}

// Finding 1: an Open-mode event with no deadline goes straight to judging.
// Every draft is settled (and judged, flagged Incomplete), a team that only
// published its site is judged too, a team with nothing stays out, and the
// dashboard's counts (Overview's Start judging confirm) say so beforehand.
func TestHackStartJudgingSettlesEveryEntry(t *testing.T) {
	w := newRulesWorld(t, 3)
	a := w.a
	draft, siteOnly, nothing := w.teams[0], w.teams[1], w.teams[2]
	a.hack.SetSites(liveSiteStub{live: map[string]bool{siteOnly.slug: true}})
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "building"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Half done"}, a.key(draft.person)); r.status != 200 {
		t.Fatalf("draft: %d %s", r.status, r.body)
	}
	publishTeamSite(t, a, w.slug, siteOnly.slug)
	draftID, siteOnlyID, nothingID := w.teamID(t, draft.slug), w.teamID(t, siteOnly.slug), w.teamID(t, nothing.slug)

	// Before: nothing is judgeable yet, and the judge is told so (not "done").
	q, body := judgeQueueByTeam(t, w)
	if len(q) != 0 || body["assignment_mode"] != "open" {
		t.Fatalf("queue before judging: %#v", body)
	}
	dash := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/judging/dashboard", nil, a.key(w.org))
	if dash.status != 200 {
		t.Fatalf("dashboard: %d %s", dash.status, dash.body)
	}
	entries := dash.json(t)["entries"].(map[string]any)
	if entries["judged"] != float64(2) || entries["no_entry"] != float64(1) || entries["excluded"] != float64(0) {
		t.Fatalf("start-judging counts: %#v", entries)
	}
	for _, raw := range dash.json(t)["teams"].([]any) {
		if raw.(map[string]any)["team_id"] == nothingID {
			t.Fatalf("a team with no entry is in Judging progress: %s", dash.body)
		}
	}

	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "judging"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("judging: %d %s", r.status, r.body)
	}
	q, _ = judgeQueueByTeam(t, w)
	if len(q) != 2 {
		t.Fatalf("queue after judging: %#v", q)
	}
	for _, id := range []string{draftID, siteOnlyID} {
		item, ok := q[id]
		if !ok || item["incomplete"] != true || item["status"] != "not_submitted" {
			t.Fatalf("team %s not judged as Incomplete: %#v", id, item)
		}
	}
	if _, ok := q[nothingID]; ok {
		t.Fatal("a team with no entry and no site reached the queue")
	}
	// The site-only team's started entry is empty and written by nobody.
	var title string
	var by *string
	if err := a.database.QueryRow(`SELECT title, updated_by::text FROM event_entries WHERE team_id = $1`, siteOnlyID).Scan(&title, &by); err != nil {
		t.Fatal(err)
	}
	if title != "" || by != nil {
		t.Fatalf("site-only entry: title %q by %v", title, by)
	}
}

// Finding 2: automatic assignment only uses teams that will be judged, and
// each judge's progress count matches their queue.
func TestHackAutoAssignmentSkipsTeamsWithoutEntry(t *testing.T) {
	w := newRulesWorld(t, 3)
	a := w.a
	a.hack.SetSites(entrySiteStub{})
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "building"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	for _, tm := range w.teams[:2] {
		if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Entry " + tm.slug}, a.key(tm.person)); r.status != 200 {
			t.Fatalf("entry: %d %s", r.status, r.body)
		}
	}
	noEntryID := w.teamID(t, w.teams[2].slug)
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "closed"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("closed: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PATCH", "/v1/hack/events/"+w.slug+"/judging/settings", map[string]any{"assignment_mode": "automatic", "judges_per_team": 1}, a.key(w.org)); r.status != 200 {
		t.Fatalf("settings: %d %s", r.status, r.body)
	}
	gen := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/assignments/generate", nil, a.key(w.org))
	if gen.status != 200 {
		t.Fatalf("generate: %d %s", gen.status, gen.body)
	}
	var n, bad int
	if err := a.database.QueryRow(`SELECT COUNT(*), COUNT(*) FILTER (WHERE team_id = $2) FROM event_assignments
		WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug, noEntryID).Scan(&n, &bad); err != nil {
		t.Fatal(err)
	}
	if n != 2 || bad != 0 {
		t.Fatalf("assignments: %d total, %d to the team with no entry", n, bad)
	}
	q, _ := judgeQueueByTeam(t, w)
	dash := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/judging/dashboard", nil, a.key(w.org)).json(t)
	for _, raw := range dash["judges"].([]any) {
		j := raw.(map[string]any)
		if j["judge_id"] == a.userID(t, w.judge) && j["assigned_count"] != float64(len(q)) {
			t.Fatalf("progress says %v assigned, queue shows %d", j["assigned_count"], len(q))
		}
	}
	if len(dash["teams"].([]any)) != 2 {
		t.Fatalf("progress teams: %#v", dash["teams"])
	}

	// With no judgeable team at all, generating says so.
	w2 := newRulesWorld(t, 1)
	if r := w2.a.at(t, "PATCH", "/v1/hack/events/"+w2.slug+"/judging/settings", map[string]any{"assignment_mode": "automatic"}, w2.a.key(w2.org)); r.status != 200 {
		t.Fatalf("settings: %d %s", r.status, r.body)
	}
	wantHackErr(t, "nothing to judge", w2.a.at(t, "POST", "/v1/hack/events/"+w2.slug+"/assignments/generate", nil, w2.a.key(w2.org)),
		http.StatusBadRequest, "too_few_teams", "No team has an entry to judge yet.")
}

// Finding 5: a date-only end lasts the whole of that day.
func TestHackEventOverDateOnlyEnd(t *testing.T) {
	jkt, _ := time.LoadLocation("Asia/Jakarta")
	midnight := time.Date(2026, 11, 4, 0, 0, 0, 0, jkt)
	if got, want := hackEventOver(midnight, "Asia/Jakarta"), time.Date(2026, 11, 5, 0, 0, 0, 0, jkt); !got.Equal(want) {
		t.Fatalf("date-only end over at %v, want %v", got, want)
	}
	timed := time.Date(2026, 11, 4, 17, 30, 0, 0, jkt)
	if got := hackEventOver(timed, "Asia/Jakarta"); !got.Equal(timed) {
		t.Fatalf("timed end over at %v, want %v", got, timed)
	}
	// Midnight in UTC is 7 am in Jakarta: not a date-only end there.
	utcMidnight := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	if got := hackEventOver(utcMidnight, "Asia/Jakarta"); !got.Equal(utcMidnight) {
		t.Fatalf("UTC midnight in Jakarta: %v", got)
	}
	if got := hackEventOver(utcMidnight, ""); !got.Equal(utcMidnight.AddDate(0, 0, 1)) {
		t.Fatalf("no zone: %v", got)
	}

	// Find a hackathon keeps an event that ends today under "now".
	a := newHackApp(t)
	org := a.newPerson(t, "r7end")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	a.listEvent(t, slug)
	today := time.Now().UTC().Format("2006-01-02")
	if _, err := a.database.Exec(`UPDATE events SET time_zone = 'UTC', starts_at = $2::date - 1, ends_at = $2::date WHERE slug = $1`, slug, today); err != nil {
		t.Fatal(err)
	}
	dir := a.at(t, "GET", "/v1/hack/directory", nil, nil)
	if dir.status != 200 {
		t.Fatalf("directory: %d %s", dir.status, dir.body)
	}
	groups := dir.json(t)
	found := ""
	for _, g := range []string{"now", "upcoming", "past"} {
		list, _ := groups[g].([]any)
		for _, raw := range list {
			if raw.(map[string]any)["slug"] == slug {
				found = g
			}
		}
	}
	if found != "now" {
		t.Fatalf("event ending today is under %q, want now: %s", found, dir.body)
	}
}

// Finding 6: a rejection can't be changed by the organiser, but the
// rejected person can leave, apply again, and be decided again.
func TestHackRejectedApplicantLeavesAndReapplies(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "r7rorg")
	p := a.newPerson(t, "r7rapp")
	slug := uniqueSlug()
	a.createEvent(t, org, slug, nil)
	a.openEvent(t, org, slug)
	a.listEvent(t, slug)
	base := "/v1/hack/events/" + slug
	decide := func(what, decision string) resp {
		return a.at(t, "POST", base+"/applications/"+a.userID(t, p)+"/decision", map[string]string{"decision": decision}, a.key(org))
	}
	wantTS(t, "apply", a.apply(t, slug, p, nil), 200, "")
	wantTS(t, "reject", decide("reject", "rejected"), 200, "")
	wantTS(t, "approve after reject", decide("approve", "approved"), 409, "application_not_pending")
	wantTS(t, "rejected leaves", a.leave(t, slug, p, map[string]any{"confirm": slug}), 200, "")
	r := a.apply(t, slug, p, nil)
	wantTS(t, "apply again", r, 200, "")
	if r.json(t)["me"].(map[string]any)["approval_status"] != "pending" {
		t.Fatalf("reapply status: %v", r.json(t)["me"])
	}
	wantTS(t, "approve second application", decide("approve", "approved"), 200, "")
}

// Finding 8: my-results says how many teams got a place, which can be fewer
// than the teams in the event.
func TestHackMyResultsRankedTeams(t *testing.T) {
	w := newJudgingWorld(t, 4, 1)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org))
	c1 := rv.Criteria[0].ID
	// Teams C and D never wrote an entry: never judged, never placed.
	for _, tm := range w.teams[2:] {
		if _, err := w.a.database.Exec(`DELETE FROM event_entries WHERE team_id = $1`, tm.id); err != nil {
			t.Fatal(err)
		}
	}
	judge := w.judges[0]
	w.scoreTeam(t, judge, w.teams[0].id, map[string]int{c1: 4}, nil)
	w.scoreTeam(t, judge, w.teams[1].id, map[string]int{c1: 4}, nil)
	if r := w.call(t, "POST", "/results/publish", map[string]any{}, w.org); r.status != http.StatusOK {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}
	mine := w.call(t, "GET", "/my-results", nil, w.part.person)
	body := mine.json(t)
	if mine.status != 200 || body["ranked_teams"] != float64(2) || body["rank"] != float64(1) || body["tied"] != true {
		t.Fatalf("my-results: %d %s", mine.status, mine.body)
	}
}

// Security review F1: a judge gets a team's preview and pinned links only for
// a team they may score, once that team can no longer change its site.
// Organisers always get them.
func TestHackJudgeSiteLinksGated(t *testing.T) {
	w := newRulesWorld(t, 2)
	a := w.a
	a.hack.SetSites(entrySiteStub{})
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "building"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("building: %d %s", r.status, r.body)
	}
	early, other := w.teams[0], w.teams[1]
	earlyID := w.teamID(t, early.slug)
	publishTeamSite(t, a, w.slug, early.slug)
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Early"}, a.key(early.person)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}
	// Submitted while still building: judgeable, but the team can still
	// change its site, so a judge gets no links yet.
	if _, err := a.database.Exec(`UPDATE event_entries SET submitted_at = now(), submit_settled_at = now() WHERE team_id = $1`, earlyID); err != nil {
		t.Fatal(err)
	}
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Other"}, a.key(other.person)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}
	entryItem := func(p person, teamID string) map[string]any {
		t.Helper()
		r := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, a.key(p))
		if r.status != 200 {
			t.Fatalf("entries: %d %s", r.status, r.body)
		}
		for _, raw := range r.json(t)["entries"].([]any) {
			item := raw.(map[string]any)
			if item["team"].(map[string]any)["id"] == teamID {
				return item
			}
		}
		t.Fatalf("team %s missing: %s", teamID, r.body)
		return nil
	}
	previewOf := func(item map[string]any) any {
		site, _ := item["site"].(map[string]any)
		if site == nil {
			return nil
		}
		return site["preview_url"]
	}
	if it := entryItem(w.judge, earlyID); previewOf(it) != nil || it["pinned_url"] != nil {
		t.Fatalf("judge got links while the team builds: %#v", it)
	}
	q, _ := judgeQueueByTeam(t, w)
	if it, ok := q[earlyID]; !ok || previewOf(it) != nil || it["pinned_url"] != nil {
		t.Fatalf("queue gave links while the team builds: %#v", it)
	}
	if it := entryItem(w.org, earlyID); previewOf(it) == nil {
		t.Fatalf("organiser lost the preview link: %#v", it)
	}

	// Entries close: now the judge may open it.
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "closed"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("closed: %d %s", r.status, r.body)
	}
	if it := entryItem(w.judge, earlyID); previewOf(it) == nil || it["pinned_url"] == nil {
		t.Fatalf("judge has no links after entries closed: %#v", it)
	}
	// A declared conflict takes them away again.
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/conflicts", map[string]any{"team_id": earlyID}, a.key(w.judge)); r.status/100 != 2 {
		t.Fatalf("conflict: %d %s", r.status, r.body)
	}
	if it := entryItem(w.judge, earlyID); previewOf(it) != nil || it["pinned_url"] != nil {
		t.Fatalf("conflicted judge still gets links: %#v", it)
	}
	if it := entryItem(w.org, earlyID); previewOf(it) == nil {
		t.Fatalf("organiser lost the preview link: %#v", it)
	}
}
