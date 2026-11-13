package servertest

import (
	"net/http"
	"strings"
	"testing"
)

// SIM-443: a team member writes to the event's organisers. One thread per
// team, shared by every organiser; the Home notice is a to-do that goes away
// when the thread is opened; email goes only to organisers whose "Email me
// when something waits on me" is on.
func TestHackTeamToOrganisers(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	a.noticeSink = sink
	org := a.newPerson(t, "t2oorg")
	orgQuiet := a.newPerson(t, "t2oquiet")
	lead := a.newPerson(t, "t2olead")
	mate := a.newPerson(t, "t2omate")
	other := a.newPerson(t, "t2oother")
	lone := a.newPerson(t, "t2olone")
	judge := a.newPerson(t, "t2ojudge")
	outsider := a.newPerson(t, "t2ooutsider")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug

	// A second organiser who turned emails off.
	if _, err := a.database.Exec(`INSERT INTO event_members (event_id, user_id, role, coc_accepted_at) SELECT id, $2, 'organiser', now() FROM events WHERE slug = $1`, slug, a.uid(t, orgQuiet)); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "opt out", a.api(t, "PATCH", "/v1/hack/preferences", map[string]any{"email_updates": false}, orgQuiet.key), 200, "")

	waitJoin(t, a, slug, lead, org, "Lea")
	waitJoin(t, a, slug, mate, org, "Mat")
	waitJoin(t, a, slug, other, org, "Oth")
	waitJoin(t, a, slug, lone, org, "Lon")
	joinAsJudge(t, a, slug, org, judge, "Jud")
	alphaTeam, alphaCode := a.startTeam(t, slug, "Alpha", lead)
	betaTeam, _ := a.startTeam(t, slug, "Beta", other)
	wantTS(t, "join alpha", a.api(t, "POST", base+"/teams/join", map[string]any{"code": alphaCode}, mate.key), 200, "")

	// Who may write: members of a team only.
	wantCode(t, "no team", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "hi"}, lone.key), http.StatusConflict, "no_team")
	wantCode(t, "organiser", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "hi"}, org.key), http.StatusNotFound, "event_not_found")
	wantCode(t, "judge", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "hi"}, judge.key), http.StatusNotFound, "event_not_found")
	wantCode(t, "outsider", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "hi"}, outsider.key), http.StatusNotFound, "event_not_found")
	wantCode(t, "empty", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "  "}, lead.key), http.StatusBadRequest, "invalid_text")
	wantCode(t, "too long", a.api(t, "POST", base+"/team/messages", map[string]any{"text": strings.Repeat("x", 1001)}, lead.key), http.StatusBadRequest, "invalid_text")
	// Nothing was written by any refusal.
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_team_messages WHERE from_team AND event_id = (SELECT id FROM events WHERE slug = $1)`, slug).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows after refusals: %d %v", rows, err)
	}

	r := a.api(t, "POST", base+"/team/messages", map[string]any{"text": "We need a bigger table."}, mate.key)
	wantTS(t, "message organisers", r, http.StatusCreated, "")
	sent := r.json(t)["message"].(map[string]any)
	if sent["from_team"] != true || sent["sender_name"] != "Mat" || sent["team_name"] != "Alpha" || sent["recipients"] != float64(2) {
		t.Fatalf("sent: %v", sent)
	}

	// Both organisers see it on Home, linking to the team's thread.
	for _, o := range []person{org, orgQuiet} {
		need := findNeed(needsYouOf(t, a, slug, o), "team_inbound")
		if need == nil || need["text"] != "Alpha wrote to the organisers: We need a bigger table." || need["section"] != "team" || need["team"] != alphaTeam || need["dismissable"] != false {
			t.Fatalf("organiser needs_you: %v", need)
		}
	}
	// Dismissing news does not clear it: only opening the thread does.
	wantTS(t, "dismiss all", a.api(t, "POST", base+"/notices/seen", nil, org.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, org), "team_inbound") == nil {
		t.Fatal("dismissing news cleared the team's message line")
	}
	// Nobody else sees it as a notice.
	for _, p := range []person{lead, mate, other, judge} {
		if findNeed(needsYouOf(t, a, slug, p), "team_inbound") != nil {
			t.Fatal("a non-organiser got the organiser notice")
		}
	}

	// The thread: both teammates and every organiser read it; another team and non-organisers cannot.
	for _, who := range []struct {
		name string
		path string
		p    person
	}{{"org", base + "/teams/" + alphaTeam + "/messages", org}, {"quiet org", base + "/teams/" + alphaTeam + "/messages", orgQuiet}, {"lead", base + "/team/messages", lead}, {"mate", base + "/team/messages", mate}} {
		got := a.api(t, "GET", who.path, nil, who.p.key).json(t)["messages"].([]any)
		if len(got) != 1 || got[0].(map[string]any)["text"] != "We need a bigger table." || got[0].(map[string]any)["from_team"] != true {
			t.Fatalf("%s thread: %v", who.name, got)
		}
	}
	if got := a.api(t, "GET", base+"/team/messages", nil, other.key).json(t)["messages"].([]any); len(got) != 0 {
		t.Fatalf("another team reads Alpha's thread: %v", got)
	}
	if got := a.api(t, "GET", base+"/team/messages", nil, lone.key).json(t)["messages"].([]any); len(got) != 0 {
		t.Fatalf("a person with no team reads a thread: %v", got)
	}
	for _, p := range []person{lead, mate, other, lone, judge, outsider} {
		wantCode(t, "organiser view", a.api(t, "GET", base+"/teams/"+alphaTeam+"/messages", nil, p.key), http.StatusNotFound, "event_not_found")
	}
	// Opening the thread cleared the Home line for the organisers who opened it.
	for _, o := range []person{org, orgQuiet} {
		if findNeed(needsYouOf(t, a, slug, o), "team_inbound") != nil {
			t.Fatal("notice still on Home after opening the thread")
		}
	}

	// Email: only the organiser with emails on.
	sweepNow(a)
	if got := mailTo(sink, org.email); len(got) != 1 || !strings.Contains(got[0].subject, "a team wrote to the organisers") ||
		!strings.Contains(got[0].body, "Alpha wrote to the organisers: We need a bigger table.") || !strings.Contains(got[0].body, "/e/"+slug+"/manage?s=teams") {
		t.Fatalf("organiser email: %+v", got)
	}
	if got := mailTo(sink, orgQuiet.email); len(got) != 0 {
		t.Fatalf("opted-out organiser emailed: %+v", got)
	}
	for _, p := range []person{lead, mate, other, judge} {
		for _, m := range mailTo(sink, p.email) {
			if strings.Contains(m.subject, "wrote to the organisers") {
				t.Fatalf("non-organiser emailed the organiser notice: %+v", m)
			}
		}
	}

	// The organiser's reply reaches the team (and only that team); it follows
	// each teammate's own opt-in.
	alphaID := teamIDFor(t, a, slug, alphaTeam)
	wantTS(t, "reply", a.api(t, "POST", base+"/team-messages", map[string]any{"team_ids": []string{alphaID}, "text": "Table found."}, org.key), http.StatusCreated, "")
	if got := a.api(t, "GET", base+"/team/messages", nil, lead.key).json(t)["messages"].([]any); len(got) != 2 {
		t.Fatalf("thread after reply: %v", got)
	}
	if findNeed(needsYouOf(t, a, slug, mate), "team_message") == nil {
		t.Fatal("teammate did not get the reply")
	}
	if findNeed(needsYouOf(t, a, slug, other), "team_message") != nil {
		t.Fatal("another team got the reply")
	}
	// The reply does not count against the teams' own limit and the team's
	// messages do not count against the organisers' (50/hour).
	_ = betaTeam

	// A member who left the team can no longer write to the organisers.
	wantTS(t, "leave", a.api(t, "POST", base+"/teams/leave", nil, mate.key), 200, "")
	wantCode(t, "left", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "still here?"}, mate.key), http.StatusConflict, "no_team")

	// Rate limit: 10 per team per hour.
	for i := 1; i < 10; i++ {
		wantTS(t, "fill", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "filler"}, lead.key), http.StatusCreated, "")
	}
	limited := a.api(t, "POST", base+"/team/messages", map[string]any{"text": "one too many"}, lead.key)
	wantCode(t, "rate limited", limited, http.StatusTooManyRequests, "rate_limited")
	if limited.header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After header")
	}
	// Another team is not affected.
	wantTS(t, "other team", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "hello from Beta"}, other.key), http.StatusCreated, "")

	// Home groups by team and counts.
	var alphaNeed, betaNeed map[string]any
	for _, n := range needsYouOf(t, a, slug, orgQuiet) {
		if n["kind"] != "team_inbound" {
			continue
		}
		switch n["team"] {
		case alphaTeam:
			alphaNeed = n
		case betaTeam:
			betaNeed = n
		}
	}
	if alphaNeed == nil || alphaNeed["count"] != float64(9) || !strings.HasPrefix(alphaNeed["text"].(string), "Alpha sent 9 messages to the organisers. Latest: filler") {
		t.Fatalf("grouped Alpha need: %v", alphaNeed)
	}
	if betaNeed == nil || betaNeed["text"] != "Beta wrote to the organisers: hello from Beta" {
		t.Fatalf("Beta need: %v", betaNeed)
	}

	// Unread team messages never block a stage move, and the move does not
	// clear them.
	wantTS(t, "stage building", a.api(t, "POST", base+"/stage", map[string]string{"stage": "building"}, org.key), 200, "")
	if findNeed(needsYouOf(t, a, slug, orgQuiet), "team_inbound") == nil {
		t.Fatal("stage move cleared the organiser's unread team messages")
	}

	// Once the event has ended nobody writes to the organisers.
	wantTS(t, "archive", a.api(t, "POST", base+"/stage", map[string]string{"stage": "archived"}, org.key), 200, "")
	wantCode(t, "ended", a.api(t, "POST", base+"/team/messages", map[string]any{"text": "late"}, other.key), http.StatusConflict, "event_closed")
}
