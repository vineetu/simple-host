package server

import (
	"strings"
	"testing"
	"time"
)

// Notify people when something waits on them (2026-11-02, audit item 7).

func waitJoin(t *testing.T, a *teamSiteApp, slug string, p, org person, name string) {
	t.Helper()
	ev := a.api(t, "GET", "/v1/hack/events/"+slug, nil, org.key).json(t)
	code := ev["organiser"].(map[string]any)["join_code"].(string)
	if r := a.api(t, "POST", "/v1/hack/join/"+code, map[string]any{"accept_coc": true, "display_name": name}, p.key); r.status != 200 && r.status != 201 {
		t.Fatalf("join: %d %s", r.status, r.body)
	}
}

func needsYouOf(t *testing.T, a *teamSiteApp, slug string, p person) []map[string]any {
	t.Helper()
	r := a.api(t, "GET", "/v1/hack/events/"+slug, nil, p.key)
	wantTS(t, "event view", r, 200, "")
	raw, ok := r.json(t)["needs_you"].([]any)
	if !ok {
		t.Fatalf("no needs_you: %s", r.body)
	}
	out := []map[string]any{}
	for _, v := range raw {
		out = append(out, v.(map[string]any))
	}
	return out
}

func findNeed(items []map[string]any, kind string) map[string]any {
	for _, it := range items {
		if it["kind"] == kind {
			return it
		}
	}
	return nil
}

// mailTo returns the notices the sink got for one address.
func mailTo(sink *contentMailSink, addr string) []struct{ to, subject, body string } {
	var out []struct{ to, subject, body string }
	for _, n := range sink.notices {
		if n.to == addr {
			out = append(out, n)
		}
	}
	return out
}

// sweepNow runs the notice sweep with no first delay (the 15-minute window
// stays), as if the minute had already passed.
func sweepNow(a *teamSiteApp) {
	sweepNoticeMail(a, 0)
}

