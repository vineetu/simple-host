package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/model"
)

// Judge notices, custom reminder offsets and organiser team messages
// (0003-hack-notices-judges-messages.sql).

// testEntriesService builds an entries.Service bound to a's database, the
// same way sweepNoticeMail does, for driving QueueJudgeReminders directly
// (there is no HTTP route for a sweep).
func testEntriesService(a *teamSiteApp) *entries.Service {
	return entries.New(a.database, func(ctx context.Context, ev model.Event, slug string, takenDown bool) bool {
		info, _, err := a.sites.TeamSiteInfo(ctx, ev.AccountID, slug)
		return err == nil && info.Exists && info.ActiveVersion > 0 && !info.Suspended && !takenDown
	})
}

func queueJudgeRemindersNow(t *testing.T, a *teamSiteApp) {
	t.Helper()
	if _, err := events.New(a.database).QueueJudgeReminders(context.Background(), testEntriesService(a)); err != nil {
		t.Fatalf("queue judge reminders: %v", err)
	}
}

func teamIDFor(t *testing.T, a *teamSiteApp, eventSlug, teamSlug string) string {
	t.Helper()
	var id string
	if err := a.database.QueryRow(`
		SELECT t.id FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.slug = $1 AND t.slug = $2`, eventSlug, teamSlug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func joinAsJudge(t *testing.T, a *teamSiteApp, slug string, org, judge person, name string) {
	t.Helper()
	ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)
	code := ev["organiser"].(map[string]any)["judge_code"].(string)
	wantTS(t, "judge join", a.api(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true, "display_name": name}, judge.key), 200, "")
}

