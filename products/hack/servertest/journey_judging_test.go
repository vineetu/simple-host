package servertest

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/products/hack/hacktest"
	"github.com/vsriram/simple-host/products/hack/pages"
)

func TestHackManualAndPanelJudgingAccess(t *testing.T) {
	w := newFeatJudgingWorld(t, 2, 2)
	criterion := hacktest.DecodeRubric(t, w.Call(t, "GET", "/rubric", nil, w.Org)).Criteria[0].ID
	settings := w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "manual"}, w.Org)
	if settings.Status != 200 {
		t.Fatalf("manual mode: %d %s", settings.Status, settings.Body)
	}
	payload := map[string]any{"assignments": []map[string]string{{"judge_id": w.Judges[0].ID, "team_id": w.Teams[0].ID}}}
	r := w.Call(t, "PUT", "/assignments", payload, w.Org)
	if r.Status != 200 {
		t.Fatalf("manual assignments: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "PUT", "/assignments", payload, w.Outsider); r.Status != 404 {
		t.Fatalf("outsider assignment write: %d %s", r.Status, r.Body)
	}
	queue := w.Call(t, "GET", "/judge/queue", nil, w.Judges[0].Person).JSON(t)["queue"].([]any)
	if len(queue) != 1 || queue[0].(map[string]any)["team_id"] != w.Teams[0].ID {
		t.Fatalf("manual queue: %v", queue)
	}
	dashboard := w.Call(t, "GET", "/judging/dashboard", nil, w.Org).JSON(t)
	manualJudges := dashboard["judges"].([]any)
	manualAssigned := 0.0
	for _, row := range manualJudges {
		manualAssigned += row.(map[string]any)["assigned_count"].(float64)
	}
	if manualAssigned != 1 {
		t.Fatalf("manual dashboard ignored assignments: %v", manualJudges)
	}
	w.SetStage(t, "judging")
	denied := w.Call(t, "PUT", "/judge/scores/"+w.Teams[1].ID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 5}}}, w.Judges[0].Person)
	if denied.Status != 403 {
		t.Fatalf("unassigned score: %d %s", denied.Status, denied.Body)
	}
	tracks := w.Call(t, "PUT", "/tracks", map[string]any{"tracks": []map[string]string{{"slug": "climate", "name": "Climate", "challenge": "Build", "prize": "Trophy"}}}, w.Org)
	if tracks.Status != 200 {
		t.Fatalf("tracks: %d %s", tracks.Status, tracks.Body)
	}
	var listed []map[string]any
	if err := json.Unmarshal(tracks.Body, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("track response: %s %v", tracks.Body, err)
	}
	trackID := listed[0]["id"].(string)
	w.SetStage(t, "building") // participants choose tracks before entries close
	r = w.Call(t, "PUT", "/team/track", map[string]string{"track": "climate"}, w.Part.Person)
	w.SetStage(t, "closed")
	if r.Status != 200 {
		t.Fatalf("team track: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "panel"}, w.Org); r.Status != 200 {
		t.Fatalf("panel mode: %d %s", r.Status, r.Body)
	}
	panel := map[string]any{"panels": []map[string]string{{"track_id": trackID, "judge_id": w.Judges[1].ID}}}
	if r = w.Call(t, "PUT", "/judging/panels", panel, w.Org); r.Status != 200 {
		t.Fatalf("panel members: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "GET", "/judge/queue", nil, w.Judges[0].Person); len(r.JSON(t)["queue"].([]any)) != 0 {
		t.Fatalf("wrong judge panel queue: %s", r.Body)
	}
	if r = w.Call(t, "GET", "/judge/queue", nil, w.Judges[1].Person); len(r.JSON(t)["queue"].([]any)) != 1 {
		t.Fatalf("panel queue: %s", r.Body)
	}
	dashboard = w.Call(t, "GET", "/judging/dashboard", nil, w.Org).JSON(t)
	panelJudges := dashboard["judges"].([]any)
	assigned := 0.0
	for _, row := range panelJudges {
		assigned += row.(map[string]any)["assigned_count"].(float64)
	}
	if assigned != 1 {
		t.Fatalf("panel dashboard ignored memberships: %v", panelJudges)
	}
	w.SetStage(t, "judging")
	if r = w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 5}}}, w.Judges[0].Person); r.Status != 403 {
		t.Fatalf("non-panel score: %d %s", r.Status, r.Body)
	}
	w.SetStage(t, "closed")
	other := hacktest.UniqueSlug()
	if r = w.a.CreateEvent(t, w.Outsider, other, nil); r.Status != 201 {
		t.Fatalf("other event: %d %s", r.Status, r.Body)
	}
	foreign := w.a.At(t, "PUT", "/v1/hack/events/"+other+"/tracks", map[string]any{"tracks": []map[string]string{{"slug": "foreign", "name": "Foreign"}}}, w.a.Key(w.Outsider))
	var foreignRows []map[string]any
	if err := json.Unmarshal(foreign.Body, &foreignRows); err != nil || len(foreignRows) != 1 {
		t.Fatalf("foreign track: %s %v", foreign.Body, err)
	}
	if r = w.Call(t, "PUT", "/judging/panels", map[string]any{"panels": []map[string]string{{"track_id": foreignRows[0]["id"].(string), "judge_id": w.Judges[1].ID}}}, w.Org); r.Status != 404 {
		t.Fatalf("foreign panel: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "POST", "/conflicts", map[string]any{"team_id": w.Teams[0].ID}, w.Judges[1].Person); r.Status != 200 {
		t.Fatalf("conflict: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "GET", "/judge/queue", nil, w.Judges[1].Person); len(r.JSON(t)["queue"].([]any)) != 0 {
		t.Fatalf("conflict queue: %s", r.Body)
	}
}

func TestHackTieCriterionAndPrizeSnapshot(t *testing.T) {
	w := newFeatJudgingWorld(t, 2, 1)
	defaults := w.Call(t, "GET", "/judging/settings", nil, w.Org).JSON(t)
	if defaults["public_scores"] != true || defaults["public_ranks"] != true {
		t.Fatalf("public results defaults changed: %v", defaults)
	}
	rubric := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 50, 5), hacktest.Criterion("Design", "", 50, 5)), w.Org))
	c1, c2 := rubric.Criteria[0].ID, rubric.Criteria[1].ID
	r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"tie_criterion_id": c1, "score_mode": "raw"}, w.Org)
	if r.Status != 200 {
		t.Fatalf("tie rule: %d %s", r.Status, r.Body)
	}
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	w.ScoreTeam(t, w.Judges[0], w.Teams[0].ID, map[string]int{c1: 5, c2: 3}, nil)
	w.ScoreTeam(t, w.Judges[0], w.Teams[1].ID, map[string]int{c1: 3, c2: 5}, nil)
	tracks := w.Call(t, "PUT", "/tracks", map[string]any{"tracks": []map[string]string{{"slug": "climate", "name": "Climate", "prize": "Trophy"}}}, w.Org)
	if tracks.Status != 200 {
		t.Fatalf("tracks: %d %s", tracks.Status, tracks.Body)
	}
	if stage := w.A.SetStage(t, w.Slug, "building", w.A.Key(w.Org)); stage.Status != 200 {
		t.Fatalf("move to building: %d %s", stage.Status, stage.Body)
	}
	r = w.Call(t, "PUT", "/team/track", map[string]string{"track": "climate"}, w.Part.Person)
	if stage := w.A.SetStage(t, w.Slug, "closed", w.A.Key(w.Org)); stage.Status != 200 {
		t.Fatalf("move to closed: %d %s", stage.Status, stage.Body)
	}
	if r.Status != 200 {
		t.Fatalf("team track: %d %s", r.Status, r.Body)
	}
	pub := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub.Status != 200 {
		t.Fatalf("publish: %d %s", pub.Status, pub.Body)
	}
	rows := pub.JSON(t)["results"].([]any)
	if rows[0].(map[string]any)["team_id"] != w.Teams[0].ID || rows[1].(map[string]any)["tie_decider"] != "criterion" {
		t.Fatalf("criterion tie order: %v", rows)
	}
	if rows[0].(map[string]any)["track_winner"] != true {
		t.Fatalf("track winner: %v", rows[0])
	}
	if r = w.Call(t, "PATCH", "/judging/settings", map[string]any{"public_scores": false, "public_ranks": false}, w.Org); r.Status != 200 {
		t.Fatalf("hide public values: %d %s", r.Status, r.Body)
	}
	public := w.Call(t, "GET", "/results", nil, w.Outsider)
	if public.Status != 200 || !strings.Contains(string(public.Body), "Trophy") || strings.Contains(string(public.Body), "raw_total") || strings.Contains(string(public.Body), "\"rank\"") {
		t.Fatalf("public score privacy: %d %s", public.Status, public.Body)
	}
	// The env admin key is the admin here too (auth.Optional); a bad key is
	// refused, not read as signed out.
	if r := w.a.At(t, "GET", w.Path("/results"), nil, map[string]string{"X-API-Key": w.a.Admin}); r.Status != 200 || r.JSON(t)["results"] == nil {
		t.Fatalf("admin key results: %d %s", r.Status, r.Body)
	}
	if r := w.a.At(t, "GET", w.Path("/results"), nil, map[string]string{"X-API-Key": "sh_bogus"}); r.Status != 401 {
		t.Fatalf("bad key results: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "PATCH", "/judging/settings", map[string]any{"public_scores": true, "public_ranks": true}, w.Org); r.Status != 200 {
		t.Fatalf("show public scores: %d %s", r.Status, r.Body)
	}
	public = w.Call(t, "GET", "/results", nil, w.Outsider)
	if !strings.Contains(string(public.Body), "raw_total") {
		t.Fatalf("public scores missing: %s", public.Body)
	}
	mine := w.Call(t, "GET", "/my-results", nil, w.Part.Person)
	if mine.Status != 200 || !strings.Contains(string(mine.Body), "normalised_total") || !strings.Contains(string(mine.Body), "Trophy") {
		t.Fatalf("private results: %d %s", mine.Status, mine.Body)
	}
	csv := w.Call(t, "GET", "/export/results.csv", nil, w.Org)
	if csv.Status != 200 || !strings.Contains(string(csv.Body), "normalised_total") || !strings.Contains(string(csv.Body), "Trophy") {
		t.Fatalf("results csv: %d %s", csv.Status, csv.Body)
	}
	stage := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org))
	if stage.Status != 200 {
		t.Fatalf("judging stage: %d %s", stage.Status, stage.Body)
	}
	if r = w.Call(t, "PATCH", "/judging/settings", map[string]any{"tie_criterion_id": c2}, w.Org); r.Status != 409 {
		t.Fatalf("late tie change: %d %s", r.Status, r.Body)
	}
	if r = w.Call(t, "PATCH", "/judging/settings", map[string]any{"score_mode": "normalised"}, w.Org); r.Status != 200 {
		t.Fatalf("normalised preview mode: %d %s", r.Status, r.Body)
	}
	preview := w.Call(t, "GET", "/judging/preview", nil, w.Org)
	if preview.Status != 200 || preview.JSON(t)["deciding_mode"] != "normalised" {
		t.Fatalf("preview: %d %s", preview.Status, preview.Body)
	}
	// A republish records the selected mode; the earlier snapshot was raw.
	if r = w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org); r.Status != 200 || !strings.Contains(string(r.Body), "normalised_total") {
		t.Fatalf("republish: %d %s", r.Status, r.Body)
	}
}

