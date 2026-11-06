package hackhttp

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/model"
)

func nt(t time.Time) sql.NullTime { return sql.NullTime{Time: t, Valid: true} }

func fixedKeys(rows []hackFixedRow) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Key)
	}
	return out
}

// TestHackFixedScheduleRows: the event's real times become fixed rows, in
// time order, marked fixed, each naming where it is set. Prompts reach only
// organisers, a date-only start or end is all day and prompts nothing, and a
// participant with an extension sees their own deadline instead.
func TestHackFixedScheduleRows(t *testing.T) {
	now := time.Date(2027, 3, 13, 12, 0, 0, 0, time.UTC)
	ev := model.Event{TimeZone: "Europe/London", Stage: "building", GalleryOpen: true,
		StartsAt:           nt(time.Date(2027, 3, 12, 9, 0, 0, 0, time.UTC)),
		EndsAt:             nt(time.Date(2027, 3, 14, 0, 0, 0, 0, time.UTC)), // midnight London: date only
		SubmissionDeadline: nt(time.Date(2027, 3, 14, 17, 0, 0, 0, time.UTC)),
	}
	voting := model.VotingSettings{Enabled: true, OpensAt: nt(time.Date(2027, 3, 14, 18, 0, 0, 0, time.UTC)), ClosesAt: nt(time.Date(2027, 3, 14, 20, 0, 0, 0, time.UTC))}
	results := entries.EventResultsRow{Published: true, PublishedAt: nt(time.Date(2027, 3, 15, 10, 0, 0, 0, time.UTC))}
	rows := hackFixedSchedule(hackFixedInputs{Event: ev, Voting: &voting, Results: results, Organiser: true, Now: now})
	if got := strings.Join(fixedKeys(rows), ","); got != "event_start,entry_deadline,voting,event_end,results_published" {
		t.Fatalf("rows/order = %s", got)
	}
	for _, r := range rows {
		if !r.Fixed || r.SetIn == "" {
			t.Errorf("%s: fixed=%v set_in=%q", r.Key, r.Fixed, r.SetIn)
		}
	}
	byKey := map[string]hackFixedRow{}
	for _, r := range rows {
		byKey[r.Key] = r
	}
	if r := byKey["event_start"]; r.AllDay || r.Prompt != "building" || r.SetIn != "basics" {
		t.Errorf("timed start: %+v", r)
	}
	if r := byKey["event_end"]; !r.AllDay || r.Prompt != "" {
		t.Errorf("date-only end should be all day with no prompt: %+v", r)
	}
	if r := byKey["entry_deadline"]; r.Prompt != "closed" || r.SetIn != "entries" || r.EndAt != nil {
		t.Errorf("deadline: %+v", r)
	}
	if r := byKey["voting"]; r.EndAt == nil || *r.EndAt != "2027-03-14T20:00:00Z" || r.SetIn != "results" || r.Prompt != "" {
		t.Errorf("voting: %+v", r)
	}

	public := hackFixedSchedule(hackFixedInputs{Event: ev, Voting: &voting, Results: results, Now: now})
	for _, r := range public {
		if r.Prompt != "" {
			t.Errorf("public row %s carries a prompt %q", r.Key, r.Prompt)
		}
	}
	b, _ := json.Marshal(public)
	if strings.Contains(string(b), `"prompt"`) {
		t.Errorf("public JSON names prompt: %s", b)
	}

	team := model.TeamWriteState{HasOverride: true, Deadline: nt(time.Date(2027, 3, 14, 19, 0, 0, 0, time.UTC))}
	mine := hackFixedSchedule(hackFixedInputs{Event: ev, Team: &team, Now: now})
	if got := strings.Join(fixedKeys(mine), ","); got != "event_start,team_deadline,event_end" {
		t.Fatalf("participant rows = %s", got)
	}
	if mine[1].StartAt != "2027-03-14T19:00:00Z" || mine[1].Title != "Entries due for your team" {
		t.Errorf("team deadline row: %+v", mine[1])
	}

	// No deadline: a timed event end prompts closing entries instead.
	ev2 := ev
	ev2.SubmissionDeadline = sql.NullTime{}
	ev2.EndsAt = nt(time.Date(2027, 3, 14, 18, 0, 0, 0, time.UTC))
	for _, r := range hackFixedSchedule(hackFixedInputs{Event: ev2, Organiser: true, Now: now}) {
		if r.Key == "event_end" && r.Prompt != "closed" {
			t.Errorf("timed end with no deadline should prompt closed: %+v", r)
		}
	}
	// Voting off, or with the gallery closed, adds no row.
	off := model.VotingSettings{}
	ev3 := ev
	ev3.GalleryOpen = false
	for _, in := range []hackFixedInputs{{Event: ev, Voting: &off, Now: now}, {Event: ev3, Voting: &voting, Now: now}} {
		for _, r := range hackFixedSchedule(in) {
			if r.Key == "voting" {
				t.Errorf("voting row with voting disabled: %+v", r)
			}
		}
	}
}