func TestHackWaitNoticesTeamRequests(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "waitorg")
	admin := a.newPerson(t, "waitadmin")
	asker := a.newPerson(t, "waitasker")
	second := a.newPerson(t, "waitsecond")
	keyOnly := a.newKeyOnlyPersonTS(t, "waitkey")
	outsider := a.newPerson(t, "waitoutsider")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, admin, org, "Ada")
	waitJoin(t, a, slug, asker, org, "Bea")
	waitJoin(t, a, slug, second, org, "Cy")
	waitJoin(t, a, slug, keyOnly, org, "Kay")
	waitJoin(t, a, slug, outsider, org, "Out")
	team, _ := a.startTeam(t, slug, "Alpha", admin)
	keyTeam, _ := a.startTeam(t, slug, "Keys", keyOnly)
	otherTeam, _ := a.startTeam(t, slug, "Other", outsider)
	_ = otherTeam

	// A request reaches the team's admins: a badge-able line, then one email.
	wantTS(t, "request", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, asker.key), 201, "")
	need := findNeed(needsYouOf(t, a, slug, admin), "join_request")
	if need == nil || need["count"] != float64(1) || need["text"] != "Bea asked to join Alpha." || need["section"] != "team" || need["dismissable"] != false {
		t.Fatalf("admin needs_you: %v", need)
	}
	if findNeed(needsYouOf(t, a, slug, outsider), "join_request") != nil {
		t.Fatal("another team's admin sees Alpha's request")
	}
	// Not before the first minute has passed.
	sweepNoticeMail(a, time.Hour)
	if got := mailTo(sink, admin.email); len(got) != 0 {
		t.Fatalf("emailed before the delay: %+v", got)
	}
	sweepNow(a)
	got := mailTo(sink, admin.email)
	if len(got) != 1 || !strings.Contains(got[0].body, "Bea asked to join Alpha.") || !strings.Contains(got[0].subject, "asked to join your team") ||
		!strings.Contains(got[0].body, "/e/"+slug+"/team?s=team") || !strings.Contains(got[0].body, "/account") {
		t.Fatalf("admin email: %+v", got)
	}
	if len(mailTo(sink, outsider.email)) != 0 || len(mailTo(sink, asker.email)) != 0 {
		t.Fatal("email reached someone the request does not wait on")
	}

	// A second request inside the window waits: one email per kind per 15 minutes.
	wantTS(t, "second request", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, second.key), 201, "")
	if need := findNeed(needsYouOf(t, a, slug, admin), "join_request"); need == nil || need["count"] != float64(2) || need["text"] != "2 people asked to join Alpha." {
		t.Fatalf("two requests: %v", need)
	}
	sweepNow(a)
	if got := mailTo(sink, admin.email); len(got) != 1 {
		t.Fatalf("second email inside the window: %d", len(got))
	}
	// Once the window has passed, the waiting line goes out on its own.
	if _, err := a.database.Exec(`UPDATE event_notices SET emailed_at = emailed_at - interval '16 minutes'
		WHERE user_id = $1 AND email_state = 'sent'`, a.uid(t, admin)); err != nil {
		t.Fatal(err)
	}
	sweepNow(a)
	got = mailTo(sink, admin.email)
	if len(got) != 2 || !strings.Contains(got[1].body, "Cy asked to join Alpha.") || strings.Contains(got[1].body, "Bea") {
		t.Fatalf("batched follow-up: %+v", got)
	}

	// A request withdrawn before the sweep sends nothing.
	if _, err := a.database.Exec(`UPDATE event_notices SET emailed_at = emailed_at - interval '16 minutes'
		WHERE user_id = $1 AND email_state = 'sent'`, a.uid(t, admin)); err != nil {
		t.Fatal(err)
	}
	var reqID string
	if err := a.database.QueryRow(`SELECT id FROM event_team_requests WHERE user_id = $1`, a.uid(t, second)).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "withdraw", a.api(t, "DELETE", base+"/requests/"+reqID, nil, second.key), 200, "")
	wantTS(t, "ask Keys", a.api(t, "POST", base+"/teams/"+keyTeam+"/requests", nil, second.key), 201, "")
	var cyAgain string
	if err := a.database.QueryRow(`SELECT id FROM event_team_requests WHERE user_id = $1`, a.uid(t, second)).Scan(&cyAgain); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "withdraw again", a.api(t, "DELETE", base+"/requests/"+cyAgain, nil, second.key), 200, "")
	wantTS(t, "ask Alpha again", a.api(t, "POST", base+"/teams/"+team+"/requests", nil, second.key), 201, "")
	if err := a.database.QueryRow(`SELECT id FROM event_team_requests WHERE user_id = $1`, a.uid(t, second)).Scan(&reqID); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "withdraw before mail", a.api(t, "DELETE", base+"/requests/"+reqID, nil, second.key), 200, "")
	sweepNow(a)
	if got := mailTo(sink, admin.email); len(got) != 2 {
		t.Fatalf("withdrawn request was emailed: %+v", got[len(got)-1])
	}

	// A key-only account still sees the line, and is never emailed.
	wantTS(t, "ask key team", a.api(t, "POST", base+"/teams/"+keyTeam+"/requests", nil, second.key), 201, "")
	if need := findNeed(needsYouOf(t, a, slug, keyOnly), "join_request"); need == nil || need["text"] != "Cy asked to join Keys." {
		t.Fatalf("key-only line: %v", need)
	}
	var queued int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_notices WHERE user_id = $1 AND email_state <> 'none'`, a.uid(t, keyOnly)).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("key-only queued: %d %v", queued, err)
	}
	sweepNow(a)
	if got := mailTo(sink, keyOnly.email); len(got) != 0 {
		t.Fatalf("key-only emailed: %+v", got)
	}

	// The admin declines Bea: Bea hears it in the app and by email, and can dismiss the line.
	var beaReq string
	if err := a.database.QueryRow(`SELECT id FROM event_team_requests WHERE user_id = $1`, a.uid(t, asker)).Scan(&beaReq); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "decline", a.api(t, "POST", base+"/requests/"+beaReq+"/decline", nil, admin.key), 200, "")
	answer := findNeed(needsYouOf(t, a, slug, asker), "request_answered")
	if answer == nil || answer["text"] != "Alpha said no to your request to join." || answer["dismissable"] != true {
		t.Fatalf("answer: %v", answer)
	}
	if findNeed(needsYouOf(t, a, slug, admin), "join_request") != nil {
		t.Fatal("answered request still waits")
	}
	sweepNow(a)
	if got := mailTo(sink, asker.email); len(got) != 1 || !strings.Contains(got[0].body, "Alpha said no") {
		t.Fatalf("answer email: %+v", got)
	}
	wantTS(t, "dismiss someone else's", a.api(t, "POST", base+"/notices/seen", map[string]any{"ids": []string{answer["id"].(string)}}, admin.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, asker), "request_answered") == nil {
		t.Fatal("another person dismissed Bea's line")
	}
	r := a.api(t, "POST", base+"/notices/seen", map[string]any{"ids": []string{answer["id"].(string)}}, asker.key)
	if r.status != 200 || r.json(t)["marked"] != float64(1) {
		t.Fatalf("dismiss: %d %s", r.status, r.body)
	}
	if findNeed(needsYouOf(t, a, slug, asker), "request_answered") != nil {
		t.Fatal("dismissed line still shows")
	}
	wantTS(t, "bad id", a.api(t, "POST", base+"/notices/seen", map[string]any{"ids": []string{"nope"}}, asker.key), 400, "invalid_id")

	// An invite reaches the invited person; GET /v1/hack/todos lists it across events.
	wantTS(t, "looking", a.api(t, "PUT", base+"/me/looking", map[string]any{"looking": true, "intro": "Hi"}, asker.key), 200, "")
	wantTS(t, "invite", a.api(t, "POST", base+"/team/invites", map[string]any{"user_id": a.uid(t, asker)}, admin.key), 201, "")
	todos := a.api(t, "GET", "/v1/hack/todos", nil, asker.key).json(t)
	evs, _ := todos["events"].([]any)
	if len(evs) != 1 || todos["email_updates"] != true || todos["email_possible"] != true {
		t.Fatalf("todos: %v", todos)
	}
	first := evs[0].(map[string]any)
	items := first["items"].([]any)
	if first["slug"] != slug || len(items) != 1 || items[0].(map[string]any)["text"] != "Alpha invited you to join." {
		t.Fatalf("todos items: %v", first)
	}
	keyTodos := a.api(t, "GET", "/v1/hack/todos", nil, keyOnly.key).json(t)
	if keyTodos["email_possible"] != false {
		t.Fatalf("key-only todos: %v", keyTodos)
	}

	// Turning email off drops what is queued, and queues nothing new.
	wantTS(t, "email off", a.api(t, "PATCH", "/v1/hack/preferences", map[string]any{"email_updates": false}, asker.key), 200, "")
	prefs := a.api(t, "GET", "/v1/hack/preferences", nil, asker.key).json(t)
	if prefs["email_updates"] != false || prefs["email_possible"] != true {
		t.Fatalf("prefs: %v", prefs)
	}
	if _, err := a.database.Exec(`UPDATE event_notices SET emailed_at = emailed_at - interval '16 minutes'
		WHERE user_id = $1 AND email_state = 'sent'`, a.uid(t, asker)); err != nil {
		t.Fatal(err)
	}
	sweepNow(a)
	if got := mailTo(sink, asker.email); len(got) != 1 {
		t.Fatalf("opted-out person emailed: %+v", got)
	}

	// A lock is news in the app only, for everyone but whoever locked it.
	wantTS(t, "accept invite", a.api(t, "POST", base+"/requests/"+findNeedID(t, a, slug, asker)+"/accept", nil, asker.key), 200, "")
	wantTS(t, "lock", a.api(t, "PUT", base+"/team/lock", map[string]any{"locked": true}, admin.key), 200, "")
	if line := findNeed(needsYouOf(t, a, slug, asker), "team_locked"); line == nil || line["text"] != "Alpha is locked, so nobody new can join." {
		t.Fatalf("lock line: %v", line)
	}
	if findNeed(needsYouOf(t, a, slug, admin), "team_locked") != nil {
		t.Fatal("the locker got their own lock line")
	}
	var lockMail int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_notices WHERE kind = 'team_locked' AND team_id::text IN (SELECT id::text FROM event_teams WHERE slug = $1) AND email_state <> 'none'`, team).Scan(&lockMail); err != nil || lockMail != 0 {
		t.Fatalf("lock queued email: %d %v", lockMail, err)
	}
	wantTS(t, "unlock", a.api(t, "PUT", base+"/team/lock", map[string]any{"locked": false}, admin.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, asker), "team_locked") != nil {
		t.Fatal("unlock left the lock line")
	}

	// Made admin: a line while it holds, gone once it no longer does.
	wantTS(t, "make admin", a.api(t, "PUT", base+"/team/members/"+a.uid(t, asker)+"/admin", map[string]any{"admin": true}, admin.key), 200, "")
	if line := findNeed(needsYouOf(t, a, slug, asker), "team_admin"); line == nil || !strings.HasPrefix(line["text"].(string), "You're now an admin of Alpha.") {
		t.Fatalf("admin line: %v", line)
	}
	wantTS(t, "unmake admin", a.api(t, "PUT", base+"/team/members/"+a.uid(t, asker)+"/admin", map[string]any{"admin": false}, admin.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, asker), "team_admin") != nil {
		t.Fatal("stale admin line")
	}

	// Removal reaches the person by email with the notice's own words.
	wantTS(t, "email on", a.api(t, "PATCH", "/v1/hack/preferences", map[string]any{"email_updates": true}, asker.key), 200, "")
	wantTS(t, "remove", a.api(t, "DELETE", base+"/team/members/"+a.uid(t, asker), nil, admin.key), 204, "")
	sweepNow(a)
	got = mailTo(sink, asker.email)
	if len(got) != 2 || !strings.Contains(got[1].body, "A teammate took you off Alpha.") {
		t.Fatalf("removal email: %+v", got)
	}

	// Leaving the event takes every notice of theirs with it.
	wantTS(t, "leave", a.api(t, "POST", base+"/leave", map[string]any{"confirm": slug}, asker.key), 200, "")
	var left int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_notices n JOIN events e ON e.id = n.event_id WHERE e.slug = $1 AND n.user_id = $2`, slug, a.uid(t, asker)).Scan(&left); err != nil || left != 0 {
		t.Fatalf("notices after leaving: %d %v", left, err)
	}
}

func findNeedID(t *testing.T, a *teamSiteApp, slug string, p person) string {
	t.Helper()
	r := a.api(t, "GET", "/v1/hack/events/"+slug+"/teams/open", nil, p.key)
	for _, v := range r.json(t)["pending"].([]any) {
		if m := v.(map[string]any); m["kind"] == "invite" {
			return m["id"].(string)
		}
	}
	t.Fatal("no pending invite")
	return ""
}

func TestHackWaitNoticesApplicationsAndTeamChanges(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "waitappsorg")
	applicant := a.newPerson(t, "waitapplicant")
	mate := a.newPerson(t, "waitmate")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, mate, org, "Mo")
	wantTS(t, "approval on", a.api(t, "PUT", base+"/registration", map[string]any{"questions": []any{}, "approval_required": true}, org.key), 200, "")
	waitJoin(t, a, slug, applicant, org, "Secret Name")

	need := findNeed(needsYouOf(t, a, slug, org), "application_new")
	if need == nil || need["count"] != float64(1) || need["text"] != "1 application is waiting for a decision." || need["section"] != "people" {
		t.Fatalf("organiser needs_you: %v", need)
	}
	if items := needsYouOf(t, a, slug, applicant); len(items) != 0 {
		t.Fatalf("pending applicant needs_you: %v", items)
	}
	sweepNow(a)
	got := mailTo(sink, org.email)
	if len(got) != 1 || !strings.Contains(got[0].body, "1 new application is waiting for a decision.") || strings.Contains(got[0].body, "Secret Name") ||
		strings.Contains(got[0].body, applicant.email) || !strings.Contains(got[0].body, "/manage?s=people") {
		t.Fatalf("organiser email: %+v", got)
	}

	wantTS(t, "approve", a.api(t, "POST", base+"/applications/"+a.uid(t, applicant)+"/decision", map[string]any{"decision": "approved"}, org.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, org), "application_new") != nil {
		t.Fatal("decided application still waits")
	}
	line := findNeed(needsYouOf(t, a, slug, applicant), "application_decided")
	if line == nil || !strings.HasPrefix(line["text"].(string), "Your application was approved.") {
		t.Fatalf("decision line: %v", line)
	}
	sweepNow(a)
	if got := mailTo(sink, applicant.email); len(got) != 1 || !strings.Contains(got[0].body, "Your application was approved.") || !strings.Contains(got[0].subject, "your application has an answer") {
		t.Fatalf("decision email: %+v", got)
	}

	// Team changes by the organiser reach everyone on the team, never another team.
	team, _ := a.startTeam(t, slug, "Gamma", applicant)
	other, _ := a.startTeam(t, slug, "Delta", mate)
	wantTS(t, "takedown", a.api(t, "POST", base+"/teams/"+team+"/takedown", map[string]any{"reason": "Broken link to a scam"}, org.key), 200, "")
	down := findNeed(needsYouOf(t, a, slug, applicant), "site_taken_down")
	if down == nil || down["text"] != "The organisers took your team's site down. Their reason: Broken link to a scam" || down["section"] != "website" {
		t.Fatalf("takedown line: %v", down)
	}
	if findNeed(needsYouOf(t, a, slug, mate), "site_taken_down") != nil {
		t.Fatal("takedown reached another team")
	}
	sweepNow(a)
	if got := mailTo(sink, applicant.email); len(got) != 2 || !strings.Contains(got[1].body, "Broken link to a scam") {
		t.Fatalf("takedown email: %+v", got)
	}
	if got := mailTo(sink, mate.email); len(got) != 0 {
		t.Fatalf("other team emailed: %+v", got)
	}
	wantTS(t, "restore", a.api(t, "POST", base+"/teams/"+team+"/restore", nil, org.key), 200, "")
	items := needsYouOf(t, a, slug, applicant)
	if findNeed(items, "site_taken_down") != nil || findNeed(items, "site_restored") == nil {
		t.Fatalf("restore lines: %v", items)
	}

	// An extension: a line with the new time, and an email.
	wantTS(t, "event deadline", a.api(t, "PATCH", base, map[string]any{"submission_deadline": "2030-01-01T10:00"}, org.key), 200, "")
	wantTS(t, "extend", a.api(t, "PUT", base+"/teams/"+team+"/deadline", map[string]any{"deadline": "2030-01-02T10:00"}, org.key), 200, "")
	ext := findNeed(needsYouOf(t, a, slug, applicant), "deadline_changed")
	if ext == nil || !strings.Contains(ext["text"].(string), "Your team has until Wed 2 Jan 2030, 10:00 am") || ext["section"] != "entry" {
		t.Fatalf("extension line: %v", ext)
	}
	if findNeed(needsYouOf(t, a, slug, mate), "deadline_changed") != nil {
		t.Fatal("extension reached another team")
	}
	_ = other
	var pending int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_notices WHERE kind = 'deadline_changed' AND user_id = $1 AND email_state = 'pending'`, a.uid(t, applicant)).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("extension email queued: %d %v", pending, err)
	}
	// Taken back before it was ever seen or emailed: nothing at all.
	wantTS(t, "clear", a.api(t, "PUT", base+"/teams/"+team+"/deadline", map[string]any{"deadline": ""}, org.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, applicant), "deadline_changed") != nil {
		t.Fatal("a reversed, never-seen extension still shows")
	}
	if err := a.database.QueryRow(`SELECT count(*) FROM event_notices WHERE kind = 'deadline_changed' AND user_id = $1 AND email_state = 'pending'`, a.uid(t, applicant)).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("reversed extension still queued: %d %v", pending, err)
	}
}

// newKeyOnlyPersonTS is hackApp.newKeyOnlyPerson for the team-site harness.
func (a *teamSiteApp) newKeyOnlyPersonTS(t *testing.T, label string) person {
	t.Helper()
	h := &hackApp{database: a.database}
	return h.newKeyOnlyPerson(t, label)
}