func TestHackNormalisedRankingAndJudgeMajority(t *testing.T) {
	w := newFeatJudgingWorld(t, 3, 2)
	c := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 10)), w.Org)).Criteria[0].ID
	w.SetStage(t, "judging")
	for _, tc := range []struct{ judge, team, points int }{{0, 0, 10}, {0, 1, 9}, {1, 0, 0}, {1, 1, 8}, {1, 2, 10}} {
		w.ScoreTeam(t, w.Judges[tc.judge], w.Teams[tc.team].ID, map[string]int{c: tc.points}, nil)
	}
	raw := w.Call(t, "GET", "/judging/preview", nil, w.Org).JSON(t)["results"].([]any)
	rank := func(rows []any, id string) float64 {
		for _, x := range rows {
			m := x.(map[string]any)
			if m["team_id"] == id {
				return m["rank"].(float64)
			}
		}
		t.Fatal("missing team")
		return 0
	}
	if rank(raw, w.Teams[0].ID) <= rank(raw, w.Teams[1].ID) {
		t.Fatalf("raw should rank B ahead of A: %v", raw)
	}
	if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"score_mode": "normalised"}, w.Org); r.Status != 200 {
		t.Fatalf("mode: %d %s", r.Status, r.Body)
	}
	norm := w.Call(t, "GET", "/judging/preview", nil, w.Org).JSON(t)["results"].([]any)
	if rank(norm, w.Teams[0].ID) >= rank(norm, w.Teams[1].ID) {
		t.Fatalf("normalisation should rank A ahead of B: %v", norm)
	}
	for _, x := range norm {
		m := x.(map[string]any)
		if m["total"] != nil {
			v := m["total"].(float64)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatal("non-finite total")
			}
		}
	}

	major := newFeatJudgingWorld(t, 2, 3)
	c = hacktest.DecodeRubric(t, major.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), major.Org)).Criteria[0].ID
	if r := major.A.SetStage(t, major.Slug, "judging", major.A.Key(major.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	for _, tc := range []struct{ judge, a, b int }{{0, 5, 4}, {1, 5, 4}, {2, 1, 3}} {
		major.ScoreTeam(t, major.Judges[tc.judge], major.Teams[0].ID, map[string]int{c: tc.a}, nil)
		major.ScoreTeam(t, major.Judges[tc.judge], major.Teams[1].ID, map[string]int{c: tc.b}, nil)
	}
	rows := major.Call(t, "POST", "/results/publish", map[string]any{}, major.Org).JSON(t)["results"].([]any)
	if rows[0].(map[string]any)["team_id"] != major.Teams[0].ID || rows[1].(map[string]any)["tie_decider"] != "judge_majority" {
		t.Fatalf("majority tie: %v", rows)
	}
}

// TestHackRubricParticipantAccess is audit item 1 (2026-11-02): an approved
// participant can read the rubric; a pending or rejected applicant gets the
// same approval_<status> refusal every other participant route gives them;
// a non-member gets the same 404 a wrong-role member would.
func TestHackRubricParticipantAccess(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "rbog")
	slug := hacktest.UniqueSlug()
	a.CreateEvent(t, org, slug, nil)
	a.OpenEvent(t, org, slug)
	a.ListEvent(t, slug)
	base := "/v1/hack/events/" + slug

	pending := a.NewPerson(t, "rbpending")
	hacktest.WantTS(t, "apply pending", a.Apply(t, slug, pending, map[string]any{"age_range": ""}), 200, "")

	rejected := a.NewPerson(t, "rbrejected")
	hacktest.WantTS(t, "apply rejected target", a.Apply(t, slug, rejected, map[string]any{"age_range": ""}), 200, "")
	hacktest.WantTS(t, "reject", a.At(t, "POST", base+"/applications/"+a.UserID(t, rejected)+"/decision", map[string]string{"decision": "rejected"}, a.Key(org)), 200, "")

	approved := a.NewPerson(t, "rbapproved")
	hacktest.WantTS(t, "apply approved target", a.Apply(t, slug, approved, map[string]any{"age_range": ""}), 200, "")
	hacktest.WantTS(t, "approve", a.At(t, "POST", base+"/applications/"+a.UserID(t, approved)+"/decision", map[string]string{"decision": "approved"}, a.Key(org)), 200, "")

	outsider := a.NewPerson(t, "rboutsider")

	hacktest.WantTS(t, "approved reads rubric", a.At(t, "GET", base+"/rubric", nil, a.Key(approved)), 200, "")
	hacktest.WantTS(t, "pending reads rubric", a.At(t, "GET", base+"/rubric", nil, a.Key(pending)), 403, "approval_pending")
	hacktest.WantTS(t, "rejected reads rubric", a.At(t, "GET", base+"/rubric", nil, a.Key(rejected)), 403, "approval_rejected")
	hacktest.WantTS(t, "non-member reads rubric", a.At(t, "GET", base+"/rubric", nil, a.Key(outsider)), 404, "event_not_found")
}

