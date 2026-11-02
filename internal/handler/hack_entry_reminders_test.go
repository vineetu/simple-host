package handler

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Entry deadline reminders (entries slice 3).
func TestHackEntryReminders(t *testing.T) {
	a := newTeamSiteApp(t)
	sink := &contentMailSink{}
	a.hack.SetMailer(sink)
	org := a.newPerson(t, "remindorg")
	lead := a.newPerson(t, "remindlead")
	optedOut := a.newPerson(t, "remindoptout")
	keyOnly := a.newKeyOnlyPersonTS(t, "remindkey")
	done := a.newPerson(t, "reminddone")
	idle := a.newPerson(t, "remindidle")
	slug := a.makeEvent(t, org)
	base := "/v1/hack/events/" + slug
	waitJoin(t, a, slug, lead, org, "Lea")
	waitJoin(t, a, slug, optedOut, org, "Oz")
	waitJoin(t, a, slug, keyOnly, org, "Kay")
	waitJoin(t, a, slug, done, org, "Dee")
	waitJoin(t, a, slug, idle, org, "Ida")
	team, code := a.startTeam(t, slug, "Alpha", lead)
	for _, p := range []person{optedOut, keyOnly} {
		wantTS(t, "join team", a.api(t, "POST", base+"/teams/join", map[string]any{"code": code}, p.key), 200, "")
	}
	doneTeam, _ := a.startTeam(t, slug, "Done", done)
	a.startTeam(t, slug, "Idle", idle)
	wantTS(t, "email off", a.api(t, "PATCH", "/v1/hack/preferences", map[string]any{"email_updates": false}, optedOut.key), 200, "")

	// Alpha: an incomplete draft. Done: a submitted entry. Idle: never started.
	wantTS(t, "draft", a.api(t, "PUT", base+"/entry", map[string]any{"title": "Alpha thing"}, lead.key), 200, "")
	complete := map[string]any{"title": "Done thing", "problem": "A problem", "tagline": "A tagline", "audience": "Everyone",
		"tools": []string{"Go"}, "maturity": "working", "video_url": "https://www.youtube.com/watch?v=dQw4w9WgXcQ"}
	wantTS(t, "complete", a.api(t, "PUT", base+"/entry", complete, done.key), 200, "")
	wantTS(t, "submit", a.api(t, "POST", base+"/entry/submit", nil, done.key), 200, "")
	_ = doneTeam

	// Two days out: nothing yet.
	if _, err := a.database.Exec(`UPDATE events SET stage = 'building', submission_deadline = now() + interval '2 days' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	a.hack.queueEntryReminders(context.Background())
	sweepNow(a)
	if got := mailTo(sink, lead.email); len(got) != 0 {
		t.Fatalf("reminded two days out: %+v", got)
	}

	// Inside 24 hours: one reminder each for Alpha and Idle, none for Done,
	// none to the opted-out member or the key-only account.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() + interval '5 hours' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	a.hack.queueEntryReminders(context.Background())
	a.hack.queueEntryReminders(context.Background()) // idempotent
	sweepNow(a)
	got := mailTo(sink, lead.email)
	if len(got) != 1 || !strings.Contains(got[0].subject, "your entry is due soon") ||
		!strings.Contains(got[0].body, "Your team's entry isn't submitted yet.") ||
		!strings.Contains(got[0].body, "in about 5 hours") ||
		!strings.Contains(got[0].body, "Still missing: the problem it solves") ||
		!strings.Contains(got[0].body, "still judged, marked incomplete") ||
		!strings.Contains(got[0].body, "/e/"+slug+"/team?s=entry") || !strings.Contains(got[0].body, "/account") {
		t.Fatalf("lead reminder: %+v", got)
	}
	if got := mailTo(sink, idle.email); len(got) != 1 || !strings.Contains(got[0].body, "hasn't started its entry") {
		t.Fatalf("idle reminder: %+v", got)
	}
	if n := len(mailTo(sink, done.email)); n != 0 {
		t.Fatalf("submitted team reminded: %d", n)
	}
	if n := len(mailTo(sink, optedOut.email)); n != 0 {
		t.Fatalf("opted-out member emailed: %d", n)
	}
	for _, n := range sink.notices {
		if strings.Contains(n.to, "remindkey") {
			t.Fatalf("key-only account emailed: %+v", n)
		}
	}
	var rows int
	if err := a.database.QueryRow(`SELECT count(*) FROM event_entry_reminders r JOIN events e ON e.id = r.event_id WHERE e.slug = $1`, slug).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("reminder rows: %d %v", rows, err)
	}

	// The last hour: the 1-hour reminder goes out once the 15-minute window allows.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() + interval '50 minutes' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE event_notices SET emailed_at = emailed_at - interval '16 minutes' WHERE kind = 'entry_reminder' AND email_state = 'sent' AND event_id = (SELECT id FROM events WHERE slug = $1)`, slug); err != nil {
		t.Fatal(err)
	}
	a.hack.queueEntryReminders(context.Background())
	sweepNow(a)
	got = mailTo(sink, lead.email)
	if len(got) != 2 || !strings.Contains(got[1].body, "in about an hour") {
		t.Fatalf("1-hour reminder: %+v", got)
	}

	// Submitted before the email goes out: the queued reminder is dropped.
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = now() + interval '20 hours' WHERE slug = $1`, slug); err != nil {
		t.Fatal(err)
	}
	a.hack.queueEntryReminders(context.Background()) // a moved deadline: the 24h reminder may go once more
	wantTS(t, "fill", a.api(t, "PUT", base+"/entry", complete, lead.key), 200, "")
	wantTS(t, "submit alpha", a.api(t, "POST", base+"/entry/submit", nil, lead.key), 200, "")
	if _, err := a.database.Exec(`UPDATE event_notices SET emailed_at = emailed_at - interval '16 minutes' WHERE kind = 'entry_reminder' AND email_state = 'sent' AND event_id = (SELECT id FROM events WHERE slug = $1)`, slug); err != nil {
		t.Fatal(err)
	}
	sweepNow(a)
	if got := mailTo(sink, lead.email); len(got) != 2 {
		t.Fatalf("reminder after submit: %+v", got)
	}
	_ = team
}

func TestReminderIn(t *testing.T) {
	cases := map[string]string{"30m": "in less than an hour", "70m": "in about an hour", "5h10m": "in about 5 hours", "23h50m": "in about 24 hours"}
	for in, want := range cases {
		d, _ := time.ParseDuration(in)
		if got := reminderIn(d); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}