// TestHackJudgingOpenAndReminderNotices covers: moving to judging writes a
// judging_open notice for every judge (text names the unscored count, 0 or
// not), the email sweep sends it; the judging-close reminder reaches only
// judges with an unscored team (one who scored everything gets none); no
// reminder at all while judging_ends_at is unset; deleting the event leaves
// no orphaned event_judge_reminders rows.
func TestHackJudgingOpenAndReminderNotices(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "jopenorg")
	lead := a.newPerson(t, "jopenlead")
	judgeBusy := a.newPerson(t, "jopenbusy")
	judgeDone := a.newPerson(t, "jopendone")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, lead, org, "Lea")
	team, _ := a.startTeam(t, slug, "Alpha", lead)
	wantTS(t, "draft entry", a.api(t, "PUT", base+"/entry", map[string]any{"title": "Alpha thing"}, lead.key), 200, "")
	joinAsJudge(t, a, slug, org, judgeBusy, "Busy")
	joinAsJudge(t, a, slug, org, judgeDone, "Done")

	for _, stage := range []string{"building", "closed"} {
		wantTS(t, "stage "+stage, a.api(t, "POST", base+"/stage", map[string]string{"stage": stage}, org.key), 200, "")
	}
	rv := decodeRubric(t, a.api(t, "PUT", base+"/rubric", rubricBody(criterion("Impact", "", 100, 5)), org.key))
	c1 := rv.Criteria[0].ID
	teamID := teamIDFor(t, a, slug, team)
	wantTS(t, "score", a.api(t, "PUT", base+"/judge/scores/"+teamID, map[string]any{"scores": []map[string]any{{"criterion_id": c1, "points": 5}}}, judgeDone.key), 200, "")

	// Judging opens: every judge gets a line, worded from their own queue.
	wantTS(t, "stage judging", a.api(t, "POST", base+"/stage", map[string]string{"stage": "judging"}, org.key), 200, "")
	busyNeed := findNeed(needsYouOf(t, a, slug, judgeBusy), "judging_open")
	if busyNeed == nil || busyNeed["text"] != "Judging is open: you have 1 team to score." || busyNeed["section"] != "queue" || busyNeed["dismissable"] != true {
		t.Fatalf("busy judge judging_open: %v", busyNeed)
	}
	// A judge who already scored every team in a non-empty queue is told
	// so, not "no teams are in your queue yet" (point 10: a populated,
	// fully-scored queue is not the same as an empty one).
	doneNeed := findNeed(needsYouOf(t, a, slug, judgeDone), "judging_open")
	if doneNeed == nil || doneNeed["text"] != "Judging is open. You've scored every team in your queue." {
		t.Fatalf("done judge judging_open: %v", doneNeed)
	}
	sweepNow(a)
	if got := mailTo(sink, judgeBusy.email); len(got) != 1 || !strings.Contains(got[0].subject, "judging is open") ||
		!strings.Contains(got[0].body, "Judging is open: you have 1 team to score.") {
		t.Fatalf("busy judge judging_open mail: %+v", got)
	}
	if got := mailTo(sink, judgeDone.email); len(got) != 1 || !strings.Contains(got[0].body, "You've scored every team in your queue.") {
		t.Fatalf("done judge judging_open mail: %+v", got)
	}

	// No judging_ends_at yet: the reminder sweep writes nothing.
	queueJudgeRemindersNow(t, a)
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_judge_reminders r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("judge reminder rows before judging_ends_at: %d %v", rows, err)
	}

	// Set judging_ends_at inside the default 1h offset: only the judge with
	// an unscored team is reminded.
	closesAt := time.Now().Add(50 * time.Minute).UTC().Format(time.RFC3339)
	wantTS(t, "set judging_ends_at", a.api(t, "PATCH", base, map[string]any{"judging_ends_at": closesAt}, org.key), 200, "")
	queueJudgeRemindersNow(t, a)
	sweepNow(a)
	if got := mailTo(sink, judgeBusy.email); len(got) != 2 || !strings.Contains(got[1].subject, "judging closes soon") ||
		!strings.Contains(got[1].body, "You still have 1 team to score.") || !strings.Contains(got[1].body, "/e/"+slug+"/judge?s=queue") {
		t.Fatalf("judging_reminder mail: %+v", got)
	}
	if got := mailTo(sink, judgeDone.email); len(got) != 1 {
		t.Fatalf("judge who scored everything was reminded: %+v", got)
	}
	// Round 2 (starvation fix): a fully-scored judge still gets a claim row
	// for this deadline -- just no notice -- so DueJudgeReminders' own NOT
	// EXISTS stops re-selecting them every sweep.
	if err := a.database.QueryRow(`SELECT count(*) FROM event_judge_reminders r WHERE r.user_id = $1`, a.uid(t, judgeDone)).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("claim row for a judge with nothing left: %d %v", rows, err)
	}

	// Deleting the event leaves no orphaned reminder rows (cascade, point 8).
	var eventID string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug = $1`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "delete event", a.api(t, "DELETE", base, map[string]any{"confirm": slug}, org.key), 204, "")
	if err := a.database.QueryRow(`SELECT count(*) FROM event_judge_reminders WHERE event_id = $1`, eventID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("orphaned judge reminders after delete: %d %v", rows, err)
	}
}

// TestHackJudgeReminderStaleAfterDeadlineMoves covers point 3: a reminder
// already queued (claimed, notice written, still pending in the mail
// queue) for one offset/deadline must not go out once judging_ends_at has
// since moved -- here, moved 7 days later, so the "1h" label queued no
// longer describes how much time is actually left.
func TestHackJudgeReminderStaleAfterDeadlineMoves(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "jstaleorg")
	lead := a.newPerson(t, "jstalelead")
	judge := a.newPerson(t, "jstalejudge")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, lead, org, "Lea")
	_, _ = a.startTeam(t, slug, "Alpha", lead)
	wantTS(t, "draft entry", a.api(t, "PUT", base+"/entry", map[string]any{"title": "Alpha thing"}, lead.key), 200, "")
	joinAsJudge(t, a, slug, org, judge, "Jud")
	for _, stage := range []string{"building", "closed"} {
		wantTS(t, "stage "+stage, a.api(t, "POST", base+"/stage", map[string]string{"stage": stage}, org.key), 200, "")
	}
	wantTS(t, "rubric", a.api(t, "PUT", base+"/rubric", rubricBody(criterion("Impact", "", 100, 5)), org.key), 200, "")
	wantTS(t, "stage judging", a.api(t, "POST", base+"/stage", map[string]string{"stage": "judging"}, org.key), 200, "")

	// Inside the default 1h offset: the sweep claims and queues the
	// judging_reminder (the judge's one team is still unscored).
	closesAt := time.Now().Add(50 * time.Minute).UTC().Format(time.RFC3339)
	wantTS(t, "set judging_ends_at", a.api(t, "PATCH", base, map[string]any{"judging_ends_at": closesAt}, org.key), 200, "")
	queueJudgeRemindersNow(t, a)
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_judge_reminders r WHERE r.user_id = $1`, a.uid(t, judge)).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("queued judge reminder: %d %v", rows, err)
	}

	// judging_ends_at moves 7 days later before the mail sweep runs: "1h"
	// no longer describes 7 days plus change left, so the queued reminder
	// must be dropped as stale, not emailed.
	if _, err := a.database.Exec(`UPDATE events SET judging_ends_at = judging_ends_at + interval '7 days' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	sweepNow(a)
	for _, m := range mailTo(sink, judge.email) {
		if strings.Contains(m.subject, "judging closes soon") || strings.Contains(m.body, "still have") {
			t.Fatalf("stale judge reminder emailed after judging_ends_at moved 7 days later: %+v", m)
		}
	}
}

// TestHackTeamMessages covers: a message reaches only the chosen team's
// current members, shows in their needs_you, and is listed on both history
// endpoints; a participant or judge may not send one; the DB-counted rate
// limit; an opted-out member and a key-only account see the in-app line but
// get no email; event deletion leaves no orphaned message rows.
func TestHackTeamMessages(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "tmsgorg")
	alphaLead := a.newPerson(t, "tmsglead")
	alphaMate := a.newPerson(t, "tmsgmate")
	alphaKey := a.newKeyOnlyPersonTS(t, "tmsgkey")
	betaLead := a.newPerson(t, "tmsgbeta")
	judge := a.newPerson(t, "tmsgjudge")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, alphaLead, org, "Lea")
	waitJoin(t, a, slug, alphaMate, org, "Mat")
	waitJoin(t, a, slug, alphaKey, org, "Kay")
	waitJoin(t, a, slug, betaLead, org, "Bet")
	alphaTeam, alphaCode := a.startTeam(t, slug, "Alpha", alphaLead)
	betaTeam, _ := a.startTeam(t, slug, "Beta", betaLead)
	for _, p := range []person{alphaMate, alphaKey} {
		wantTS(t, "join alpha", a.api(t, "POST", base+"/teams/join", map[string]any{"code": alphaCode}, p.key), 200, "")
	}
	wantTS(t, "opt out", a.api(t, "PATCH", "/v1/hack/preferences", map[string]any{"email_updates": false}, alphaMate.key), 200, "")
	joinAsJudge(t, a, slug, org, judge, "Jud")

	alphaID := teamIDFor(t, a, slug, alphaTeam)
	r := a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{alphaID}, "text": "Please update your README."}, org.key)
	wantTS(t, "message team", r, http.StatusCreated, "")
	sent := r.json(t)["messages"].([]any)[0].(map[string]any)
	if sent["team_id"] != alphaID || sent["team_name"] != "Alpha" || sent["sender_name"] != "Org" || sent["recipients"] != float64(3) {
		t.Fatalf("sent message: %v", sent)
	}

	// Shows in needs_you for every current Alpha member, not Beta's.
	for _, p := range []person{alphaLead, alphaMate, alphaKey} {
		need := findNeed(needsYouOf(t, a, slug, p), "team_message")
		if need == nil || need["text"] != "Message from the organisers: Please update your README." || need["section"] != "team" || need["dismissable"] != true {
			t.Fatalf("team member needs_you: %v", need)
		}
	}
	if findNeed(needsYouOf(t, a, slug, betaLead), "team_message") != nil {
		t.Fatal("another team sees Alpha's message")
	}

	// Listed on both history endpoints.
	orgHist := a.api(t, "GET", base+"/teams/"+alphaTeam+"/messages", nil, org.key).json(t)["messages"].([]any)
	if len(orgHist) != 1 || orgHist[0].(map[string]any)["text"] != "Please update your README." {
		t.Fatalf("organiser history: %v", orgHist)
	}
	orgBetaHist := a.api(t, "GET", base+"/teams/"+betaTeam+"/messages", nil, org.key).json(t)["messages"].([]any)
	if len(orgBetaHist) != 0 {
		t.Fatalf("organiser beta history not empty: %v", orgBetaHist)
	}
	myHist := a.api(t, "GET", base+"/team/messages", nil, alphaLead.key).json(t)["messages"].([]any)
	if len(myHist) != 1 || myHist[0].(map[string]any)["text"] != "Please update your README." {
		t.Fatalf("participant history: %v", myHist)
	}
	betaHist := a.api(t, "GET", base+"/team/messages", nil, betaLead.key).json(t)["messages"].([]any)
	if len(betaHist) != 0 {
		t.Fatalf("beta history not empty: %v", betaHist)
	}

	// A participant or judge may not send one.
	wantCode(t, "participant refused", a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{alphaID}, "text": "x"}, alphaLead.key), http.StatusNotFound, "event_not_found")
	wantCode(t, "judge refused", a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{alphaID}, "text": "x"}, judge.key), http.StatusNotFound, "event_not_found")

	// Opted-out and key-only members see the line but get no email; the
	// normal lead does.
	sweepNow(a)
	if got := mailTo(sink, alphaLead.email); len(got) != 1 || !strings.Contains(got[0].body, "Message from the organisers: Please update your README.") ||
		!strings.Contains(got[0].subject, "a message from the organisers") {
		t.Fatalf("lead email: %+v", got)
	}
	if got := mailTo(sink, alphaMate.email); len(got) != 0 {
		t.Fatalf("opted-out member emailed: %+v", got)
	}
	for _, n := range sink.notices {
		if strings.Contains(n.to, "tmsgkey") {
			t.Fatalf("key-only account emailed: %+v", n)
		}
	}

	// Rate limit: at most 50 rows per event per rolling hour.
	var existing int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_messages WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, slug).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	for i := existing; i < 50; i++ {
		wantTS(t, "fill rate limit", a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{alphaID}, "text": "filler"}, org.key), http.StatusCreated, "")
	}
	limited := a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{alphaID}, "text": "one too many"}, org.key)
	wantCode(t, "rate limited", limited, http.StatusTooManyRequests, "rate_limited")
	if retry := limited.header.Get("Retry-After"); retry == "" {
		t.Fatal("no Retry-After header")
	} else if n, err := strconv.Atoi(retry); err != nil || n <= 0 {
		t.Fatalf("Retry-After %q", retry)
	}

	// Deleting the event leaves no orphaned message rows (cascade, point 8).
	var eventID string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug = $1`, slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "delete event", a.api(t, "DELETE", base, map[string]any{"confirm": slug}, org.key), 204, "")
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_messages WHERE event_id = $1`, eventID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("orphaned team messages after delete: %d %v", rows, err)
	}
}

// TestHackTeamMessageRateLimitConcurrent covers point 1: two sends arriving
// at the same instant, right at the 50/hour boundary, must not both get
// through and push the count past it -- the count-then-insert has to be
// serialised per event (pg_advisory_xact_lock), not just correct when run
// one at a time.
func TestHackTeamMessageRateLimitConcurrent(t *testing.T) {
	a := newTeamSiteApp(t)
	org := a.newPerson(t, "tmsgraceorg")
	lead := a.newPerson(t, "tmsgracelead")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, lead, org, "Lea")
	team, _ := a.startTeam(t, slug, "Alpha", lead)
	teamID := teamIDFor(t, a, slug, team)

	// Fill to exactly one below the limit, so each of the two concurrent
	// sends below would individually look like it's still under the limit
	// if the count-then-insert weren't serialised.
	for i := 0; i < 49; i++ {
		wantTS(t, "fill rate limit", a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{teamID}, "text": "filler"}, org.key), http.StatusCreated, "")
	}

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{teamID}, "text": "racer"}, org.key).status
		}(i)
	}
	wg.Wait()

	if !((statuses[0] == http.StatusCreated && statuses[1] == http.StatusTooManyRequests) ||
		(statuses[0] == http.StatusTooManyRequests && statuses[1] == http.StatusCreated)) {
		t.Fatalf("concurrent sends at the boundary: %v", statuses)
	}
	var count int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_messages WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, slug).Scan(&count); err != nil || count != 50 {
		t.Fatalf("rows after concurrent sends at the boundary: %d %v", count, err)
	}
}

// TestHackCustomReminderOffsets covers: PUT validation (too many offsets,
// too small, too large), custom offsets driving the entry reminder sweep
// (DueEntryReminders' generalised due-offset rule), and a moved deadline
// re-arming an already-sent reminder.
func TestHackCustomReminderOffsets(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "remoffsorg")
	lead := a.newPerson(t, "remoffslead")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, lead, org, "Lea")
	team, _ := a.startTeam(t, slug, "Alpha", lead)
	_ = team

	wantCode(t, "too many offsets", a.api(t, "PUT", base+"/reminders", map[string]any{"offsets_minutes": []int{60, 120, 180, 240, 300, 360}}, org.key), http.StatusBadRequest, "invalid_offsets")
	wantCode(t, "too small", a.api(t, "PUT", base+"/reminders", map[string]any{"offsets_minutes": []int{10}}, org.key), http.StatusBadRequest, "invalid_offsets")
	wantCode(t, "too large", a.api(t, "PUT", base+"/reminders", map[string]any{"offsets_minutes": []int{21600}}, org.key), http.StatusBadRequest, "invalid_offsets")
	// Point 9: a missing offsets_minutes field is refused, not silently
	// treated as [] (which would turn reminders off for both audiences).
	wantCode(t, "missing field", a.api(t, "PUT", base+"/reminders", map[string]any{}, org.key), http.StatusBadRequest, "invalid_offsets")
	// An explicit [] is still how reminders are turned off.
	wantTS(t, "explicit empty turns reminders off", a.api(t, "PUT", base+"/reminders", map[string]any{"offsets_minutes": []int{}}, org.key), http.StatusOK, "")

	put := a.api(t, "PUT", base+"/reminders", map[string]any{"offsets_minutes": []int{4320, 120}}, org.key)
	wantTS(t, "set offsets", put, http.StatusOK, "")
	settings := put.json(t)
	offs := settings["offsets_minutes"].([]any)
	if len(offs) != 2 || offs[0] != float64(4320) || offs[1] != float64(120) {
		t.Fatalf("offsets not sorted descending: %v", offs)
	}

	if _, err := a.database.Exec(`UPDATE events SET stage = 'building', submission_deadline = now() + interval '50 hours' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	events.New(a.database).QueueEntryReminders(context.Background())
	sweepNow(a)
	got := mailTo(sink, lead.email)
	if len(got) != 1 {
		t.Fatalf("72h reminder: %+v", got)
	}
	var kind string
	if err := a.database.QueryRow(`SELECT kind FROM event_entry_reminders r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&kind); err != nil || kind != "72h" {
		t.Fatalf("reminder kind: %q %v", kind, err)
	}

	// Still 50 hours out with the same offsets, nobody ever gets a 24h one:
	// 1440 isn't configured, so no row of that kind can exist.
	var kind24 int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_entry_reminders r JOIN events e ON e.id = r.event_id WHERE e.slug = $1 AND r.kind = '24h'`, slug).Scan(&kind24); err != nil || kind24 != 0 {
		t.Fatalf("unexpected 24h reminder: %d %v", kind24, err)
	}

	// A moved deadline (still inside the 72h offset) re-arms the reminder.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() + interval '49 hours' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE event_notices SET emailed_at = emailed_at - interval '16 minutes' WHERE kind = 'entry_reminder' AND email_state = 'sent' AND event_id = (SELECT id FROM events WHERE slug = $1)`, slug); err != nil {
		t.Fatal(err)
	}
	events.New(a.database).QueueEntryReminders(context.Background())
	sweepNow(a)
	if got := mailTo(sink, lead.email); len(got) != 2 {
		t.Fatalf("re-armed reminder: %+v", got)
	}
}