// There is one comment per judge and team. Scoring a criterion for the first
// time without a comment keeps the comment already given, in the judge's own
// view and in the organiser's scores export alike.
func TestHackJudgingPointsOnlySaveKeepsTheComment(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 60, 5), hacktest.Criterion("Craft", "", 40, 5)), w.Org))
	c1, c2 := rv.Criteria[0].ID, rv.Criteria[1].ID
	team, judge := w.Teams[0].ID, w.Judges[0]
	w.SetStage(t, "judging")
	comment := "Lovely demo"
	if r := w.ScoreTeam(t, judge, team, map[string]int{c1: 4}, &comment); r.Status != http.StatusOK {
		t.Fatalf("score with comment: %d %s", r.Status, r.Body)
	}
	// A new row for c2, saved later and with no comment.
	if r := w.ScoreTeam(t, judge, team, map[string]int{c2: 3}, nil); r.Status != http.StatusOK || r.JSON(t)["comment"] != comment {
		t.Fatalf("points-only save on a new criterion blanked the comment: %s", r.Body)
	}
	if r := w.Call(t, "GET", "/judge/scores/"+team, nil, judge.Person); r.JSON(t)["comment"] != comment {
		t.Fatalf("judge reads a blank comment: %s", r.Body)
	}
	if n := w.CountRows(t, `SELECT COUNT(*) FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND comment=$3`, judge.ID, team, comment); n != 2 {
		t.Fatalf("%d of 2 rows hold the comment", n)
	}
	csv := string(w.Call(t, "GET", "/export/scores.csv", nil, w.Org).Body)
	if strings.Count(csv, comment) != 2 {
		t.Fatalf("scores export does not show the comment on both rows: %s", csv)
	}
}

// handScoreFormula is the same formula the handler uses, computed by hand
// here for a known input, to cross-check computeResults independently.
func TestHackJudgingScoreFormulaHandCheck(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(
		hacktest.Criterion("Impact", "", 60, 5), // weight 60, max 5
		hacktest.Criterion("Craft", "", 40, 10), // weight 40, max 10
	), w.Org))
	c1, c2 := rv.Criteria[0].ID, rv.Criteria[1].ID
	team := w.Teams[0].ID
	judge := w.Judges[0]
	// Impact 4/5, Craft 8/10:
	// (60/100 * 4/5)*100 + (40/100 * 8/10)*100 = 48 + 32 = 80
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	r := w.ScoreTeam(t, judge, team, map[string]int{c1: 4, c2: 8}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("score: %d %s", r.Status, r.Body)
	}
	pub := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub.Status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.Status, pub.Body)
	}
	rows := pub.JSON(t)["results"].([]any)
	found := false
	for _, row := range rows {
		m := row.(map[string]any)
		if m["team_id"] == team {
			found = true
			total, ok := m["total"].(float64)
			if !ok || total != 80 {
				t.Fatalf("hand-checked total: got %v, want 80", m["total"])
			}
		}
	}
	if !found {
		t.Fatal("team not in results")
	}
}

