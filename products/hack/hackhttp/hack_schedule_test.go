package hackhttp

import (
	"strings"
	"testing"
	"time"
)

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