// TestHackEntryReminderExactStaleAfterDeadlineMoves covers the round 2 exact
// staleness check (0003-hack-notices-judges-messages.sql): detail is now
// "<label>@<deadline RFC3339 UTC>", read back and compared exactly, instead
// of the retired "+30 minutes" heuristic. With offsets_minutes [15], a
// reminder queued 10 minutes out is still within the old heuristic's grace
// window (15+30 = 45 minutes) once the deadline moves to 40 minutes out --
// under the old rule it would wrongly still go out -- but the deadline it
// was queued for no longer exists, so the exact check must drop it. Once
// the deadline comes back within the offset (a different instant from the
// first), the reminder re-arms and is sent.
func TestHackEntryReminderExactStaleAfterDeadlineMoves(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "remexactorg")
	lead := a.newPerson(t, "remexactlead")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, lead, org, "Lea")
	a.startTeam(t, slug, "Alpha", lead)
	wantTS(t, "set offsets", a.api(t, "PUT", base+"/reminders", map[string]any{"offsets_minutes": []int{15}}, org.key), http.StatusOK, "")

	// 10 minutes out: inside the 15-minute offset, so the sweep claims and
	// queues the reminder.
	if _, err := a.database.Exec(`UPDATE events SET stage = 'building', submission_deadline = now() + interval '10 minutes' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	events.New(a.database).QueueEntryReminders(context.Background())
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_entry_reminders r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("queued entry reminder: %d %v", rows, err)
	}

	// The deadline moves to 40 minutes out before the mail sweep runs.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() + interval '40 minutes' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	sweepNow(a)
	if got := mailTo(sink, lead.email); len(got) != 0 {
		t.Fatalf("stale reminder emailed after deadline moved from 10m to 40m out: %+v", got)
	}

	// The deadline comes back within the offset, at a different instant
	// than the first time: the reminder re-arms and is sent.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() + interval '10 minutes' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	events.New(a.database).QueueEntryReminders(context.Background())
	sweepNow(a)
	if got := mailTo(sink, lead.email); len(got) != 1 {
		t.Fatalf("re-armed reminder not emailed: %+v", got)
	}
}