// TestHackPublishResultsMovesEventToResults is SIM-271: before this fix,
// PublishResults had no stage check at all, so a judging-locked publish from
// the organiser's Results screen (or a direct REST/MCP call) could succeed
// while the event stage stayed "judging" (or earlier) -- the Overview pill
// and the public page kept saying "Judging is under way" even though
// winners were already out. Publishing now always leaves the event at
// Results (or later), the same place the Overview's "Finish judging" button
// would have put it, so the stage and its words never disagree with what
// just happened.
func TestHackPublishResultsMovesEventToResults(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org))
	c1 := rv.Criteria[0].ID
	team := w.Teams[0].ID
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	w.ScoreTeam(t, w.Judges[0], team, map[string]int{c1: 5}, nil)
	w.SetStage(t, "closed")

	eventJSON := func() map[string]any {
		return w.Call(t, "GET", "", nil, w.Org).JSON(t)["event"].(map[string]any)
	}
	if stage := eventJSON()["stage"]; stage != "closed" {
		t.Fatalf("setup: stage = %v, want closed", stage)
	}

	// Before entries close there is nothing to judge: publishing is refused
	// and the stage stays put.
	if r := w.A.SetStage(t, w.Slug, "building", w.A.Key(w.Org)); r.Status != http.StatusOK {
		t.Fatalf("back to building: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org); r.Status != http.StatusConflict || !strings.Contains(string(r.Body), "judging_not_started") {
		t.Fatalf("publish at building: %d %s, want 409 judging_not_started", r.Status, r.Body)
	}
	if stage := eventJSON()["stage"]; stage != "building" {
		t.Fatalf("stage after refused publish = %v, want building", stage)
	}
	if r := w.A.SetStage(t, w.Slug, "closed", w.A.Key(w.Org)); r.Status != http.StatusOK {
		t.Fatalf("back to closed: %d %s", r.Status, r.Body)
	}

	// A refused publish (a bad rank override) rolls the stage move back
	// with it: still Closed, judging not locked.
	if r := w.Call(t, "POST", "/results/publish", map[string]any{"rank_overrides": []map[string]any{{"team_id": "no-such-team", "rank": 1}}}, w.Org); r.Status != http.StatusBadRequest {
		t.Fatalf("publish with a bad override: %d %s, want 400", r.Status, r.Body)
	}
	if ev := eventJSON(); ev["stage"] != "closed" || ev["judging_locked_at"] != nil {
		t.Fatalf("after refused publish: stage %v, judging_locked_at %v; want closed, unlocked", ev["stage"], ev["judging_locked_at"])
	}

	// Publishing straight from Closed -- skipping Start judging, Lock
	// judging and Finish judging entirely, as a direct REST/MCP call or an
	// organiser Results screen visit could -- still works, and leaves the
	// event at Results, locked, exactly as Finish judging would have.
	pub := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub.Status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.Status, pub.Body)
	}
	ev := eventJSON()
	if ev["stage"] != "results" {
		t.Fatalf("stage after publish = %v, want results", ev["stage"])
	}
	if ev["judging_locked_at"] == nil {
		t.Fatal("judging_locked_at after publish: want set, got nil")
	}
	if ev["results_published"] != true {
		t.Fatalf("results_published after publish = %v, want true", ev["results_published"])
	}
	// hackStagePill/buildHackStageCard (hack_eventpage.go) already switch
	// on exactly this "results" + published combination to stop saying
	// "Judging is under way" -- the bug was only ever that the stage never
	// got here, which the checks above now cover directly.

	// Republishing (e.g. after fixing a conflict) keeps working once Results
	// has already been reached: no second stage move, no error.
	pub2 := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub2.Status != http.StatusOK {
		t.Fatalf("republish at results: %d %s", pub2.Status, pub2.Body)
	}
	if ev2 := eventJSON(); ev2["stage"] != "results" {
		t.Fatalf("stage after republish = %v, want results", ev2["stage"])
	}

	// And results stay updatable (e.g. to fix a disqualified team) even
	// once the event has ended -- publishing never refuses on account of
	// Archived, only reopening a stage does.
	w.SetStage(t, "archived")
	pub3 := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub3.Status != http.StatusOK {
		t.Fatalf("republish at archived: %d %s", pub3.Status, pub3.Body)
	}
	if ev3 := eventJSON(); ev3["stage"] != "archived" {
		t.Fatalf("stage after republish at archived = %v, want still archived", ev3["stage"])
	}
}

func TestHackJudgingConflictExcludedFromTotal(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 2)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org))
	c1 := rv.Criteria[0].ID
	team := w.Teams[0].ID
	// Judge 1 scores normally; judge 2 scores (with a comment) then declares a conflict.
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	w.ScoreTeam(t, w.Judges[0], team, map[string]int{c1: 5}, nil) // 100
	conflictedComment := "a comment from the conflicted judge"
	w.ScoreTeam(t, w.Judges[1], team, map[string]int{c1: 1}, &conflictedComment) // 20
	w.Call(t, "POST", "/conflicts", map[string]any{"team_id": team}, w.Judges[1].Person)

	pub := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub.Status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.Status, pub.Body)
	}
	rows := pub.JSON(t)["results"].([]any)
	for _, row := range rows {
		m := row.(map[string]any)
		if m["team_id"] == team {
			if m["judges_scored"].(float64) != 1 {
				t.Fatalf("judges_scored: want 1 (conflicted judge excluded), got %v", m["judges_scored"])
			}
			if m["total"].(float64) != 100 {
				t.Fatalf("total: want 100 (only judge 1's score), got %v", m["total"])
			}
		}
	}
	// A conflicted judge's comment is excluded from the team's private view
	// too, for the same reason their score is excluded from the total.
	mine := w.Call(t, "GET", "/my-results", nil, w.Part.Person)
	if mine.Status != http.StatusOK {
		t.Fatalf("my-results: %d %s", mine.Status, mine.Body)
	}
	comments := mine.JSON(t)["comments"].([]any)
	for _, c := range comments {
		if c == conflictedComment {
			t.Fatalf("conflicted judge's comment leaked into my-results: %v", comments)
		}
	}
}

