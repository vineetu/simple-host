package servertest

import (
	"context"
	"github.com/vsriram/simple-host/products/hack/public"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/webassets"
)

// TestHackScheduleActsAPI: GET /content returns fixed_schedule, a fixed row
// written back is dropped, prompts are validated and kept for organisers but
// hidden from participants, and the organiser's next step carries the cue
// without the stage moving.
func TestHackScheduleActsAPI(t *testing.T) {
	a := newFeatApp(t)
	org, participant := a.NewPerson(t, "sched-org"), a.NewPerson(t, "sched-part")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	base := "/v1/hack/events/" + slug
	// SIM-382: a brand-new event's next_step is entry_rules until the
	// organiser saves a choice; this test is about the schedule cue on the
	// Open sign-up step, so get past that first step like the website would.
	hacktest.WantTS(t, "save entry rules", a.At(t, "PUT", base+"/entry-rules", map[string]any{"preset": "video_or_website"}, a.Key(org)), 200, "")
	kickoff := time.Now().Add(-5 * time.Minute).UTC()
	content := map[string]any{
		"sponsors": []any{}, "faq": []any{},
		"schedule": []map[string]any{
			{"title": "Kickoff", "start_at": kickoff.Format(time.RFC3339), "end_at": kickoff.Add(time.Hour).Format(time.RFC3339), "prompt": "open"},
			{"title": "Entries due", "start_at": kickoff.Format(time.RFC3339), "end_at": kickoff.Add(time.Hour).Format(time.RFC3339), "fixed": true},
		},
	}
	r := a.At(t, "PUT", base+"/content", content, a.Key(org))
	if r.Status != 200 {
		t.Fatalf("put: %d %s", r.Status, r.Body)
	}
	got := r.JSON(t)
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
	hacktest.WantTS(t, "bad prompt", a.At(t, "PUT", base+"/content", bad, a.Key(org)), 400, "invalid_schedule_prompt")

	// The next step asks, and nothing moves.
	view := a.At(t, "GET", base, nil, a.Key(org)).JSON(t)
	step := view["next_step"].(map[string]any)
	cue, _ := step["cue"].(map[string]any)
	if cue == nil || cue["text"] != "Kickoff is now. Open sign-up?" || cue["now"] != true {
		t.Fatalf("cue: %v", step)
	}
	if view["event"].(map[string]any)["stage"] != "draft" {
		t.Fatalf("the schedule moved the stage")
	}

	a.OpenEvent(t, org, slug)
	view = a.At(t, "GET", base, nil, a.Key(org)).JSON(t)
	if step := view["next_step"].(map[string]any); step["stage"] != "building" || step["cue"] != nil {
		t.Fatalf("after opening, the kickoff cue should go: %v", step)
	}

	// An entry deadline shows as a fixed row with set_in entries.
	deadline := time.Now().Add(48 * time.Hour).UTC().Truncate(time.Minute)
	if r := a.At(t, "PATCH", base, map[string]string{"submission_deadline": deadline.Format(time.RFC3339)}, a.Key(org)); r.Status != 200 {
		t.Fatalf("deadline: %d %s", r.Status, r.Body)
	}
	join, _ := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, participant, "Pat")
	pr := a.At(t, "GET", base+"/content", nil, a.Key(participant))
	if pr.Status != 200 || strings.Contains(string(pr.Body), `"prompt"`) {
		t.Fatalf("participant content leaks prompt: %d %s", pr.Status, pr.Body)
	}
	var found bool
	for _, f := range pr.JSON(t)["fixed_schedule"].([]any) {
		row := f.(map[string]any)
		if row["key"] == "entry_deadline" {
			found = row["start_at"] == deadline.Format(time.RFC3339) && row["set_in"] == "entries" && row["end_at"] == nil
		}
	}
	if !found {
		t.Fatalf("entry deadline row missing or wrong: %s", pr.Body)
	}

	// The public feed carries the fixed rows too, without prompts.
	pub := a.At(t, "GET", "/v1/hack/events/"+slug+"/public", nil, nil)
	if pub.Status == 200 {
		if strings.Contains(string(pub.Body), `"prompt"`) || !strings.Contains(string(pub.Body), `"fixed_schedule"`) {
			t.Fatalf("public feed: %s", pub.Body)
		}
	}
}

