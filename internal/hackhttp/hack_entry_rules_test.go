package hackhttp

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

// Entry rules (slice 1 of the entries and participant journey package,
// build-plan.md): the rules engine (hack_entry_rules.go), the new PUT entry
// fields and submit/unsubmit, the deadline settle sweep, judge eligibility
// over a settled-incomplete or hand-excluded entry, the rules PUT (presets,
// dry run, the deprecated entry_required alias) and the entries CSV/counts.
//
// The harness is newHackApp (DB_DSN); see hack_test.go and
// newHackEntryWorld (hack_entries_test.go) for the pattern this file follows.

// --- HTTP tests: a small world builder, separate from newHackEntryWorld --

type rulesTeam struct {
	person person
	slug   string
}

type rulesWorld struct {
	a     *hackApp
	slug  string
	org   person
	judge person
	teams []rulesTeam
}

// newRulesWorld makes an event (default video_or_website preset, no tracks), an
// organiser, a judge, and nTeams participants each already on their own
// solo team.
func newRulesWorld(t *testing.T, nTeams int) *rulesWorld {
	t.Helper()
	a := newHackApp(t)
	org := a.newPerson(t, "ruo")
	judge := a.newPerson(t, "ruj")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	ev := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)
	orgView := ev["organiser"].(map[string]any)
	join := orgView["join_code"].(string)
	jcode := orgView["judge_code"].(string)
	if r := a.at(t, "POST", "/v1/hack/judge/"+jcode, map[string]any{"accept_coc": true, "display_name": "Jude"}, a.key(judge)); r.status != 200 {
		t.Fatalf("judge join: %d %s", r.status, r.body)
	}
	w := &rulesWorld{a: a, slug: slug, org: org, judge: judge}
	for i := 0; i < nTeams; i++ {
		p := a.newPerson(t, "rup")
		name := fmt.Sprintf("Rules Team %c", 'A'+i)
		if r := a.at(t, "POST", "/v1/hack/join/"+join, map[string]any{"accept_coc": true, "display_name": name}, a.key(p)); r.status != 200 {
			t.Fatalf("join %s: %d %s", name, r.status, r.body)
		}
		tr := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.key(p))
		if tr.status != http.StatusCreated {
			t.Fatalf("team %s: %d %s", name, tr.status, tr.body)
		}
		w.teams = append(w.teams, rulesTeam{person: p, slug: tr.json(t)["slug"].(string)})
	}
	return w
}

func (w *rulesWorld) entryPath() string { return "/v1/hack/events/" + w.slug + "/entry" }