func TestHackOrganisersCanJudgeAndRemovedOrganiserScoresStopCounting(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	team := w.Teams[0].ID
	criterionID := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org)).Criteria[0].ID
	co := w.a.NewPerson(t, "cojudge")
	w.AddOrganiser(t, w.a.Key(co))
	w.Call(t, "PATCH", "/judging/settings", map[string]any{"include_organisers": true}, w.Org)
	for _, who := range []hacktest.Person{w.Org, co} {
		queue := w.Call(t, "GET", "/judge/queue", nil, who)
		if queue.Status != http.StatusOK || len(queue.JSON(t)["queue"].([]any)) != 1 {
			t.Fatalf("organiser queue: %d %s", queue.Status, queue.Body)
		}
	}
	for _, who := range []hacktest.Person{w.Part.Person, w.Outsider} {
		if r := w.Call(t, "GET", "/judge/queue", nil, who); r.Status != http.StatusNotFound {
			t.Fatalf("nonjudge queue: %d %s", r.Status, r.Body)
		}
		if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
			t.Fatalf("start judging: %d %s", r.Status, r.Body)
		}
		if r := w.Call(t, "PUT", "/judge/scores/"+team, map[string]any{"scores": []map[string]any{{"criterion_id": criterionID, "points": 5}}}, who); r.Status != http.StatusNotFound {
			t.Fatalf("nonjudge score: %d %s", r.Status, r.Body)
		}
	}
	for _, method := range []string{"GET", "PUT"} {
		path := "/judge/queue"
		var body any
		if method == "PUT" {
			path = "/judge/scores/" + team
			body = map[string]any{"scores": []map[string]any{{"criterion_id": criterionID, "points": 5}}}
		}
		if r := w.a.At(t, method, w.Path(path), body, w.a.AdminH()); r.Status != http.StatusNotFound {
			t.Fatalf("platform admin %s: %d %s", method, r.Status, r.Body)
		}
	}
	if r := w.a.At(t, "GET", w.Path("/judge/scores/"+team), nil, w.a.AdminH()); r.Status != http.StatusNotFound {
		t.Fatalf("platform admin score read: %d %s", r.Status, r.Body)
	}
	orgComment, coComment := "creator comment", "co organiser comment"
	for _, row := range []struct {
		who     hacktest.Person
		points  int
		comment string
	}{{w.Org, 5, orgComment}, {co, 3, coComment}, {w.Judges[0].Person, 1, "judge comment"}} {
		r := w.Call(t, "PUT", "/judge/scores/"+team, map[string]any{"scores": []map[string]any{{"criterion_id": criterionID, "points": row.points}}, "comment": row.comment}, row.who)
		if r.Status != http.StatusOK {
			t.Fatalf("score: %d %s", r.Status, r.Body)
		}
	}
	if r := w.Call(t, "GET", "/judge/scores/"+team, nil, co); r.Status != http.StatusOK || r.JSON(t)["comment"] != coComment {
		t.Fatalf("co-organiser scores: %d %s", r.Status, r.Body)
	}
	csv := w.Call(t, "GET", "/export/scores.csv", nil, w.Org)
	if csv.Status != http.StatusOK || !strings.Contains(string(csv.Body), orgComment) || !strings.Contains(string(csv.Body), coComment) {
		t.Fatalf("organiser raw scores missing from CSV: %d %s", csv.Status, csv.Body)
	}
	checkResult := func(wantTotal, wantJudges float64, wantComments ...string) {
		t.Helper()
		published := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
		if published.Status != http.StatusOK {
			t.Fatalf("publish: %d %s", published.Status, published.Body)
		}
		result := published.JSON(t)["results"].([]any)[0].(map[string]any)
		if result["total"] != wantTotal || result["judges_scored"] != wantJudges {
			t.Fatalf("total/coverage: %v", result)
		}
		mine := w.Call(t, "GET", "/my-results", nil, w.Part.Person)
		if mine.Status != http.StatusOK {
			t.Fatalf("private result: %d %s", mine.Status, mine.Body)
		}
		comments := mine.JSON(t)["comments"].([]any)
		if len(comments) != len(wantComments) {
			t.Fatalf("comments: %v want %v", comments, wantComments)
		}
		for _, want := range wantComments {
			found := false
			for _, got := range comments {
				found = found || got == want
			}
			if !found {
				t.Fatalf("missing comment %q in %v", want, comments)
			}
		}
	}
	// The self-conflict (and the forbidden read/write/queue checks it
	// causes) is declared before the first publish, while scoring is still
	// open: SIM-271 made publishing move the event to Results and lock
	// judging in the same step, the same as the Overview's "Finish judging"
	// -- scoring itself (not just a new conflict) is meant to close once
	// judging has moved on, so this is no longer something to exercise
	// after a publish.
	self := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{"team_id": team}, w.Org))
	if self.JudgeID != w.a.UserID(t, w.Org) {
		t.Fatalf("self conflict targeted %s", self.JudgeID)
	}
	if r := w.Call(t, "GET", "/judge/scores/"+team, nil, w.Org); r.Status != http.StatusForbidden {
		t.Fatalf("conflicted organiser read: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "PUT", "/judge/scores/"+team, map[string]any{"scores": []map[string]any{{"criterion_id": criterionID, "points": 5}}}, w.Org); r.Status != http.StatusForbidden {
		t.Fatalf("conflicted organiser write: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "GET", "/judge/queue", nil, w.Org); len(r.JSON(t)["queue"].([]any)) != 0 {
		t.Fatalf("conflicted organiser queue: %s", r.Body)
	}
	// First publish: the conflicted organiser's score is already excluded.
	checkResult(40, 2, coComment, "judge comment")
	if r := w.Call(t, "DELETE", "/organisers/"+w.a.UserID(t, co), nil, w.Org); r.Status != http.StatusNoContent {
		t.Fatalf("remove co-organiser: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "GET", "/judge/queue", nil, co); r.Status != http.StatusNotFound {
		t.Fatalf("removed co-organiser queue: %d %s", r.Status, r.Body)
	}
	checkResult(20, 1, "judge comment")
}

func TestHackJudgingTiesAndRankOverrides(t *testing.T) {
	w := newFeatJudgingWorld(t, 3, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org))
	c1 := rv.Criteria[0].ID
	judge := w.Judges[0]
	// Teams A and B tie at 5/5=100; team C gets 3/5=60.
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	w.ScoreTeam(t, judge, w.Teams[0].ID, map[string]int{c1: 5}, nil)
	w.ScoreTeam(t, judge, w.Teams[1].ID, map[string]int{c1: 5}, nil)
	w.ScoreTeam(t, judge, w.Teams[2].ID, map[string]int{c1: 3}, nil)

	pub := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org)
	if pub.Status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.Status, pub.Body)
	}
	rows := pub.JSON(t)["results"].([]any)
	tiedIDs := []string{}
	for _, row := range rows {
		m := row.(map[string]any)
		if m["tied"] == true {
			tiedIDs = append(tiedIDs, m["team_id"].(string))
		}
	}
	if len(tiedIDs) != 2 {
		t.Fatalf("want 2 tied teams, got %v", tiedIDs)
	}
	// An override naming a team that isn't tied is refused.
	bad := w.Call(t, "POST", "/results/publish", map[string]any{
		"rank_overrides": []map[string]any{{"team_id": w.Teams[2].ID, "rank": 1}},
	}, w.Org)
	if bad.Status != http.StatusBadRequest || bad.JSON(t)["code"] != "invalid_rank_override" {
		t.Fatalf("bad override: %d %s", bad.Status, bad.Body)
	}
	// A valid override for the tied pair is accepted.
	good := w.Call(t, "POST", "/results/publish", map[string]any{
		"rank_overrides": []map[string]any{
			{"team_id": tiedIDs[0], "rank": 1},
			{"team_id": tiedIDs[1], "rank": 2},
		},
	}, w.Org)
	if good.Status != http.StatusOK {
		t.Fatalf("good override: %d %s", good.Status, good.Body)
	}
	rows2 := good.JSON(t)["results"].([]any)
	seenRanks := map[string]float64{}
	for _, row := range rows2 {
		m := row.(map[string]any)
		seenRanks[m["team_id"].(string)] = m["rank"].(float64)
		if m["team_id"] == tiedIDs[0] && m["rank"].(float64) != 1 {
			t.Fatalf("override rank 1 not applied: %v", m)
		}
		if m["team_id"] == tiedIDs[1] && m["rank"].(float64) != 2 {
			t.Fatalf("override rank 2 not applied: %v", m)
		}
	}
	// Regression: computeResults ranks densely (a tied pair shares rank 1,
	// so team C sat at rank 2 BEFORE the override, not rank 3). Resolving
	// the tie must bump every team after it down by the tie's width minus
	// one, giving C rank 3 — not leave it colliding with B's new rank 2.
	if r := seenRanks[w.Teams[2].ID]; r != 3 {
		t.Fatalf("untied team C should move to rank 3 once the tie above it resolves, got %v (full rows: %v)", r, rows2)
	}
	ranksUsed := map[float64]bool{}
	for _, m := range rows2 {
		rk := m.(map[string]any)["rank"].(float64)
		if ranksUsed[rk] {
			t.Fatalf("two teams share rank %v after resolving the tie: %v", rk, rows2)
		}
		ranksUsed[rk] = true
	}
}