// TestHackScheduleCue: the next step picks up the latest row whose time has
// come and that prompts this very move, and words it the website's way.
func TestHackScheduleCue(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	now := time.Date(2027, 3, 12, 10, 5, 0, 0, loc)
	at := func(h, m int) string { return time.Date(2027, 3, 12, h, m, 0, 0, loc).UTC().Format(time.RFC3339) }
	openStep := map[string]any{"kind": "stage", "stage": "open", "label": "Open sign-up", "blocked": nil}
	free := []hackScheduleItem{
		{Title: "Kickoff", StartAt: at(10, 0), EndAt: at(11, 0), Prompt: "open"},
		{Title: "Warm-up", StartAt: at(9, 0), EndAt: at(9, 30), Prompt: "open"},
		{Title: "Lunch", StartAt: at(12, 0), EndAt: at(13, 0), Prompt: "open"},
		{Title: "Demos", StartAt: at(8, 0), EndAt: at(9, 0), Prompt: "judging"},
	}
	cue := hackScheduleCue(openStep, free, nil, loc, now)
	if cue == nil || cue["text"] != "Kickoff is now. Open sign-up?" || cue["now"] != true || cue["fixed"] != false || cue["key"] != "" {
		t.Fatalf("kickoff cue: %v", cue)
	}
	// After the window, it still asks, saying when it was.
	later := time.Date(2027, 3, 12, 11, 30, 0, 0, loc)
	if cue := hackScheduleCue(openStep, free[:1], nil, loc, later); cue == nil || cue["text"] != "Kickoff was at 10:00 am. Open sign-up?" || cue["now"] != false {
		t.Fatalf("past cue: %v", cue)
	}
	// Nothing due yet, a different move, or a blocked step: no cue.
	if cue := hackScheduleCue(openStep, free[2:3], nil, loc, now); cue != nil {
		t.Fatalf("future row cued: %v", cue)
	}
	if cue := hackScheduleCue(map[string]any{"kind": "stage", "stage": "building", "blocked": nil}, free, nil, loc, now); cue != nil {
		t.Fatalf("other move cued: %v", cue)
	}
	if cue := hackScheduleCue(map[string]any{"kind": "stage", "stage": "archived", "blocked": "archive_needs_participants"}, free, nil, loc, now); cue != nil {
		t.Fatalf("blocked step cued: %v", cue)
	}
	if cue := hackScheduleCue(map[string]any{"kind": "none"}, free, nil, loc, now); cue != nil {
		t.Fatalf("no step cued: %v", cue)
	}

	// The fixed entry deadline: "Entries are due now", then "were due at".
	closeStep := map[string]any{"kind": "stage", "stage": "closed", "label": "Close entries", "blocked": nil}
	deadline := []hackFixedRow{{Key: "entry_deadline", Title: "Entries due", StartAt: at(10, 0), Fixed: true, Prompt: "closed"}}
	if cue := hackScheduleCue(closeStep, nil, deadline, loc, now); cue == nil || cue["text"] != "Entries are due now. Close entries?" || cue["fixed"] != true || cue["key"] != "entry_deadline" || cue["end_at"] != nil {
		t.Fatalf("deadline cue: %v", cue)
	}
	nextDay := time.Date(2027, 3, 13, 9, 0, 0, 0, loc)
	if cue := hackScheduleCue(closeStep, nil, deadline, loc, nextDay); cue == nil || cue["text"] != "Entries were due at Fri 12 Mar, 10:00 am. Close entries?" {
		t.Fatalf("deadline next day: %v", cue)
	}
	// An all-day row never cues.
	allDay := []hackFixedRow{{Key: "event_start", Title: "Event starts", StartAt: at(0, 0), AllDay: true, Fixed: true, Prompt: "building"}}
	if cue := hackScheduleCue(map[string]any{"kind": "stage", "stage": "building", "blocked": nil}, nil, allDay, loc, now); cue != nil {
		t.Fatalf("all-day row cued: %v", cue)
	}
	// Publish results lines up too.
	pub := []hackScheduleItem{{Title: "Awards", StartAt: at(10, 0), EndAt: at(10, 30), Prompt: "publish_results"}}
	if cue := hackScheduleCue(map[string]any{"kind": "publish_results", "stage": "", "blocked": nil}, pub, nil, loc, now); cue == nil || cue["text"] != "Awards is now. Publish results?" {
		t.Fatalf("publish cue: %v", cue)
	}
}