func (w *rulesWorld) teamID(t *testing.T, slug string) string {
	t.Helper()
	var id string
	if err := w.a.database.QueryRow(`
		SELECT id FROM event_teams WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND slug = $2`, w.slug, slug).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// buildCompleteFields are the fields that satisfy the default "video_or_website"
// preset with no event tracks: everything required, plus video_url for the
// one_of group.
func buildCompleteFields() map[string]any {
	return map[string]any{
		"title": "A Title", "problem": "A problem", "tagline": "A tagline", "audience": "Everyone",
		"tools": []string{"Go"}, "maturity": "working", "video_url": "https://example.com/v",
	}
}

// --- PUT entry: new field limits ------------------------------------------

func TestHackEntryNewFieldLimits(t *testing.T) {
	w := newRulesWorld(t, 1)
	a, p := w.a, w.teams[0].person

	// problem: checkHackText(0, 140).
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"problem": strings.Repeat("a", 140)}, a.key(p)); r.status != 200 {
		t.Fatalf("problem 140: %d %s", r.status, r.body)
	}
	wantCode(t, "problem 141", a.at(t, "PUT", w.entryPath(), map[string]string{"problem": strings.Repeat("a", 141)}, a.key(p)), 400, "invalid_problem")

	// audience: checkHackText(0, 100).
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"audience": strings.Repeat("a", 100)}, a.key(p)); r.status != 200 {
		t.Fatalf("audience 100: %d %s", r.status, r.body)
	}
	wantCode(t, "audience 101", a.at(t, "PUT", w.entryPath(), map[string]string{"audience": strings.Repeat("a", 101)}, a.key(p)), 400, "invalid_audience")

	// Control characters are refused on the new text fields.
	wantCode(t, "problem control", a.at(t, "PUT", w.entryPath(), map[string]string{"problem": "bad\x01char"}, a.key(p)), 400, "invalid_problem")
	wantCode(t, "audience control", a.at(t, "PUT", w.entryPath(), map[string]string{"audience": "bad\x01char"}, a.key(p)), 400, "invalid_audience")

	// tools: dedupe case-insensitively (first spelling kept), cap of 10.
	r := a.at(t, "PUT", w.entryPath(), map[string]any{"tools": []string{"Claude", "claude", "Figma"}}, a.key(p))
	if r.status != 200 {
		t.Fatalf("tools dedupe: %d %s", r.status, r.body)
	}
	got := jsonStrings(t, r.json(t)["entry"].(map[string]any)["tools"])
	if !slices.Equal(got, []string{"Claude", "Figma"}) {
		t.Fatalf("tools dedupe = %v", got)
	}
	ten := make([]string, 10)
	for i := range ten {
		ten[i] = fmt.Sprintf("tool%d", i)
	}
	if r := a.at(t, "PUT", w.entryPath(), map[string]any{"tools": ten}, a.key(p)); r.status != 200 {
		t.Fatalf("tools cap 10: %d %s", r.status, r.body)
	}
	eleven := append(ten, "tool10")
	wantCode(t, "tools cap 11", a.at(t, "PUT", w.entryPath(), map[string]any{"tools": eleven}, a.key(p)), 400, "invalid_tools")

	// maturity enum.
	for _, v := range []string{"working", "partial", "concept", ""} {
		if r := a.at(t, "PUT", w.entryPath(), map[string]string{"maturity": v}, a.key(p)); r.status != 200 {
			t.Fatalf("maturity %q: %d %s", v, r.status, r.body)
		}
	}
	wantCode(t, "maturity invalid", a.at(t, "PUT", w.entryPath(), map[string]string{"maturity": "finished"}, a.key(p)), 400, "invalid_maturity")

	// track: unknown slug, or a track from another event, both 4xx.
	other := newRulesWorld(t, 0)
	if r := other.a.at(t, "PUT", "/v1/hack/events/"+other.slug+"/tracks", map[string]any{
		"tracks": []map[string]string{{"slug": "ai", "name": "AI"}},
	}, other.a.key(other.org)); r.status != 200 {
		t.Fatalf("other tracks: %d %s", r.status, r.body)
	}
	wantCode(t, "unknown track", a.at(t, "PUT", w.entryPath(), map[string]string{"track": "no-such-track"}, a.key(p)), 404, "track_not_found")
	wantCode(t, "cross-event track", a.at(t, "PUT", w.entryPath(), map[string]string{"track": "ai"}, a.key(p)), 404, "track_not_found")
	wantCode(t, "malformed track", a.at(t, "PUT", w.entryPath(), map[string]string{"track": "Not Valid!"}, a.key(p)), 400, "invalid_track")

	// A track that does exist on this event writes event_teams.track_id.
	if r := a.at(t, "PUT", "/v1/hack/events/"+w.slug+"/tracks", map[string]any{
		"tracks": []map[string]string{{"slug": "mine", "name": "Mine"}},
	}, a.key(w.org)); r.status != 200 {
		t.Fatalf("own tracks: %d %s", r.status, r.body)
	}
	r = a.at(t, "PUT", w.entryPath(), map[string]string{"track": "mine"}, a.key(p))
	if r.status != 200 {
		t.Fatalf("set track: %d %s", r.status, r.body)
	}
	track := r.json(t)["entry"].(map[string]any)["track"].(map[string]any)
	if track["slug"] != "mine" {
		t.Fatalf("track not applied: %#v", r.json(t)["entry"])
	}
	var trackID string
	if err := a.database.QueryRow(`SELECT track_id FROM event_teams WHERE id = $1`, w.teamID(t, w.teams[0].slug)).Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	if trackID == "" {
		t.Fatal("event_teams.track_id not written")
	}
}

// --- Draft writes never check rules; a submitted save that would become
// incomplete is refused and changes nothing. --------------------------------

func TestHackEntryDraftNeverChecksRulesSubmittedCannotRegress(t *testing.T) {
	w := newRulesWorld(t, 1)
	a, p := w.a, w.teams[0].person

	// A draft save that leaves the entry wildly incomplete is still 200.
	r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Only a title"}, a.key(p))
	if r.status != 200 {
		t.Fatalf("draft incomplete save: %d %s", r.status, r.body)
	}
	if r.json(t)["complete"] != false {
		t.Fatalf("draft should read incomplete: %#v", r.json(t))
	}

	// Complete it and submit.
	if r := a.at(t, "PUT", w.entryPath(), buildCompleteFields(), a.key(p)); r.status != 200 {
		t.Fatalf("complete: %d %s", r.status, r.body)
	}
	r = a.at(t, "POST", "/v1/hack/events/"+w.slug+"/entry/submit", map[string]any{}, a.key(p))
	if r.status != 200 || r.json(t)["status"] != "submitted" {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}

	// Removing a required line on a submitted entry is refused, unchanged.
	r = a.at(t, "PUT", w.entryPath(), map[string]string{"problem": ""}, a.key(p))
	wantCode(t, "regress submitted", r, 422, "entry_would_be_incomplete")
	missing := jsonStrings(t, r.json(t)["missing"])
	if !slices.Equal(missing, []string{"problem"}) {
		t.Fatalf("missing = %v", missing)
	}
	after := a.at(t, "GET", w.entryPath(), nil, a.key(p)).json(t)
	if after["status"] != "submitted" || after["entry"].(map[string]any)["problem"] != "A problem" {
		t.Fatalf("rejected write changed the entry: %#v", after)
	}
}