func TestHackJudgingPublicResultsAndMyResults(t *testing.T) {
	w := newFeatJudgingWorld(t, 2, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org))
	c1 := rv.Criteria[0].ID
	judge := w.Judges[0]
	comment := "Great demo"
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
		t.Fatalf("start judging: %d %s", r.Status, r.Body)
	}
	w.ScoreTeam(t, judge, w.Teams[0].ID, map[string]int{c1: 5}, &comment)
	w.ScoreTeam(t, judge, w.Teams[1].ID, map[string]int{c1: 1}, nil)

	// Before publish both result routes say not published.
	pubBefore := w.Call(t, "GET", "/results", nil, hacktest.Person{})
	if pubBefore.Status != http.StatusOK || pubBefore.JSON(t)["published"] != false {
		t.Fatalf("public before publish: %d %s", pubBefore.Status, pubBefore.Body)
	}
	mine := w.Call(t, "GET", "/my-results", nil, w.Part.Person)
	if mine.Status != http.StatusOK || mine.JSON(t)["published"] != false {
		t.Fatalf("my-results before publish: %d %s", mine.Status, mine.Body)
	}

	if r := w.Call(t, "POST", "/results/publish", map[string]any{}, w.Org); r.Status != http.StatusOK {
		t.Fatalf("publish: %d %s", r.Status, r.Body)
	}

	// Winners-only by default: only team A (rank 1).
	pub := w.Call(t, "GET", "/results", nil, hacktest.Person{})
	if pub.Status != http.StatusOK {
		t.Fatalf("public results: %d %s", pub.Status, pub.Body)
	}
	winners := pub.JSON(t)["winners"].([]any)
	if len(winners) != 1 || winners[0].(map[string]any)["team_id"] != w.Teams[0].ID {
		t.Fatalf("winners: %v", winners)
	}
	if _, has := pub.JSON(t)["ranking"]; has {
		t.Fatal("winners-only view leaked ranking")
	}

	// Switch to full ranking.
	patch := w.Call(t, "PATCH", "/results", map[string]any{"full_ranking": true}, w.Org)
	if patch.Status != http.StatusOK {
		t.Fatalf("patch results: %d %s", patch.Status, patch.Body)
	}
	pub2 := w.Call(t, "GET", "/results", nil, hacktest.Person{})
	ranking := pub2.JSON(t)["ranking"].([]any)
	if len(ranking) != 2 {
		t.Fatalf("full ranking: %v", ranking)
	}
	// Never leaks raw scores or comments.
	body := string(pub2.Body)
	if strings.Contains(body, "Great demo") {
		t.Fatal("public results leaked a judge's comment")
	}

	// my-results for the participant on team A: sees own total/rank and the comment,
	// never the judge's identity.
	mine2 := w.Call(t, "GET", "/my-results", nil, w.Part.Person)
	if mine2.Status != http.StatusOK || mine2.JSON(t)["published"] != true {
		t.Fatalf("my-results after publish: %d %s", mine2.Status, mine2.Body)
	}
	comments := mine2.JSON(t)["comments"].([]any)
	if len(comments) != 1 || comments[0] != "Great demo" {
		t.Fatalf("my comments: %v", comments)
	}
	if strings.Contains(string(mine2.Body), judge.ID) {
		t.Fatal("my-results leaked the judge's id")
	}
}

func TestHackJudgingCSVExportsAndSafety(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("=cmd", "", 100, 5)), w.Org))
	c1 := rv.Criteria[0].ID
	judge := w.Judges[0]
	team := w.Teams[0].ID
	w.SetStage(t, "judging")
	w.ScoreTeam(t, judge, team, map[string]int{c1: 4}, nil)

	r := w.Call(t, "GET", "/export/scores.csv", nil, w.Org)
	if r.Status != http.StatusOK {
		t.Fatalf("scores csv: %d %s", r.Status, r.Body)
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type: %s", ct)
	}
	// A criterion name starting with "=" must be escaped by csvSafe.
	if !strings.Contains(string(r.Body), "'=cmd") {
		t.Fatalf("csvSafe not applied: %s", r.Body)
	}

	r2 := w.Call(t, "GET", "/export/results.csv", nil, w.Org)
	if r2.Status != http.StatusOK {
		t.Fatalf("results csv: %d %s", r2.Status, r2.Body)
	}
	if !strings.Contains(string(r2.Body), "team,total,rank,tied,judges_scored") {
		t.Fatalf("results csv header: %s", r2.Body)
	}

	// A participant cannot export.
	r3 := w.Call(t, "GET", "/export/scores.csv", nil, w.Part.Person)
	if r3.Status == http.StatusOK {
		t.Fatal("participant should not export scores")
	}
}

func TestUntickedOrganiserCannotJudgeAnyMode(t *testing.T) {
	for _, mode := range []string{"open", "manual"} {
		t.Run(mode, func(t *testing.T) {
			w := newFeatJudgingWorld(t, 1, 1)
			id := w.a.UserID(t, w.Org)
			if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": mode}, w.Org); r.Status != 200 {
				t.Fatal(string(r.Body))
			}
			w.SetStage(t, "judging")
			event := w.a.At(t, "GET", "/v1/hack/events/"+w.Slug, nil, w.a.Key(w.Org)).JSON(t)
			if event["can"].(map[string]any)["judge"] != false {
				t.Fatal("unticked can.judge")
			}
			for _, path := range []string{"/judge/queue", "/checklist?role=judge", "/judge/scores/" + w.Teams[0].ID} {
				if r := w.Call(t, "GET", path, nil, w.Org); r.Status < 400 {
					t.Fatalf("unticked organiser admitted: %s %s", path, r.Body)
				}
			}
			rubric := hacktest.DecodeRubric(t, w.Call(t, "GET", "/rubric", nil, w.Org))
			score := map[string]any{"scores": []map[string]any{{"criterion_id": rubric.Criteria[0].ID, "points": 3}}}
			if r := w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, score, w.Org); r.Status < 400 {
				t.Fatal("unticked score accepted")
			}
			w.SetStage(t, "closed")
			if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{id}}, w.Org); r.Status != 200 {
				t.Fatal(string(r.Body))
			}
			event = w.a.At(t, "GET", "/v1/hack/events/"+w.Slug, nil, w.a.Key(w.Org)).JSON(t)
			if event["can"].(map[string]any)["judge"] != true {
				t.Fatal("selected can.judge false")
			}
			if r := w.Call(t, "GET", "/judge/queue", nil, w.Org); r.Status != 200 {
				t.Fatalf("selected queue: %d %s", r.Status, r.Body)
			}
			w.SetStage(t, "judging")
			if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{}}, w.Org); r.Status != 409 {
				t.Fatal("selection not setup-locked")
			}
		})
	}
}

func TestUntickingPreservesScoresAndFormerJudgeCounts(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	orgID := w.a.UserID(t, w.Org)
	if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{orgID}}, w.Org); r.Status != 200 {
		t.Fatal(string(r.Body))
	}
	w.SetStage(t, "judging")
	rubric := hacktest.DecodeRubric(t, w.Call(t, "GET", "/rubric", nil, w.Org))
	points := map[string]int{}
	for _, c := range rubric.Criteria {
		points[c.ID] = 1
	}
	w.ScoreTeam(t, w.Judges[0], w.Teams[0].ID, points, nil)
	scoreRows := []map[string]any{}
	for _, c := range rubric.Criteria {
		scoreRows = append(scoreRows, map[string]any{"criterion_id": c.ID, "points": 1})
	}
	if r := w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, map[string]any{"scores": scoreRows}, w.Org); r.Status != 200 {
		t.Fatalf("selected score: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "POST", "/leave", map[string]string{"confirm": w.Slug}, w.Judges[0].Person); r.Status != 200 {
		t.Fatalf("judge leave: %d %s", r.Status, r.Body)
	}
	scored := func() float64 {
		t.Helper()
		return w.Call(t, "GET", "/judging/dashboard", nil, w.Org).JSON(t)["teams"].([]any)[0].(map[string]any)["judges_scored"].(float64)
	}
	if got := scored(); got != 2 {
		t.Fatalf("before untick: %v", got)
	}
	w.SetStage(t, "closed")
	if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{}}, w.Org); r.Status != 200 {
		t.Fatal(string(r.Body))
	}
	if got := scored(); got != 1 {
		t.Fatalf("untick changed former scores or counted unticked organiser: %v", got)
	}
	var rows int
	if err := w.a.DB.QueryRow(`SELECT count(*) FROM event_scores WHERE judge_id=$1`, orgID).Scan(&rows); err != nil || rows != len(rubric.Criteria) {
		t.Fatalf("untick deleted score rows: %d %v", rows, err)
	}
}