func TestHackSubmissions(t *testing.T) {
	w := newFeatEntryWorld(t)
	a := w.a
	path := "/v1/hack/events/" + w.Slug
	pages := public.NewHackEventPages(webassets.Static)
	public.RegisterHackPublic(a.Mux, a.DB, a.Srv.URL, nil, nil, pages)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.DB.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	stage := func(s string) { exec(`UPDATE events SET stage=$2 WHERE slug=$1`, w.Slug, s) }
	get := func(p string, who hacktest.Person) hacktest.Resp { return a.At(t, "GET", p, nil, a.Key(who)) }
	patch := func(on bool) {
		t.Helper()
		r := a.At(t, "PATCH", path, map[string]any{"submissions_public": on}, a.Key(w.Org))
		if r.Status != 200 {
			t.Fatalf("patch: %d %s", r.Status, r.Body)
		}
	}
	if get(path, w.Org).JSON(t)["event"].(map[string]any)["submissions_public"] != false {
		t.Fatal("must default off")
	}
	for _, p := range []hacktest.Person{w.P1, w.P2} {
		r := a.At(t, "PUT", w.EntryPath(), map[string]any{"title": "A project"}, a.Key(p))
		if r.Status != 200 {
			t.Fatalf("entry: %s", r.Body)
		}
	}
	exec(`UPDATE event_entries SET submitted_at=now(), screenshot=$2, screenshot_type='image/png' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.Slug, []byte("test image"))
	for _, s := range []string{"building", "closed", "judging", "results", "archived"} {
		stage(s)
		r := get(path+"/submissions", w.Lone)
		want := 200
		if s == "building" || s == "closed" {
			want = 409
		}
		if r.Status != want {
			t.Fatalf("%s: %d %s", s, r.Status, r.Body)
		}
		if want == 409 {
			if r.JSON(t)["code"] != "submissions_not_open" {
				t.Fatal(string(r.Body))
			}
			continue
		}
		if len(r.JSON(t)["submissions"].([]any)) != 2 {
			t.Fatal(string(r.Body))
		}
		for _, key := range []string{"score", "rank", "comments", "missing", "excluded"} {
			if strings.Contains(string(r.Body), `"`+key+`"`) {
				t.Fatalf("leaked %s", key)
			}
		}
		allowed := map[string]bool{"title": true, "tagline": true, "problem": true, "audience": true, "tools": true, "maturity": true, "track": true, "video_url": true, "video_host": true, "code_url": true, "code_host": true, "site_url": true, "has_screenshot": true, "team_name": true, "team": true, "screenshot_url": true}
		for _, row := range r.JSON(t)["submissions"].([]any) {
			for key := range row.(map[string]any) {
				if !allowed[key] {
					t.Fatalf("unexpected card field %q", key)
				}
			}
		}
		if r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatalf("cache: %q", r.Header.Get("Cache-Control"))
		}
	}
	stage("judging")
	for _, p := range []hacktest.Person{w.P1, w.P2, w.Lone} {
		if r := get(path+"/submissions", p); r.Status != 200 {
			t.Fatal(string(r.Body))
		}
	}
	outsider := a.NewPerson(t, "outsider")
	if r := get(path+"/submissions", outsider); r.Status == 200 {
		t.Fatal("non-member allowed")
	}
	pending := a.NewPerson(t, "pending")
	exec(`INSERT INTO event_members (event_id,user_id,role,display_name,approval_status) SELECT id,$2,'participant','Pending person','pending' FROM events WHERE slug=$1`, w.Slug, a.UserID(t, pending))
	if r := get(path+"/submissions", pending); r.Status != 403 {
		t.Fatalf("pending applicant: %d %s", r.Status, r.Body)
	}
	if r := get(path+"/submissions/"+w.Alpha+"/screenshot", pending); r.Status != 403 {
		t.Fatalf("pending applicant screenshot: %d", r.Status)
	}
	if r := a.At(t, "PATCH", path, map[string]any{"submissions_public": true}, a.Key(w.P1)); r.Status == 200 {
		t.Fatal("participant changed the setting")
	}
	if r := a.At(t, "GET", path+"/submissions", nil, nil); r.Status == 200 {
		t.Fatal("anonymous allowed")
	}
	shot := "/e/" + w.Slug + "/submissions/" + w.Alpha + "/screenshot"
	if r := a.At(t, "GET", shot, nil, nil); r.Status != 404 {
		t.Fatalf("off shot: %d", r.Status)
	}
	if r := a.At(t, "GET", path+"/public", nil, nil); len(r.JSON(t)["submissions"].([]any)) != 0 {
		t.Fatal("off feed")
	}
	patch(true)
	if get(path, w.Org).JSON(t)["event"].(map[string]any)["submissions_public"] != true {
		t.Fatal("patch did not round-trip")
	}
	for _, s := range []string{"building", "closed", "judging", "results", "archived"} {
		stage(s)
		r := a.At(t, "GET", path+"/public", nil, nil)
		rows := r.JSON(t)["submissions"].([]any)
		want := 2
		shotStatus := 200
		if s == "building" || s == "closed" {
			want = 0
			shotStatus = 404
		}
		if len(rows) != want {
			t.Fatalf("public %s: %s", s, r.Body)
		}
		r = a.At(t, "GET", shot, nil, nil)
		if r.Status != shotStatus {
			t.Fatalf("shot %s: %d %s", s, r.Status, r.Body)
		}
		if shotStatus == 200 && r.Header.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
			t.Fatal("cross-origin image blocked")
		}
		if want > 0 && !strings.HasPrefix(rows[0].(map[string]any)["screenshot_url"].(string), a.Srv.URL+"/e/") {
			t.Fatal("screenshot URL must be absolute")
		}
	}
	stage("judging")
	if r := get(path+"/submissions/"+w.Alpha+"/screenshot", w.P1); r.Status != 200 {
		t.Fatal("participant screenshot unavailable")
	}
	var accountID string
	if err := a.DB.QueryRow(`SELECT account_id FROM events WHERE slug=$1`, w.Slug).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO sites (user_id,name,site_url,active_version) VALUES ($1,$2,'https://example.test/',1)`, accountID, w.Alpha)
	ev, err := a.hack.Events.EventBySlug(context.Background(), w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	cards, err := public.VisibleSubmissions(context.Background(), a.DB, ev, hacktest.SiteStub{}.TeamSiteURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 || cards[0].Team != w.Alpha || cards[0].SiteURL == "" {
		t.Fatalf("live site missing: %+v", cards)
	}
	a.hack.SetSites(hacktest.SiteStub{})
	exec(`UPDATE event_teams SET site_taken_down_at=now() WHERE event_id=$1 AND slug=$2`, ev.ID, w.Alpha)
	r := get(path+"/submissions", w.P1)
	if r.JSON(t)["submissions"].([]any)[0].(map[string]any)["site_url"] != "" {
		t.Fatal("taken-down site linked")
	}
	if r := a.At(t, "GET", "/e/"+w.Slug, nil, nil); r.Status != 200 || !strings.Contains(string(r.Body), `id="submissions"`) || !strings.Contains(string(r.Body), `href="#submissions"`) {
		t.Fatalf("submissions page: %d %s", r.Status, r.Body)
	}
	patch(false)
	if r := a.At(t, "GET", "/e/"+w.Slug, nil, nil); strings.Contains(string(r.Body), `id="submissions"`) {
		t.Fatal("off page shows submissions")
	}
	patch(true)
	for _, state := range []string{"draft", "not_submitted", "excluded"} {
		exec(`UPDATE event_entries SET submitted_at=NULL, submit_settled_at=NULL, excluded_at=NULL WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.Slug, w.Alpha)
		if state == "not_submitted" {
			exec(`UPDATE event_entries SET submit_settled_at=now() WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.Slug, w.Alpha)
		}
		if state == "excluded" {
			exec(`UPDATE event_entries SET submitted_at=now(),excluded_at=now() WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.Slug, w.Alpha)
		}
		r := get(path+"/submissions", w.P2)
		if len(r.JSON(t)["submissions"].([]any)) != 1 {
			t.Fatalf("%s: %s", state, r.Body)
		}
		if feed := a.At(t, "GET", path+"/public", nil, nil); len(feed.JSON(t)["submissions"].([]any)) != 1 {
			t.Fatalf("hidden public %s: %s", state, feed.Body)
		}
		if r := a.At(t, "GET", shot, nil, nil); r.Status != 404 {
			t.Fatalf("hidden screenshot %s", state)
		}
		if r := get(path+"/submissions/"+w.Alpha+"/screenshot", w.P1); r.Status != 404 || r.JSON(t)["code"] != "team_not_found" {
			t.Fatal("hidden participant screenshot")
		}
	}
	exec(`UPDATE event_entries SET submitted_at=now(), excluded_at=NULL WHERE team_id=(SELECT id FROM event_teams WHERE event_id=(SELECT id FROM events WHERE slug=$1) AND slug=$2)`, w.Slug, w.Alpha)
	if r := a.At(t, "GET", shot, nil, nil); r.Status != 200 || r.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("restored screenshot: %d %q", r.Status, r.Header.Get("Cache-Control"))
	}
	if r := get(path+"/submissions/"+w.Alpha+"/screenshot", w.P1); r.Status != 200 {
		t.Fatal("restored participant screenshot")
	}
	exec(`UPDATE events SET taken_down_at=now() WHERE slug=$1`, w.Slug)
	if r := get(path+"/submissions/"+w.Alpha+"/screenshot", w.P1); r.Status == 200 {
		t.Fatal("taken-down participant screenshot")
	}
	if r := a.At(t, "GET", path+"/public", nil, nil); r.Status == 200 {
		t.Fatal("taken-down feed")
	}
	if r := get(path+"/submissions", w.P1); r.Status == 200 {
		t.Fatal("taken-down event exposed")
	}
	if r := a.At(t, "GET", shot, nil, nil); r.Status != 404 {
		t.Fatal("taken-down screenshot")
	}
}