// --- Submit / unsubmit ------------------------------------------------------

func TestHackEntrySubmitUnsubmit(t *testing.T) {
	w := newRulesWorld(t, 1)
	a, p := w.a, w.teams[0].person
	submitPath := "/v1/hack/events/" + w.slug + "/entry/submit"
	unsubmitPath := "/v1/hack/events/" + w.slug + "/entry/unsubmit"

	// Incomplete: 422 entry_incomplete with missing.
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "T"}, a.key(p)); r.status != 200 {
		t.Fatalf("title: %d %s", r.status, r.body)
	}
	r := a.at(t, "POST", submitPath, map[string]any{}, a.key(p))
	wantCode(t, "incomplete submit", r, 422, "entry_incomplete")
	if len(jsonStrings(t, r.json(t)["missing"])) == 0 {
		t.Fatalf("missing empty: %#v", r.json(t))
	}

	// Complete, submit: 200, then idempotent.
	if r := a.at(t, "PUT", w.entryPath(), buildCompleteFields(), a.key(p)); r.status != 200 {
		t.Fatalf("complete: %d %s", r.status, r.body)
	}
	first := a.at(t, "POST", submitPath, map[string]any{}, a.key(p))
	if first.status != 200 || first.json(t)["status"] != "submitted" || first.json(t)["submit_auto"] != false {
		t.Fatalf("submit: %d %s", first.status, first.body)
	}
	firstAt := first.json(t)["submitted_at"]
	second := a.at(t, "POST", submitPath, map[string]any{}, a.key(p))
	if second.status != 200 || second.json(t)["submitted_at"] != firstAt {
		t.Fatalf("idempotent submit changed submitted_at: %v -> %v", firstAt, second.json(t)["submitted_at"])
	}

	// A non-member (a stranger to the event) is refused.
	stranger := a.newPerson(t, "rus")
	wantCode(t, "stranger submit", a.at(t, "POST", submitPath, map[string]any{}, a.key(stranger)), 404, "event_not_found")

	// Unsubmit: back to draft.
	un := a.at(t, "POST", unsubmitPath, nil, a.key(p))
	if un.status != 200 || un.json(t)["status"] != "draft" {
		t.Fatalf("unsubmit: %d %s", un.status, un.body)
	}

	// Submit again, then the deadline passes: 409.
	if r := a.at(t, "POST", submitPath, map[string]any{}, a.key(p)); r.status != 200 {
		t.Fatalf("resubmit: %d %s", r.status, r.body)
	}
	if _, err := a.database.Exec(`UPDATE event_teams SET deadline_override = NULL WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 minute' WHERE slug = $1`, w.slug); err != nil {
		t.Fatal(err)
	}
	wantCode(t, "unsubmit after deadline", a.at(t, "POST", unsubmitPath, nil, a.key(p)), 409, "submissions_closed")

	// A team extension (after the event deadline) reopens the write path.
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	ext := a.at(t, "PUT", "/v1/hack/events/"+w.slug+"/teams/"+w.teams[0].slug+"/deadline", map[string]string{"deadline": future}, a.key(w.org))
	if ext.status != 200 {
		t.Fatalf("extension: %d %s", ext.status, ext.body)
	}
	un = a.at(t, "POST", unsubmitPath, nil, a.key(p))
	if un.status != 200 || un.json(t)["status"] != "draft" {
		t.Fatalf("unsubmit after extension: %d %s", un.status, un.body)
	}
}