func TestRhhEntrySaveRulesOnRetiredPreset(t *testing.T) {
	w := newFeatRulesWorld(t, 0)
	a := w.a
	base := "/v1/hack/events/" + w.Slug + "/entry-rules"

	// Simulate an event that already existed on the "build" preset before
	// SIM-382 (a new event today starts on video_or_website instead).
	buildRules := `{"v":1,"fields":{"title":"required","problem":"required","tagline":"required","audience":"optional","track":"required","tools":"required","maturity":"required","description":"optional","video_url":"one_of","site":"one_of","code_url":"off","screenshot":"optional"}}`
	if _, err := a.DB.Exec(`UPDATE events SET entry_preset = 'build', entry_rules = $2::jsonb, judge_first = 'video' WHERE slug = $1`, w.Slug, buildRules); err != nil {
		t.Fatal(err)
	}
	before := a.At(t, "GET", base, nil, a.Key(w.Org)).JSON(t)
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

	dry := a.At(t, "PUT", base, map[string]any{"fields": fields, "judge_first": "site", "dry_run": true}, a.Key(w.Org))
	if dry.Status != 200 {
		t.Fatalf("dry run on a retired preset should succeed: %d %s", dry.Status, dry.Body)
	}
	dryPreview := dry.JSON(t)["preview"].(map[string]any)
	if dryPreview["preset"] != "build" {
		t.Fatalf("dry run preview should keep the retired preset: %#v", dryPreview)
	}

	r := a.At(t, "PUT", base, body, a.Key(w.Org))
	if r.Status != 200 {
		t.Fatalf("save on a retired preset should succeed: %d %s", r.Status, r.Body)
	}
	saved := r.JSON(t)
	if saved["preset"] != "build" {
		t.Fatalf("judge_first-only save should keep the retired preset, not demote it: %#v", saved)
	}
	if saved["judge_first"] != "site" {
		t.Fatalf("judge_first did not save: %#v", saved)
	}

	// The refused shape (what the client used to send): preset explicitly
	// named, even though it is retired.
	hacktest.WantCode(t, "preset build still refused directly", a.At(t, "PUT", base, map[string]any{"preset": "build"}, a.Key(w.Org)), 400, "invalid_preset")
}

func TestRhhResultsOldSparseSnapshotPassesThrough(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	w.SetStage(t, "results")
	snap := `[{"team_id":"t1","team_name":"Old","rank":1,"total":"n/a","legacy":"keep"}]`
	if _, err := w.a.DB.Exec(`INSERT INTO event_results (event_id, full_ranking, snapshot)
		SELECT id, true, $2::jsonb FROM events WHERE slug=$1`, w.Slug, snap); err != nil {
		t.Fatal(err)
	}
	org := w.Call(t, "GET", "/results", nil, w.Org)
	if org.Status != 200 || !strings.Contains(string(org.Body), `"legacy":"keep"`) || strings.Contains(string(org.Body), "judges_scored") {
		t.Fatalf("organiser: %d %s", org.Status, org.Body)
	}
	pub := w.Call(t, "GET", "/results", nil, hacktest.Person{})
	if pub.Status != 200 || strings.Contains(string(pub.Body), `"track_winner":false`) || !strings.Contains(string(pub.Body), `"track_winner":null`) {
		t.Fatalf("public: %d %s", pub.Status, pub.Body)
	}
	csv := w.Call(t, "GET", "/export/results.csv", nil, w.Org)
	if csv.Status != 200 || !strings.Contains(string(csv.Body), "Old") {
		t.Fatalf("csv: %d %s", csv.Status, csv.Body)
	}
}