// TestHackScheduleViewsMergesFixedRows: the public page lists fixed rows
// with the typed ones, a moment with one time, a date-only end after the
// rest of its day.
func TestHackScheduleViewsMergesFixedRows(t *testing.T) {
	p := testHackEvent()
	p.TimeZone = "UTC"
	p.Content.Schedule = []hackScheduleItem{
		{Title: "Demos", StartAt: "2027-03-14T16:00:00Z", EndAt: "2027-03-14T17:00:00Z"},
	}
	p.Fixed = []hackFixedRow{
		{Key: "event_end", Title: "Event ends", StartAt: "2027-03-14T23:59:00Z", Fixed: true},
		{Key: "entry_deadline", Title: "Entries due", StartAt: "2027-03-14T15:00:00Z", Fixed: true},
	}
	body := renderHackEvent(t, p).Body.String()
	iDeadline, iDemos, iEnd := strings.Index(body, "Entries due"), strings.Index(body, "Demos"), strings.Index(body, "Event ends")
	if iDeadline < 0 || iDemos < 0 || iEnd < 0 || !(iDeadline < iDemos && iDemos < iEnd) {
		t.Fatalf("order deadline<demos<end broken: %d %d %d", iDeadline, iDemos, iEnd)
	}
	if !strings.Contains(body, "Sun 14 Mar, 3:00 pm UTC") {
		t.Errorf("deadline moment not shown with one time")
	}
	if !strings.Contains(body, `<span class="when">Sun 14 Mar</span>`) {
		t.Errorf("all-day end not shown as a date")
	}
}