// TestHackEntryProgressVideoWebsiteCodeCountsEveryRequiredEvidenceField:
// Codex review round 1, finding 3. EntryProgress's evidence step used to
// filter evidenceCheck.Missing down to "one_of" and "code_url" only --
// right for every preset before SIM-382, where video_url/site were never
// individually required (only one_of or off). video_website_code requires
// video_url, site and code_url individually (no one_of group), so a team
// with only a code link used to read evidence "Done" on Home (and `next`
// would skip straight to "submit") while POST .../entry/submit still
// correctly refused it with `missing` -- Home and the real completeness
// check disagreed. Fixed by counting every hackentries.OneOfFields member,
// not just code_url.
func TestHackEntryProgressVideoWebsiteCodeCountsEveryRequiredEvidenceField(t *testing.T) {
	w := newRulesWorld(t, 1)
	a, p := w.a, w.teams[0].person
	base := "/v1/hack/events/" + w.slug

	if r := a.at(t, "PUT", base+"/entry-rules", map[string]any{"preset": "video_website_code"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("preset video_website_code: %d %s", r.status, r.body)
	}

	// Every summary field filled (so the summary step is done and can't
	// mask the evidence step in `next`), a code link but no video or
	// website -- video_website_code requires all three.
	fields := buildCompleteFields()
	delete(fields, "video_url")
	fields["code_url"] = "https://example.com/c"
	if r := a.at(t, "PUT", w.entryPath(), fields, a.key(p)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}

	view := a.at(t, "GET", base, nil, a.key(p)).json(t)
	progress := view["progress"].(map[string]any)
	steps := progress["steps"].([]any)
	var evidence map[string]any
	for _, s := range steps {
		step := s.(map[string]any)
		if step["id"] == "evidence" {
			evidence = step
		}
	}
	if evidence == nil {
		t.Fatalf("no evidence step: %#v", progress)
	}
	if evidence["done"] != false {
		t.Fatalf("evidence should not be done with no video or website: %#v", evidence)
	}
	missing := jsonStrings(t, evidence["missing"])
	if !slices.Contains(missing, "video_url") || !slices.Contains(missing, "site") {
		t.Fatalf("evidence missing should name video_url and site: %v", missing)
	}
	if progress["next"] != "evidence" {
		t.Fatalf("next should be evidence, not submit, while video/website are missing: %#v", progress["next"])
	}

	// The real completeness check agrees: submit is still refused.
	r := a.at(t, "POST", base+"/entry/submit", map[string]any{}, a.key(p))
	wantCode(t, "submit with only a code link", r, 422, "entry_incomplete")
	submitMissing := jsonStrings(t, r.json(t)["missing"])
	if !slices.Contains(submitMissing, "video_url") || !slices.Contains(submitMissing, "site") {
		t.Fatalf("submit missing should also name video_url and site: %v", submitMissing)
	}
}

// --- The deadline settle sweep ---------------------------------------------

func TestHackEntrySettleSweep(t *testing.T) {
	w := newRulesWorld(t, 2)
	a := w.a
	complete, incomplete := w.teams[0], w.teams[1]

	if r := a.at(t, "PUT", w.entryPath(), buildCompleteFields(), a.key(complete.person)); r.status != 200 {
		t.Fatalf("complete entry: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Only a title"}, a.key(incomplete.person)); r.status != 200 {
		t.Fatalf("incomplete entry: %d %s", r.status, r.body)
	}

	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 minute' WHERE slug = $1`, w.slug); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	a.hack.SettleDueEntries(ctx)

	cView := a.at(t, "GET", w.entryPath(), nil, a.key(complete.person)).json(t)
	if cView["status"] != "submitted" || cView["submit_auto"] != true {
		t.Fatalf("complete draft did not auto-submit: %#v", cView)
	}
	iView := a.at(t, "GET", w.entryPath(), nil, a.key(incomplete.person)).json(t)
	if iView["status"] != "not_submitted" {
		t.Fatalf("incomplete draft not settled: %#v", iView)
	}
	submittedAt := cView["submitted_at"]

	// Running the sweep again changes nothing (idempotent).
	a.hack.SettleDueEntries(ctx)
	a.hack.SettleDueEntries(ctx)
	cView2 := a.at(t, "GET", w.entryPath(), nil, a.key(complete.person)).json(t)
	if cView2["submitted_at"] != submittedAt {
		t.Fatalf("second sweep moved submitted_at: %v -> %v", submittedAt, cView2["submitted_at"])
	}
	iView2 := a.at(t, "GET", w.entryPath(), nil, a.key(incomplete.person)).json(t)
	if iView2["status"] != "not_submitted" {
		t.Fatalf("second sweep changed settled status: %#v", iView2)
	}

	// An extension unsettles the incomplete team's draft.
	future := time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339)
	ext := a.at(t, "PUT", "/v1/hack/events/"+w.slug+"/teams/"+incomplete.slug+"/deadline", map[string]string{"deadline": future}, a.key(w.org))
	if ext.status != 200 {
		t.Fatalf("extension: %d %s", ext.status, ext.body)
	}
	iView3 := a.at(t, "GET", w.entryPath(), nil, a.key(incomplete.person)).json(t)
	if iView3["status"] != "draft" {
		t.Fatalf("extension did not unsettle: %#v", iView3)
	}
	// And the extended team is unaffected by a further sweep (not frozen yet).
	a.hack.SettleDueEntries(ctx)
	iView4 := a.at(t, "GET", w.entryPath(), nil, a.key(incomplete.person)).json(t)
	if iView4["status"] != "draft" {
		t.Fatalf("sweep re-settled an extended team: %#v", iView4)
	}

	// Moving the event back to building unsettles every remaining
	// settled draft -- do this on a separate, freshly-settled team so the
	// extension above does not confuse the signal.
	if _, err := a.database.Exec(`UPDATE event_teams SET deadline_override = NULL WHERE id = $2 AND event_id = (SELECT id FROM events WHERE slug = $1)`,
		w.slug, w.teamID(t, incomplete.slug)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.database.Exec(`UPDATE events SET submission_deadline = clock_timestamp() - interval '1 minute' WHERE slug = $1`, w.slug); err != nil {
		t.Fatal(err)
	}
	a.hack.SettleDueEntries(ctx)
	iView5 := a.at(t, "GET", w.entryPath(), nil, a.key(incomplete.person)).json(t)
	if iView5["status"] != "not_submitted" {
		t.Fatalf("re-settle after clearing extension: %#v", iView5)
	}
	// setStage's own "back to open/building" branch only fires moving IN
	// from a stage outside {open, building} (hack.go setStage, the
	// "case (stage == open || building) && current != open && current !=
	// building"): the event is still on "open" from newRulesWorld, so it
	// must pass through "closed" first for the building move below to be
	// a real transition that triggers UnsettleEventDrafts.
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "closed"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("close before building: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "building"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("back to building: %d %s", r.status, r.body)
	}
	iView6 := a.at(t, "GET", w.entryPath(), nil, a.key(incomplete.person)).json(t)
	if iView6["status"] != "draft" {
		t.Fatalf("back to building did not unsettle: %#v", iView6)
	}
	// The already-submitted (complete) team is unaffected by any of this.
	cView3 := a.at(t, "GET", w.entryPath(), nil, a.key(complete.person)).json(t)
	if cView3["status"] != "submitted" || cView3["submitted_at"] != submittedAt {
		t.Fatalf("building move touched a submitted entry: %#v", cView3)
	}
}

// --- Judge eligibility over settled-incomplete and hand-excluded entries --

func TestHackJudgeEligibilityIncompleteAndExcluded(t *testing.T) {
	w := newRulesWorld(t, 2)
	a := w.a
	notStarted, incomplete := w.teams[0], w.teams[1]

	// notStarted never writes an entry at all.
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Half"}, a.key(incomplete.person)); r.status != 200 {
		t.Fatalf("incomplete entry: %d %s", r.status, r.body)
	}

	// Closing submissions settles the incomplete draft as not_submitted
	// (still judged) and leaves notStarted with no entry row at all.
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "closed"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("close: %d %s", r.status, r.body)
	}

	queueIDs := func() map[string]map[string]any {
		t.Helper()
		q := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/judge/queue", nil, a.key(w.judge))
		if q.status != 200 {
			t.Fatalf("queue: %d %s", q.status, q.body)
		}
		out := map[string]map[string]any{}
		for _, raw := range q.json(t)["queue"].([]any) {
			item := raw.(map[string]any)
			out[item["team_id"].(string)] = item
		}
		return out
	}

	notStartedID := w.teamID(t, notStarted.slug)
	incompleteID := w.teamID(t, incomplete.slug)

	q := queueIDs()
	if _, ok := q[notStartedID]; ok {
		t.Fatalf("a team with no entry row reached the queue: %#v", q)
	}
	item, ok := q[incompleteID]
	if !ok {
		t.Fatalf("a settled-incomplete entry did not reach the queue: %#v", q)
	}
	if item["incomplete"] != true || item["status"] != "not_submitted" {
		t.Fatalf("incomplete flag not set: %#v", item)
	}

	// Exclude it by hand: out of the queue, and the judge can no longer
	// score it.
	excl := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/teams/"+incomplete.slug+"/entry/exclude", nil, a.key(w.org))
	if excl.status != 200 || excl.json(t)["excluded"] != true {
		t.Fatalf("exclude: %d %s", excl.status, excl.body)
	}
	q = queueIDs()
	if _, ok := q[incompleteID]; ok {
		t.Fatalf("excluded entry still in the queue: %#v", q)
	}
	scoreR := a.at(t, "PUT", "/v1/hack/events/"+w.slug+"/judge/scores/"+incompleteID, map[string]any{"scores": []any{}}, a.key(w.judge))
	if scoreR.status == 200 {
		t.Fatalf("scored an excluded team: %d %s", scoreR.status, scoreR.body)
	}
	// NOTE: the task brief for this suite expected 404 here; the actual
	// handler (hack_judging.go putJudgeScores, "eligible" check) answers
	// 403 forbidden for any team outside the judge's eligible set --
	// unassigned, conflicted, or (since this slice) unsettled/excluded --
	// with no special case for "excluded". Documented in the report, not
	// changed here.
	if scoreR.status != http.StatusForbidden {
		t.Fatalf("score write on excluded team: got %d %s, want 403", scoreR.status, scoreR.body)
	}

	// Include restores it.
	incl := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/teams/"+incomplete.slug+"/entry/include", nil, a.key(w.org))
	if incl.status != 200 || incl.json(t)["excluded"] != false {
		t.Fatalf("include: %d %s", incl.status, incl.body)
	}
	q = queueIDs()
	if _, ok := q[incompleteID]; !ok {
		t.Fatalf("included entry did not return to the queue: %#v", q)
	}
	scoreR2 := a.at(t, "PUT", "/v1/hack/events/"+w.slug+"/judge/scores/"+incompleteID, map[string]any{"scores": []any{}}, a.key(w.judge))
	if scoreR2.status != 200 {
		t.Fatalf("score write after include: %d %s", scoreR2.status, scoreR2.body)
	}
}