func TestRhhFormerJudgeConflictReversible(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvconf-org")
	p1 := a.NewPerson(t, "lvconf-p1")
	p2 := a.NewPerson(t, "lvconf-p2")
	judge := a.NewPerson(t, "lvconf-j")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	a.JoinParticipant(t, join, p2, "Quinn")
	teamX, _ := a.CreateTeamAndJoin(t, slug, p1, "TeamX")
	teamY, _ := a.CreateTeamAndJoin(t, slug, p2, "TeamY")
	a.GiveSubmittedEntry(t, slug, teamX)
	a.GiveSubmittedEntry(t, slug, teamY)
	a.JoinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.DB.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.FirstCriterionID(t, slug)
	hacktest.WantTS(t, "score X", a.PutScores(t, "/v1/hack/events/"+slug, teamX, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.Key(judge)), 200, "")
	hacktest.WantTS(t, "score Y", a.PutScores(t, "/v1/hack/events/"+slug, teamY, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.Key(judge)), 200, "")

	eventID := a.EventID(t, slug)
	judgeID := a.UserID(t, judge)
	// A conflict declared on X by mistake, in place when the judge leaves.
	if _, err := a.DB.Exec(`INSERT INTO event_conflicts (event_id, judge_id, team_id) VALUES ($1, $2, $3)`, eventID, judgeID, teamX); err != nil {
		t.Fatal(err)
	}
	hacktest.WantTS(t, "leave", a.Leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	var countedIDs []string
	if err := a.DB.QueryRow(`SELECT counted_team_ids FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(pq.Array(&countedIDs)); err != nil {
		t.Fatal(err)
	}
	has := func(id string) bool {
		for _, c := range countedIDs {
			if c == id {
				return true
			}
		}
		return false
	}
	if !has(teamX) || !has(teamY) {
		t.Fatalf("counted_team_ids must include every scored team regardless of a conflict at leave time: %v", countedIDs)
	}

	// With the conflict still in place, X must not count (live check).
	dash := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	teamsByID := map[string]map[string]any{}
	for _, raw := range dash["teams"].([]any) {
		team := raw.(map[string]any)
		teamsByID[team["team_id"].(string)] = team
	}
	if teamsByID[teamX]["judges_scored"] != float64(0) {
		t.Fatalf("X should not count while the conflict stands: %v", teamsByID[teamX])
	}
	if teamsByID[teamY]["judges_scored"] != float64(1) {
		t.Fatalf("Y should count: %v", teamsByID[teamY])
	}

	// Remove the conflict: X must come back, because it was never dropped
	// from counted_team_ids to begin with.
	hacktest.WantTS(t, "remove conflict", a.At(t, "DELETE", "/v1/hack/events/"+slug+"/conflicts/"+teamX+"?judge_user_id="+judgeID, nil, a.Key(org)), 204, "")
	dash2 := a.At(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.Key(org)).JSON(t)
	for _, raw := range dash2["teams"].([]any) {
		team := raw.(map[string]any)
		if team["team_id"] == teamX {
			if team["judges_scored"] != float64(1) {
				t.Fatalf("X should count again once the conflict is gone: %v", team)
			}
		}
	}
}

func TestRhhLeaveKeepsTotalsAndRanksStable(t *testing.T) {
	a := newFeatApp(t)
	org := a.NewPerson(t, "lvstableorg")
	p1 := a.NewPerson(t, "lvstablep1")
	p2 := a.NewPerson(t, "lvstablep2")
	judge1 := a.NewPerson(t, "lvstablej1")
	judge2 := a.NewPerson(t, "lvstablej2")
	slug := hacktest.UniqueSlug()
	hacktest.WantTS(t, "create", a.CreateEvent(t, org, slug, nil), 201, "")
	a.OpenEvent(t, org, slug)
	join, judgeCode := a.JoinCodes(t, org, slug)
	a.JoinParticipant(t, join, p1, "Pat")
	a.JoinParticipant(t, join, p2, "Quinn")
	teamA, _ := a.CreateTeamAndJoin(t, slug, p1, "TeamA")
	teamB, _ := a.CreateTeamAndJoin(t, slug, p2, "TeamB")
	a.GiveSubmittedEntry(t, slug, teamA)
	a.GiveSubmittedEntry(t, slug, teamB)
	a.JoinJudge(t, judgeCode, judge1, "Judge1")
	a.JoinJudge(t, judgeCode, judge2, "Judge2")
	base := "/v1/hack/events/" + slug
	hacktest.WantTS(t, "start judging", a.SetStage(t, base, "judging", a.Key(org)), 200, "")
	criterion := a.FirstCriterionID(t, slug)
	score := func(judge hacktest.Person, teamID string, points int) {
		hacktest.WantTS(t, "score", a.PutScores(t, base, teamID, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": points}}}, a.Key(judge)), 200, "")
	}
	score(judge1, teamA, 3)
	score(judge1, teamB, 4)
	score(judge2, teamA, 5)
	score(judge2, teamB, 1)

	snapshot := func() map[string]map[string]any {
		pub := a.At(t, "POST", base+"/results/publish", map[string]any{}, a.Key(org))
		if pub.Status != 200 {
			t.Fatalf("publish: %d %s", pub.Status, pub.Body)
		}
		out := map[string]map[string]any{}
		for _, raw := range pub.JSON(t)["results"].([]any) {
			row := raw.(map[string]any)
			out[row["team_id"].(string)] = row
		}
		return out
	}

	before := snapshot()
	hacktest.WantTS(t, "judge1 leaves", a.Leave(t, slug, judge1, map[string]any{"confirm": slug}), 200, "")
	after := snapshot()

	for _, teamID := range []string{teamA, teamB} {
		b, af := before[teamID], after[teamID]
		if b["total"] != af["total"] || b["rank"] != af["rank"] || b["judges_scored"] != af["judges_scored"] {
			t.Fatalf("team %s changed across the leave: before=%v after=%v", teamID, b, af)
		}
	}
}

func decodeConflict(t *testing.T, r hacktest.Resp) conflictView {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("conflict: %d %s", r.Status, r.Body)
	}
	var v conflictView
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

type conflictView struct {
	JudgeID    string `json:"judge_id"`
	JudgeName  string `json:"judge_name"`
	TeamID     string `json:"team_id"`
	TeamName   string `json:"team_name"`
	DeclaredBy string `json:"declared_by"`
	HadScore   bool   `json:"had_score"`
}

func TestHackJudgingScoringStageAndAutomaticOrganisers(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 1)
	c := hacktest.DecodeRubric(t, w.Call(t, "GET", "/rubric", nil, w.Org)).Criteria[0]
	for _, stage := range []string{"open", "building", "results", "closed", "judging"} {
		if _, err := w.a.DB.Exec(`UPDATE events SET stage=$2 WHERE slug=$1`, w.Slug, stage); err != nil {
			t.Fatal(err)
		}
		r := w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, map[string]any{"scores": []map[string]any{{"criterion_id": c.ID, "points": 1}}}, w.Judges[0].Person)
		if stage == "judging" {
			if r.Status != 200 {
				t.Fatalf("%s: %d %s", stage, r.Status, r.Body)
			}
		} else {
			hacktest.WantTS(t, stage, r, 409, "scoring_not_open")
		}
	}
	if _, err := w.a.DB.Exec(`UPDATE users SET is_admin=true WHERE username=$1`, w.Org.Email); err != nil {
		t.Fatal(err)
	}
	pages.RegisterDemo(w.a.Mux, w.a.hack.Hack, demoPolicyPages{}, w.Slug)
	if _, err := w.a.DB.Exec(`UPDATE events SET stage='building' WHERE slug=$1`, w.Slug); err != nil {
		t.Fatal(err)
	}
	if r := w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, map[string]any{"scores": []map[string]any{{"criterion_id": c.ID, "points": 1}}}, w.Judges[0].Person); r.Status != 200 {
		t.Fatalf("demo scoring while building: %d %s", r.Status, r.Body)
	}
	decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "automatic", "judges_per_team": 2}, w.Org))
	rows, _ := decodeAssignments(t, w.Call(t, "POST", "/assignments/generate", nil, w.Org))
	if len(rows) != 1 || rows[0].JudgeID != w.Judges[0].ID {
		t.Fatalf("default judges: %+v", rows)
	}
	rows, _ = decodeAssignments(t, w.Call(t, "POST", "/assignments/generate", map[string]any{"include_organisers": true}, w.Org))
	if len(rows) != 2 {
		t.Fatalf("include organisers: %+v", rows)
	}
}

// This scoring test registers demo mode without serving a page. The only
// presentation dependency registration needs is its HTTP wrapper.
type demoPolicyPages struct{ pages.Pages }

func (demoPolicyPages) AdminUICSP(next http.Handler) http.Handler { return next }

func decodeSettings(t *testing.T, r hacktest.Resp) judgingSettings {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("settings: %d %s", r.Status, r.Body)
	}
	var v judgingSettings
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

type judgingSettings struct {
	Mode string `json:"assignment_mode"`
	Per  int    `json:"judges_per_team"`
}

func decodeAssignments(t *testing.T, r hacktest.Resp) (rows []assignmentRow, under []string) {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("assignments: %d %s", r.Status, r.Body)
	}
	var body struct {
		Assignments []assignmentRow `json:"assignments"`
		Under       []string        `json:"teams_under_target"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatalf("assignments json: %v body %s", err, r.Body)
	}
	if body.Assignments == nil || body.Under == nil {
		t.Fatalf("assignments or teams_under_target is null: %s", r.Body)
	}
	return body.Assignments, body.Under
}

type assignmentRow struct {
	JudgeID   string `json:"judge_id"`
	JudgeName string `json:"judge_name"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
}