// TestHackScheduleActsAPI: GET /content returns fixed_schedule, a fixed row
// written back is dropped, prompts are validated and kept for organisers but
// hidden from participants, and the organiser's next step carries the cue
// without the stage moving.
func TestHackScheduleActsAPI(t *testing.T) {
	a := newHackApp(t)
	org, participant := a.newPerson(t, "sched-org"), a.newPerson(t, "sched-part")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	// SIM-382: a brand-new event's next_step is entry_rules until the
	// organiser saves a choice; this test is about the schedule cue on the
	// Open sign-up step, so get past that first step like the website would.
	wantTS(t, "save entry rules", a.at(t, "PUT", base+"/entry-rules", map[string]any{"preset": "video_or_website"}, a.key(org)), 200, "")
	kickoff := time.Now().Add(-5 * time.Minute).UTC()
	content := map[string]any{
		"sponsors": []any{}, "faq": []any{},
		"schedule": []map[string]any{
			{"title": "Kickoff", "start_at": kickoff.Format(time.RFC3339), "end_at": kickoff.Add(time.Hour).Format(time.RFC3339), "prompt": "open"},
			{"title": "Entries due", "start_at": kickoff.Format(time.RFC3339), "end_at": kickoff.Add(time.Hour).Format(time.RFC3339), "fixed": true},
		},
	}
	r := a.at(t, "PUT", base+"/content", content, a.key(org))
	if r.status != 200 {
		t.Fatalf("put: %d %s", r.status, r.body)
	}
	got := r.json(t)
	if sched := got["schedule"].([]any); len(sched) != 1 || sched[0].(map[string]any)["prompt"] != "open" {
		t.Fatalf("fixed row not dropped or prompt lost: %v", got["schedule"])
	}
	fixed := got["fixed_schedule"].([]any)
	if len(fixed) != 2 { // the create body's date-only start and end
		t.Fatalf("fixed_schedule: %v", fixed)
	}
	for _, f := range fixed {
		row := f.(map[string]any)
		if row["fixed"] != true || row["all_day"] != true || row["set_in"] != "basics" {
			t.Errorf("fixed row: %v", row)
		}
	}
	bad := map[string]any{"schedule": []map[string]any{{"title": "X", "start_at": "2027-01-01T10:00", "end_at": "2027-01-01T11:00", "prompt": "archived"}}}
	wantTS(t, "bad prompt", a.at(t, "PUT", base+"/content", bad, a.key(org)), 400, "invalid_schedule_prompt")

	// The next step asks, and nothing moves.
	view := a.at(t, "GET", base, nil, a.key(org)).json(t)
	step := view["next_step"].(map[string]any)
	cue, _ := step["cue"].(map[string]any)
	if cue == nil || cue["text"] != "Kickoff is now. Open sign-up?" || cue["now"] != true {
		t.Fatalf("cue: %v", step)
	}
	if view["event"].(map[string]any)["stage"] != "draft" {
		t.Fatalf("the schedule moved the stage")
	}

	a.openEvent(t, org, slug)
	view = a.at(t, "GET", base, nil, a.key(org)).json(t)
	if step := view["next_step"].(map[string]any); step["stage"] != "building" || step["cue"] != nil {
		t.Fatalf("after opening, the kickoff cue should go: %v", step)
	}

	// An entry deadline shows as a fixed row with set_in entries.
	deadline := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Minute)
	if r := a.at(t, "PATCH", base, map[string]string{"submission_deadline": deadline.Format(time.RFC3339)}, a.key(org)); r.status != 200 {
		t.Fatalf("deadline: %d %s", r.status, r.body)
	}
	join, _ := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, participant, "Pat")
	pr := a.at(t, "GET", base+"/content", nil, a.key(participant))
	if pr.status != 200 || strings.Contains(string(pr.body), `"prompt"`) {
		t.Fatalf("participant content leaks prompt: %d %s", pr.status, pr.body)
	}
	var found bool
	for _, f := range pr.json(t)["fixed_schedule"].([]any) {
		row := f.(map[string]any)
		if row["key"] == "entry_deadline" {
			found = row["start_at"] == deadline.Format(time.RFC3339) && row["set_in"] == "entries" && row["end_at"] == nil
		}
	}
	if !found {
		t.Fatalf("entry deadline row missing or wrong: %s", pr.body)
	}

	// The public feed carries the fixed rows too, without prompts.
	pub := a.at(t, "GET", "/v1/hack/events/"+slug+"/public", nil, nil)
	if pub.status == 200 {
		if strings.Contains(string(pub.body), `"prompt"`) || !strings.Contains(string(pub.body), `"fixed_schedule"`) {
			t.Fatalf("public feed: %s", pub.body)
		}
	}
}