// --- Rules PUT: presets, overrides, dry run, closed, the deprecated alias --

func TestHackEntryRulesPresetsAndDryRun(t *testing.T) {
	w := newRulesWorld(t, 3)
	a := w.a
	base := "/v1/hack/events/" + w.slug + "/entry-rules"

	// A new event starts on the video_or_website preset, expanded.
	view := a.at(t, "GET", base, nil, a.key(w.org)).json(t)
	fields := view["fields"].(map[string]any)
	if view["preset"] != "video_or_website" || fields["audience"] != "optional" || fields["code_url"] != "off" {
		t.Fatalf("default video_or_website preset: %#v", view)
	}

	// SIM-382: a removed preset (the three retired names, and custom) is
	// refused when set directly, naming the two current ones.
	for _, bad := range []string{"build", "code", "ideas", "custom"} {
		r := a.at(t, "PUT", base, map[string]any{"preset": bad}, a.key(w.org))
		if r.status != 400 || r.json(t)["code"] != "invalid_preset" {
			t.Fatalf("preset %s should be refused: %d %s", bad, r.status, r.body)
		}
	}

	// Switching preset to "video_website_code" expands its own table: all
	// three show-your-work lines required, not one_of.
	codeR := a.at(t, "PUT", base, map[string]any{"preset": "video_website_code"}, a.key(w.org))
	if codeR.status != 200 {
		t.Fatalf("preset video_website_code: %d %s", codeR.status, codeR.body)
	}
	cFields := codeR.json(t)["fields"].(map[string]any)
	if codeR.json(t)["preset"] != "video_website_code" || cFields["video_url"] != "required" || cFields["site"] != "required" || cFields["code_url"] != "required" {
		t.Fatalf("video_website_code preset: %#v", codeR.json(t))
	}

	// Back to video_or_website, then a field override demotes the stored preset to custom.
	if r := a.at(t, "PUT", base, map[string]any{"preset": "video_or_website"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("back to video_or_website: %d %s", r.status, r.body)
	}
	overR := a.at(t, "PUT", base, map[string]any{"fields": map[string]string{"screenshot": "required"}}, a.key(w.org))
	if overR.status != 200 || overR.json(t)["preset"] != "custom" {
		t.Fatalf("field override -> custom: %d %#v", overR.status, overR.json(t))
	}
	oFields := overR.json(t)["fields"].(map[string]any)
	if oFields["screenshot"] != "required" || oFields["audience"] != "optional" {
		t.Fatalf("override kept the rest of video_or_website: %#v", oFields)
	}

	// preset + a differing field in the same call also stores custom.
	combo := a.at(t, "PUT", base, map[string]any{"preset": "video_or_website", "fields": map[string]string{"description": "required"}}, a.key(w.org))
	if combo.status != 200 || combo.json(t)["preset"] != "custom" {
		t.Fatalf("preset+override -> custom: %d %#v", combo.status, combo.json(t))
	}

	// Set up one submitted and one draft entry for the dry run's impact
	// counts, under the current (combo) rules.
	submitted, draft := w.teams[0], w.teams[1]
	full := buildCompleteFields()
	full["description"] = "has a description"
	if r := a.at(t, "PUT", w.entryPath(), full, a.key(submitted.person)); r.status != 200 {
		t.Fatalf("submitted entry: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/entry/submit", map[string]any{}, a.key(submitted.person)); r.status != 200 {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", w.entryPath(), full, a.key(draft.person)); r.status != 200 {
		t.Fatalf("draft entry: %d %s", r.status, r.body)
	}
	// A draft that is already incomplete is not "newly blocked" by a
	// tighter rule, even though it now misses one more line.
	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Only a title"}, a.key(w.teams[2].person)); r.status != 200 {
		t.Fatalf("incomplete draft: %d %s", r.status, r.body)
	}

	// dry_run: tighten "problem" to required... it already is under build;
	// instead drop audience's requirement and turn "code_url" on to see a
	// real newly-blocked count: require code_url, which neither team has set.
	dry := a.at(t, "PUT", base, map[string]any{"dry_run": true, "fields": map[string]string{"code_url": "required"}}, a.key(w.org))
	if dry.status != 200 {
		t.Fatalf("dry run: %d %s", dry.status, dry.body)
	}
	impact := dry.json(t)["impact"].(map[string]any)
	if impact["submitted_missing"] != float64(1) || impact["drafts_newly_blocked"] != float64(1) {
		t.Fatalf("dry run impact: %#v", impact)
	}
	// dry_run writes nothing: re-GET shows the old rules.
	after := a.at(t, "GET", base, nil, a.key(w.org)).json(t)
	afterFields := after["fields"].(map[string]any)
	if afterFields["code_url"] == "required" {
		t.Fatalf("dry run persisted a write: %#v", after)
	}

	// The submitted entry stays submitted even after a real tightening.
	if r := a.at(t, "PUT", base, map[string]any{"fields": map[string]string{"code_url": "required"}}, a.key(w.org)); r.status != 200 {
		t.Fatalf("tighten: %d %s", r.status, r.body)
	}
	sView := a.at(t, "GET", w.entryPath(), nil, a.key(submitted.person)).json(t)
	if sView["status"] != "submitted" {
		t.Fatalf("tightening un-submitted an entry: %#v", sView)
	}

	// 409 once rules are closed (stage closed or later).
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "closed"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("close: %d %s", r.status, r.body)
	}
	wantCode(t, "closed non-dry-run", a.at(t, "PUT", base, map[string]any{"preset": "video_website_code"}, a.key(w.org)), 409, "entry_rules_closed")
	wantCode(t, "closed dry run too", a.at(t, "PUT", base, map[string]any{"dry_run": true, "preset": "video_website_code"}, a.key(w.org)), 409, "entry_rules_closed")
}

// TestHackEntryRequiredAliasDeprecated: PATCH entry_required maps to
// "required" for the listed (legacy) fields and demotes the preset to
// custom, per hack.go patchEvent's documented alias behaviour.
func TestHackEntryRequiredAliasDeprecated(t *testing.T) {
	w := newRulesWorld(t, 0)
	a := w.a

	// The default video_or_website preset leaves "screenshot" optional.
	before := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entry-rules", nil, a.key(w.org)).json(t)
	if before["fields"].(map[string]any)["screenshot"] != "optional" {
		t.Fatalf("precondition: %#v", before)
	}

	r := a.at(t, "PATCH", "/v1/hack/events/"+w.slug, map[string]any{"entry_required": []string{"screenshot"}}, a.key(w.org))
	if r.status != 200 {
		t.Fatalf("patch alias: %d %s", r.status, r.body)
	}

	after := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entry-rules", nil, a.key(w.org)).json(t)
	if after["preset"] != "custom" {
		t.Fatalf("alias did not demote to custom: %#v", after)
	}
	fields := after["fields"].(map[string]any)
	if fields["screenshot"] != "required" {
		t.Fatalf("alias did not require screenshot: %#v", fields)
	}
	// Fields outside the legacy list are untouched (still video_or_website's values).
	if fields["audience"] != "optional" || fields["code_url"] != "off" {
		t.Fatalf("alias touched a non-legacy field: %#v", fields)
	}
}

// TestHackEntrySaveRulesOnRetiredPreset: Codex review round 1, finding 2.
// organiser-entries.js used to send body.preset whenever it wasn't
// "custom", including a retired preset (build/code/ideas) an event from
// before SIM-382 is still on -- saving unchanged rules, or changing only
// judge_first, passed the dry run (which never sent preset at all) but
// 400ed on the real save. The fix: the client sends preset only when it is
// one of the two current ones, and dry_run uses the identical body. This
// proves the server-side half the fix relies on: the exact request shape
// the UI now sends for an untouched retired-preset event -- fields (the
// full current table, unchanged) and judge_first, no preset -- succeeds on
// both dry_run and the real save, and keeps the retired preset rather than
// demoting it (the fields still match that preset's own table).
func TestHackEntrySaveRulesOnRetiredPreset(t *testing.T) {
	w := newRulesWorld(t, 0)
	a := w.a
	base := "/v1/hack/events/" + w.slug + "/entry-rules"

	// Simulate an event that already existed on the "build" preset before
	// SIM-382 (a new event today starts on video_or_website instead).
	buildRules := `{"v":1,"fields":{"title":"required","problem":"required","tagline":"required","audience":"required","track":"required","tools":"required","maturity":"required","description":"optional","video_url":"one_of","site":"one_of","code_url":"off","screenshot":"optional"}}`
	if _, err := a.database.Exec(`UPDATE events SET entry_preset = 'build', entry_rules = $2::jsonb, judge_first = 'video' WHERE slug = $1`, w.slug, buildRules); err != nil {
		t.Fatal(err)
	}
	before := a.at(t, "GET", base, nil, a.key(w.org)).json(t)
	if before["preset"] != "build" {
		t.Fatalf("precondition: %#v", before)
	}
	fieldsAny := before["fields"].(map[string]any)
	fields := make(map[string]string, len(fieldsAny))
	for k, v := range fieldsAny {
		fields[k] = v.(string)
	}

	// The exact shape organiser-entries.js's onSaveRules now sends for this
	// event (preset omitted, since "build" isn't one of the two current
	// ones): fields unchanged, judge_first flipped.
	body := map[string]any{"fields": fields, "judge_first": "site"}

	dry := a.at(t, "PUT", base, map[string]any{"fields": fields, "judge_first": "site", "dry_run": true}, a.key(w.org))
	if dry.status != 200 {
		t.Fatalf("dry run on a retired preset should succeed: %d %s", dry.status, dry.body)
	}
	dryPreview := dry.json(t)["preview"].(map[string]any)
	if dryPreview["preset"] != "build" {
		t.Fatalf("dry run preview should keep the retired preset: %#v", dryPreview)
	}

	r := a.at(t, "PUT", base, body, a.key(w.org))
	if r.status != 200 {
		t.Fatalf("save on a retired preset should succeed: %d %s", r.status, r.body)
	}
	saved := r.json(t)
	if saved["preset"] != "build" {
		t.Fatalf("judge_first-only save should keep the retired preset, not demote it: %#v", saved)
	}
	if saved["judge_first"] != "site" {
		t.Fatalf("judge_first did not save: %#v", saved)
	}

	// The refused shape (what the client used to send): preset explicitly
	// named, even though it is retired.
	wantCode(t, "preset build still refused directly", a.at(t, "PUT", base, map[string]any{"preset": "build"}, a.key(w.org)), 400, "invalid_preset")
}

// --- listEntries counts -----------------------------------------------------

func TestHackEntriesListCounts(t *testing.T) {
	w := newRulesWorld(t, 3)
	a := w.a
	notStarted, draftTeam, submittedTeam := w.teams[0], w.teams[1], w.teams[2]
	_ = notStarted

	if r := a.at(t, "PUT", w.entryPath(), map[string]string{"title": "Draft only"}, a.key(draftTeam.person)); r.status != 200 {
		t.Fatalf("draft: %d %s", r.status, r.body)
	}
	if r := a.at(t, "PUT", w.entryPath(), buildCompleteFields(), a.key(submittedTeam.person)); r.status != 200 {
		t.Fatalf("submit entry: %d %s", r.status, r.body)
	}
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/entry/submit", map[string]any{}, a.key(submittedTeam.person)); r.status != 200 {
		t.Fatalf("submit: %d %s", r.status, r.body)
	}

	list := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, a.key(w.org))
	if list.status != 200 {
		t.Fatalf("list: %d %s", list.status, list.body)
	}
	counts := list.json(t)["counts"].(map[string]any)
	if counts["submitted"] != float64(1) || counts["drafts"] != float64(1) || counts["not_started"] != float64(1) || counts["not_submitted"] != float64(0) {
		t.Fatalf("counts: %#v", counts)
	}

	// Close submissions: the draft (incomplete) team settles to
	// not_submitted; counts move accordingly.
	if r := a.at(t, "POST", "/v1/hack/events/"+w.slug+"/stage", map[string]string{"stage": "closed"}, a.key(w.org)); r.status != 200 {
		t.Fatalf("close: %d %s", r.status, r.body)
	}
	list2 := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/entries", nil, a.key(w.org))
	counts2 := list2.json(t)["counts"].(map[string]any)
	if counts2["submitted"] != float64(1) || counts2["not_submitted"] != float64(1) || counts2["drafts"] != float64(0) || counts2["not_started"] != float64(1) {
		t.Fatalf("counts after close: %#v", counts2)
	}
}

// --- CSV: csvSafe neutralises a formula-looking cell ------------------------

func TestHackEntriesCSVNeutralisesFormulas(t *testing.T) {
	w := newRulesWorld(t, 1)
	a, p := w.a, w.teams[0].person
	fields := buildCompleteFields()
	fields["problem"] = "=cmd|' /C calc'!A0"
	if r := a.at(t, "PUT", w.entryPath(), fields, a.key(p)); r.status != 200 {
		t.Fatalf("entry: %d %s", r.status, r.body)
	}
	r := a.at(t, "GET", "/v1/hack/events/"+w.slug+"/export/entries.csv", nil, a.key(w.org))
	if r.status != 200 {
		t.Fatalf("csv: %d %s", r.status, r.body)
	}
	rows, err := csv.NewReader(strings.NewReader(string(r.body))).ReadAll()
	if err != nil || len(rows) < 2 {
		t.Fatalf("csv parse: %v %s", err, r.body)
	}
	header := rows[0]
	idx := slices.Index(header, "problem")
	if idx < 0 {
		t.Fatalf("no problem column: %v", header)
	}
	got := rows[1][idx]
	if !strings.HasPrefix(got, "'") || strings.HasPrefix(got, "=") {
		t.Fatalf("problem cell not neutralised: %q", got)
	}
}
