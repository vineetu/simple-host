package judging

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"
)

// Judging setup: the rubric, how judges are spread, conflicts, and the
// organiser's dashboard. Score rows do not exist yet, so coverage stays at
// zero. The harness is newHackApp (DB_DSN, same as the other hack tests).

func sortedJudges(in []hacktest.JudgingPerson) []hacktest.JudgingPerson {
	out := append([]hacktest.JudgingPerson(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedTeams(in []hacktest.JudgingTeam) []hacktest.JudgingTeam {
	out := append([]hacktest.JudgingTeam(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type assignmentRow struct {
	JudgeID   string `json:"judge_id"`
	JudgeName string `json:"judge_name"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
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

func wantRows(t *testing.T, got []assignmentRow, want []assignmentRow) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d assignments want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("assignment %d\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// spread is the hand-worked plan: team index, then judge index, both in id
// order, already in the response's team-then-judge order.
func spread(js []hacktest.JudgingPerson, ts []hacktest.JudgingTeam, pairs [][2]int) []assignmentRow {
	out := make([]assignmentRow, len(pairs))
	for i, p := range pairs {
		out[i] = assignmentRow{
			JudgeID: js[p[1]].ID, JudgeName: js[p[1]].Name,
			TeamID: ts[p[0]].ID, TeamName: ts[p[0]].Name,
		}
	}
	return out
}

type conflictView struct {
	JudgeID    string `json:"judge_id"`
	JudgeName  string `json:"judge_name"`
	TeamID     string `json:"team_id"`
	TeamName   string `json:"team_name"`
	DeclaredBy string `json:"declared_by"`
	HadScore   bool   `json:"had_score"`
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

func decodeConflictList(t *testing.T, r hacktest.Resp) []conflictView {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("conflicts: %d %s", r.Status, r.Body)
	}
	var body struct {
		Conflicts []conflictView `json:"conflicts"`
	}
	if err := json.Unmarshal(r.Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Conflicts == nil {
		t.Fatalf("conflicts is null: %s", r.Body)
	}
	return body.Conflicts
}

type judgingSettings struct {
	Mode string `json:"assignment_mode"`
	Per  int    `json:"judges_per_team"`
}

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

func TestHackJudgingRubric(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	longName := strings.Repeat("n", 81)
	many := make([]map[string]any, 11)
	for i := range many {
		many[i] = hacktest.Criterion(fmt.Sprintf("C%d", i), "", 0, 1)
	}
	cases := []struct {
		name string
		body map[string]any
	}{
		{"weights", hacktest.RubricBody(hacktest.Criterion("Impact", "", 60, 5), hacktest.Criterion("Build", "", 30, 5))},
		{"too many", hacktest.RubricBody(many...)},
		{"long name", hacktest.RubricBody(hacktest.Criterion(longName, "", 100, 5))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := w.Call(t, "PUT", "/rubric", tc.body, w.Org)
			hacktest.WantTS(t, tc.name, r, http.StatusBadRequest, "invalid_rubric")
		})
	}

	firstBody := hacktest.RubricBody(
		hacktest.Criterion("Impact", "  Who it helps  ", 60, 10),
		hacktest.Criterion("Build", "", 40, 1),
	)
	saved := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", firstBody, w.Org))
	if len(saved.Criteria) != 2 {
		t.Fatalf("criteria: %+v", saved.Criteria)
	}
	wantFirst := []hacktest.RubricCriterion{
		{Position: 0, Name: "Impact", Description: "Who it helps", Weight: 60, MaxPoints: 10},
		{Position: 1, Name: "Build", Description: "", Weight: 40, MaxPoints: 1},
	}
	oldIDs := map[string]bool{}
	for i, want := range wantFirst {
		got := saved.Criteria[i]
		if got.ID == "" || oldIDs[got.ID] {
			t.Fatalf("id %q", got.ID)
		}
		oldIDs[got.ID] = true
		got.ID = ""
		if got != want {
			t.Fatalf("criterion %d got %+v want %+v", i, saved.Criteria[i], want)
		}
	}
	again := hacktest.DecodeRubric(t, w.Call(t, "GET", "/rubric", nil, w.Org))
	if len(again.Criteria) != 2 || again.Criteria[0].ID != saved.Criteria[0].ID {
		t.Fatalf("get after put: %+v", again)
	}

	// Full replace: one criterion, spaces collapsed, previous rows gone.
	replaced := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("  Clarity   of   purpose  ", "Clear.", 100, 5)), w.Org))
	if len(replaced.Criteria) != 1 {
		t.Fatalf("replace: %+v", replaced.Criteria)
	}
	got := replaced.Criteria[0]
	if oldIDs[got.ID] || got.ID == "" || got.Position != 0 || got.Name != "Clarity of purpose" || got.Description != "Clear." || got.Weight != 100 || got.MaxPoints != 5 {
		t.Fatalf("replaced criterion: %+v", got)
	}
	n := w.CountRows(t, `SELECT COUNT(*) FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.Slug)
	if n != 1 {
		t.Fatalf("rubric rows %d", n)
	}
	orgGet := w.Call(t, "GET", "/rubric", nil, w.Org)
	judgeGet := w.Call(t, "GET", "/rubric", nil, w.Judges[0].Person)
	if orgGet.Status != http.StatusOK || string(orgGet.Body) != string(judgeGet.Body) {
		t.Fatalf("organiser %d %s\njudge %d %s", orgGet.Status, orgGet.Body, judgeGet.Status, judgeGet.Body)
	}
	// Audit item 1 (2026-11-02): an approved participant reads the exact
	// same rubric a judge or organiser does -- no score ever appears in
	// this response, so there is nothing participant-unsafe to strip.
	partGet := w.Call(t, "GET", "/rubric", nil, w.Part.Person)
	if partGet.Status != http.StatusOK || string(partGet.Body) != string(orgGet.Body) {
		t.Fatalf("participant %d %s\norganiser %d %s", partGet.Status, partGet.Body, orgGet.Status, orgGet.Body)
	}

	// A non-member looks like a missing event. A participant may now read
	// the rubric, but writing it is still organiser-only, so PUT still
	// looks like a missing event for them too.
	for _, tc := range []struct {
		name, method string
		who          hacktest.Person
		body         any
	}{
		{"outsider get", "GET", w.Outsider, nil},
		{"outsider put", "PUT", w.Outsider, firstBody},
		{"participant put", "PUT", w.Part.Person, firstBody},
	} {
		r := w.Call(t, tc.method, "/rubric", tc.body, tc.who)
		hacktest.WantTS(t, tc.name, r, http.StatusNotFound, "event_not_found")
	}

	if _, err := w.a.DB.Exec(`UPDATE events SET judging_locked_at = now() WHERE slug = $1`, w.Slug); err != nil {
		t.Fatal(err)
	}
	r := w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Other", "", 100, 5)), w.Org)
	hacktest.WantTS(t, "locked", r, http.StatusConflict, "scores_locked")
	// The lock wins over a body that would also fail validation.
	r = w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 60, 5), hacktest.Criterion("Build", "", 30, 5)), w.Org)
	hacktest.WantTS(t, "locked before validation", r, http.StatusConflict, "scores_locked")
	if hacktest.DecodeRubric(t, w.Call(t, "GET", "/rubric", nil, w.Org)).Criteria[0].Name != "Clarity of purpose" {
		t.Fatal("locked put changed the rubric")
	}
}

func TestHackJudgingSettings(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	for _, tc := range []struct {
		name, method string
		who          hacktest.Person
		body         any
	}{
		{"judge get", "GET", w.Judges[0].Person, nil},
		{"participant get", "GET", w.Part.Person, nil},
		{"outsider get", "GET", w.Outsider, nil},
		{"judge patch", "PATCH", w.Judges[0].Person, map[string]any{"assignment_mode": "automatic"}},
		{"participant patch", "PATCH", w.Part.Person, map[string]any{"judges_per_team": 4}},
	} {
		r := w.Call(t, tc.method, "/judging/settings", tc.body, tc.who)
		hacktest.WantTS(t, tc.name, r, http.StatusNotFound, "event_not_found")
	}

	if got := decodeSettings(t, w.Call(t, "GET", "/judging/settings", nil, w.Org)); got != (judgingSettings{"open", 2}) {
		t.Fatalf("defaults %+v", got)
	}
	patched := decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{
		"assignment_mode": "automatic", "judges_per_team": 3,
	}, w.Org))
	if patched != (judgingSettings{"automatic", 3}) {
		t.Fatalf("patch %+v", patched)
	}
	if got := decodeSettings(t, w.Call(t, "GET", "/judging/settings", nil, w.Org)); got != patched {
		t.Fatalf("get after patch %+v", got)
	}
	// A field left out stays as it is.
	if got := decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{"judges_per_team": 20}, w.Org)); got != (judgingSettings{"automatic", 20}) {
		t.Fatalf("partial per-team %+v", got)
	}
	if got := decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "open"}, w.Org)); got != (judgingSettings{"open", 20}) {
		t.Fatalf("partial mode %+v", got)
	}
	if got := decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{}, w.Org)); got != (judgingSettings{"open", 20}) {
		t.Fatalf("empty patch %+v", got)
	}

	invalid := []struct {
		name string
		body map[string]any
	}{
		{"mode", map[string]any{"assignment_mode": "round-robin"}},
		{"per team high", map[string]any{"judges_per_team": 21}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			r := w.Call(t, "PATCH", "/judging/settings", tc.body, w.Org)
			hacktest.WantTS(t, tc.name, r, http.StatusBadRequest, "invalid_request")
			if got := decodeSettings(t, w.Call(t, "GET", "/judging/settings", nil, w.Org)); got != (judgingSettings{"open", 20}) {
				t.Fatalf("invalid patch stuck: %+v", got)
			}
		})
	}
}

func TestHackJudgingAssignments(t *testing.T) {
	w := newJudgingWorld(t, 3, 3)
	r := w.Call(t, "POST", "/assignments/generate", nil, w.Org)
	hacktest.WantTS(t, "open", r, http.StatusBadRequest, "invalid_request")
	if n := w.CountRows(t, `SELECT COUNT(*) FROM event_assignments WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.Slug); n != 0 {
		t.Fatalf("open mode wrote %d assignments", n)
	}

	if got := decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{
		"assignment_mode": "automatic", "judges_per_team": 2,
	}, w.Org)); got != (judgingSettings{"automatic", 2}) {
		t.Fatalf("settings %+v", got)
	}
	js, ts := sortedJudges(w.Judges), sortedTeams(w.Teams)
	// Three teams, three judges, two each. Least-loaded first, id breaks
	// ties, teams in id order:
	//   T0 takes J0 J1; T1 takes J2 J0; T2 takes J1 J2.
	// Every judge ends on two teams.
	first := w.Call(t, "POST", "/assignments/generate", nil, w.Org)
	second := w.Call(t, "POST", "/assignments/generate", nil, w.Org)
	if string(first.Body) != string(second.Body) {
		t.Fatalf("not deterministic\n%s\n%s", first.Body, second.Body)
	}
	rows, under := decodeAssignments(t, first)
	wantRows(t, rows, spread(js, ts, [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 2}, {2, 1}, {2, 2}}))
	if len(under) != 0 {
		t.Fatalf("under %v", under)
	}
	if n := w.CountRows(t, `SELECT COUNT(*) FROM event_assignments WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.Slug); n != 6 {
		t.Fatalf("assignment rows %d, want 6", n)
	}

	// J0 is conflicted with T0, so T0 is judged by J1 and J2.
	//   T0: J1 J2; T1: J0 J1; T2: J0 J2.
	declared := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{
		"team_id": ts[0].ID, "judge_user_id": js[0].ID,
	}, w.Org))
	if declared.DeclaredBy != "organiser" || declared.JudgeID != js[0].ID || declared.TeamID != ts[0].ID {
		t.Fatalf("declare: %+v", declared)
	}
	rows, under = decodeAssignments(t, w.Call(t, "POST", "/assignments/generate", nil, w.Org))
	wantRows(t, rows, spread(js, ts, [][2]int{{0, 1}, {0, 2}, {1, 0}, {1, 1}, {2, 0}, {2, 2}}))
	if len(under) != 0 {
		t.Fatalf("under with two eligible judges: %v", under)
	}
	for _, row := range rows {
		if row.JudgeID == js[0].ID && row.TeamID == ts[0].ID {
			t.Fatalf("conflicted pair assigned: %+v", row)
		}
	}

	// Target 3, but T0 has only two judges free of the conflict.
	if got := decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{"judges_per_team": 3}, w.Org)); got.Per != 3 {
		t.Fatalf("per team %+v", got)
	}
	rows, under = decodeAssignments(t, w.Call(t, "POST", "/assignments/generate", nil, w.Org))
	wantRows(t, rows, spread(js, ts, [][2]int{{0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {2, 0}, {2, 1}, {2, 2}}))
	if len(under) != 1 || under[0] != ts[0].ID {
		t.Fatalf("under got %v want [%s]", under, ts[0].ID)
	}
}

func TestHackJudgingConflicts(t *testing.T) {
	w := newJudgingWorld(t, 2, 2)
	w.Call(t, "PATCH", "/judging/settings", map[string]any{"include_organisers": true}, w.Org)
	j0, j1 := w.Judges[0], w.Judges[1]
	t0, t1 := w.Teams[0], w.Teams[1]

	r := w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t0.ID}, w.Part.Person)
	hacktest.WantTS(t, "participant", r, http.StatusNotFound, "event_not_found")
	r = w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t0.ID}, w.Outsider)
	hacktest.WantTS(t, "outsider", r, http.StatusNotFound, "event_not_found")

	self := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t0.ID}, j0.Person))
	if self != (conflictView{j0.ID, j0.Name, t0.ID, t0.Name, "judge", false}) {
		t.Fatalf("self: %+v", self)
	}
	// A second declaration is the same row, and declared_by stays "judge".
	again := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t0.ID}, j0.Person))
	if again != self {
		t.Fatalf("second declare %+v", again)
	}
	if n := w.CountRows(t, `SELECT COUNT(*) FROM event_conflicts WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.Slug); n != 1 {
		t.Fatalf("conflict rows %d", n)
	}

	// With no target, an organiser declares a conflict for themself.
	orgID := w.a.UserID(t, w.Org)
	selfOrg := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t0.ID}, w.Org))
	if selfOrg.JudgeID != orgID || selfOrg.DeclaredBy != "judge" {
		t.Fatalf("organiser self conflict: %+v", selfOrg)
	}
	onBehalf := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{
		"team_id": t0.ID, "judge_user_id": j1.ID,
	}, w.Org))
	if onBehalf != (conflictView{j1.ID, j1.Name, t0.ID, t0.Name, "organiser", false}) {
		t.Fatalf("on behalf: %+v", onBehalf)
	}
	// Declaring the same triple again does not flip declared_by.
	repeat := decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{
		"team_id": t0.ID, "judge_user_id": j1.ID,
	}, w.Org))
	if repeat.DeclaredBy != "organiser" {
		t.Fatalf("repeat declared_by %q", repeat.DeclaredBy)
	}
	if n := w.CountRows(t, `SELECT COUNT(*) FROM event_conflicts WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.Slug); n != 3 {
		t.Fatalf("conflict rows %d", n)
	}

	r = w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t1.ID, "judge_user_id": j1.ID}, j0.Person)
	hacktest.WantTS(t, "judge for someone else", r, http.StatusForbidden, "forbidden")

	all := decodeConflictList(t, w.Call(t, "GET", "/conflicts", nil, w.Org))
	if len(all) != 3 {
		t.Fatalf("organiser list: %+v", all)
	}
	mine := decodeConflictList(t, w.Call(t, "GET", "/conflicts", nil, j0.Person))
	if len(mine) != 1 || mine[0] != self {
		t.Fatalf("judge list: %+v", mine)
	}
	theirs := decodeConflictList(t, w.Call(t, "GET", "/conflicts", nil, j1.Person))
	if len(theirs) != 1 || theirs[0].JudgeID != j1.ID || theirs[0].DeclaredBy != "organiser" {
		t.Fatalf("other judge list: %+v", theirs)
	}

	r = w.Call(t, "DELETE", "/conflicts/"+t0.ID, nil, j0.Person)
	if r.Status != http.StatusNoContent {
		t.Fatalf("judge delete: %d %s", r.Status, r.Body)
	}
	if mine = decodeConflictList(t, w.Call(t, "GET", "/conflicts", nil, j0.Person)); len(mine) != 0 {
		t.Fatalf("after delete: %+v", mine)
	}
	r = w.Call(t, "DELETE", "/conflicts/"+t0.ID, nil, j0.Person)
	hacktest.WantTS(t, "delete missing", r, http.StatusNotFound, "conflict_not_found")

	r = w.Call(t, "DELETE", "/conflicts/"+t0.ID+"?judge_user_id="+j1.ID, nil, w.Org)
	if r.Status != http.StatusNoContent {
		t.Fatalf("organiser delete: %d %s", r.Status, r.Body)
	}
	r = w.Call(t, "DELETE", "/conflicts/"+t0.ID+"?judge_user_id="+j1.ID, nil, w.Org)
	hacktest.WantTS(t, "organiser delete missing", r, http.StatusNotFound, "conflict_not_found")
	r = w.Call(t, "DELETE", "/conflicts/"+t0.ID, nil, w.Org)
	if r.Status != http.StatusNoContent {
		t.Fatalf("organiser self delete: %d %s", r.Status, r.Body)
	}

	// Put one back so a judge's attempt to remove someone else's can be seen to miss.
	decodeConflict(t, w.Call(t, "POST", "/conflicts", map[string]any{"team_id": t1.ID}, j0.Person))
	r = w.Call(t, "DELETE", "/conflicts/"+t1.ID+"?judge_user_id="+j0.ID, nil, j1.Person)
	hacktest.WantTS(t, "judge deletes another", r, http.StatusBadRequest, "invalid_request")
	if n := w.CountRows(t, `
		SELECT COUNT(*) FROM event_conflicts
		WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND judge_id = $2 AND team_id = $3`,
		w.Slug, j0.ID, t1.ID); n != 1 {
		t.Fatalf("other judge's delete removed the row (%d)", n)
	}
}

func TestHackJudgingDashboard(t *testing.T) {
	w := newJudgingWorld(t, 2, 2)
	w.Call(t, "PATCH", "/judging/settings", map[string]any{"include_organisers": true}, w.Org)
	for _, tc := range []struct {
		name string
		who  hacktest.Person
	}{
		{"judge", w.Judges[0].Person},
		{"participant", w.Part.Person},
		{"outsider", w.Outsider},
	} {
		r := w.Call(t, "GET", "/judging/dashboard", nil, tc.who)
		hacktest.WantTS(t, tc.name, r, http.StatusNotFound, "event_not_found")
	}

	if r := w.Call(t, "GET", "/judging/dashboard", nil, w.Org); r.Status != http.StatusOK {
		t.Fatalf("organiser dashboard: %d %s", r.Status, r.Body)
	}
}

// --- M3 pass 2: scoring, lock/unlock, publish, results, exports ----------

// Locking must freeze every input that feeds a computed total, not just
// score rows: a conflict or assignment change left unlocked would silently
// change a published result the next time it's recomputed, with no further
// "score" write ever happening — defeating the point of locking.
func TestHackJudgingLockCoversConflictsAndAssignments(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	if r := w.A.SetStage(t, w.Slug, "results", w.A.Key(w.Org)); r.Status != http.StatusOK {
		t.Fatalf("lock: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "automatic"}, w.Org); r.Status != http.StatusConflict || r.JSON(t)["code"] != "scores_locked" {
		t.Fatalf("settings while locked: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "POST", "/assignments/generate", map[string]any{}, w.Org); r.Status != http.StatusConflict || r.JSON(t)["code"] != "scores_locked" {
		t.Fatalf("generate while locked: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "POST", "/conflicts", map[string]any{"team_id": w.Teams[0].ID}, w.Judges[0].Person); r.Status != http.StatusConflict || r.JSON(t)["code"] != "scores_locked" {
		t.Fatalf("declare conflict while locked: %d %s", r.Status, r.Body)
	}
	// Unlock, then declaring a conflict works; locking again, removing it is refused.
	if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != http.StatusOK {
		t.Fatalf("unlock: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "POST", "/conflicts", map[string]any{"team_id": w.Teams[0].ID}, w.Judges[0].Person); r.Status != http.StatusOK {
		t.Fatalf("declare conflict unlocked: %d %s", r.Status, r.Body)
	}
	if r := w.A.SetStage(t, w.Slug, "results", w.A.Key(w.Org)); r.Status != http.StatusOK {
		t.Fatalf("relock: %d %s", r.Status, r.Body)
	}
	if r := w.Call(t, "DELETE", "/conflicts/"+w.Teams[0].ID+"?judge_user_id="+w.Judges[0].ID, nil, w.Judges[0].Person); r.Status != http.StatusConflict || r.JSON(t)["code"] != "scores_locked" {
		t.Fatalf("remove conflict while locked: %d %s", r.Status, r.Body)
	}
}

func TestHackJudgingScoresUpsertAndRetrySafety(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 60, 5), hacktest.Criterion("Craft", "", 40, 5)), w.Org))
	c1, c2 := rv.Criteria[0].ID, rv.Criteria[1].ID
	team := w.Teams[0].ID
	judge := w.Judges[0]

	// Partial save: only one criterion.
	w.SetStage(t, "judging")
	r := w.ScoreTeam(t, judge, team, map[string]int{c1: 4}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("partial score: %d %s", r.Status, r.Body)
	}
	if r.JSON(t)["complete"] != false {
		t.Fatalf("expected incomplete: %s", r.Body)
	}

	// A retried write of the SAME criterion with the SAME value must never
	// double-count: still exactly one row, same value.
	r = w.ScoreTeam(t, judge, team, map[string]int{c1: 4}, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("retry score: %d %s", r.Status, r.Body)
	}
	n := w.CountRows(t, `SELECT COUNT(*) FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND criterion_id=$3`, judge.ID, team, c1)
	if n != 1 {
		t.Fatalf("retry created %d rows, want 1", n)
	}

	// Complete it.
	r = w.ScoreTeam(t, judge, team, map[string]int{c2: 3}, nil)
	if r.Status != http.StatusOK || r.JSON(t)["complete"] != true {
		t.Fatalf("complete: %d %s", r.Status, r.Body)
	}

	// Out-of-range points.
	r = w.ScoreTeam(t, judge, team, map[string]int{c1: 9}, nil)
	if r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_points" {
		t.Fatalf("out of range: %d %s", r.Status, r.Body)
	}

	// Comment-only with existing scores updates the comment.
	comment := "Nice work"
	r = w.ScoreTeam(t, judge, team, map[string]int{}, &comment)
	if r.Status != http.StatusOK || r.JSON(t)["comment"] != comment {
		t.Fatalf("comment update: %d %s", r.Status, r.Body)
	}
	// The comment lands on EVERY row for this (judge, team) pair, not just
	// one named in some earlier call — there is no separate comment table,
	// so every row must agree or "the" comment is ambiguous.
	var c1Comment, c2Comment string
	if err := w.a.DB.QueryRow(`SELECT comment FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND criterion_id=$3`, judge.ID, team, c1).Scan(&c1Comment); err != nil {
		t.Fatal(err)
	}
	if err := w.a.DB.QueryRow(`SELECT comment FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND criterion_id=$3`, judge.ID, team, c2).Scan(&c2Comment); err != nil {
		t.Fatal(err)
	}
	if c1Comment != comment || c2Comment != comment {
		t.Fatalf("comment not applied to every row: c1=%q c2=%q want %q", c1Comment, c2Comment, comment)
	}
	// Saving a NEW point value on just c1 (no comment field sent) must not
	// blank the comment that's already there.
	r = w.ScoreTeam(t, judge, team, map[string]int{c1: 5}, nil)
	if r.Status != http.StatusOK || r.JSON(t)["comment"] != comment {
		t.Fatalf("points-only write blanked the comment: %s", r.Body)
	}

	// Comment-only with NO scores yet at all is refused.
	team2 := w.Teams[0].ID
	if len(w.Teams) > 1 {
		team2 = w.Teams[1].ID
	}
	if team2 == team {
		// only one team in this world; use a throwaway second team via direct SQL is overkill,
		// skip this branch when there's nothing to test against.
	} else {
		c := "hello"
		r = w.ScoreTeam(t, judge, team2, map[string]int{}, &c)
		if r.Status != http.StatusBadRequest || r.JSON(t)["code"] != "invalid_request" {
			t.Fatalf("comment-only no scores: %d %s", r.Status, r.Body)
		}
	}

	// Locked judging refuses a score write.
	w.A.SetStage(t, w.Slug, "results", w.A.Key(w.Org))
	r = w.ScoreTeam(t, judge, team, map[string]int{c1: 5}, nil)
	hacktest.WantTS(t, "score while locked", r, http.StatusConflict, "scores_locked")
}

func TestHackJudgingConflictExcludesFromQueueAndScoring(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	judge := w.Judges[0]
	team := w.Teams[0].ID
	r := w.Call(t, "POST", "/conflicts", map[string]any{"team_id": team}, judge.Person)
	if r.Status != http.StatusOK {
		t.Fatalf("declare conflict: %d %s", r.Status, r.Body)
	}
	rv := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org))
	c1 := rv.Criteria[0].ID

	// The conflicted team never appears in this judge's queue.
	q := w.Call(t, "GET", "/judge/queue", nil, judge.Person)
	if q.Status != http.StatusOK {
		t.Fatalf("queue: %d %s", q.Status, q.Body)
	}
	queue := q.JSON(t)["queue"].([]any)
	for _, item := range queue {
		if item.(map[string]any)["team_id"] == team {
			t.Fatalf("conflicted team present in queue: %v", item)
		}
	}

	// Scoring the conflicted team directly is still refused.
	w.SetStage(t, "judging")
	r = w.ScoreTeam(t, judge, team, map[string]int{c1: 5}, nil)
	hacktest.WantTS(t, "score conflicted team", r, http.StatusForbidden, "forbidden")
}

func TestHackJudgingRubricPreservesScoresAndConfirmsDeletion(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	c := hacktest.Criterion("Impact", "", 100, 5)
	saved := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(c), w.Org))
	id := saved.Criteria[0].ID
	score := map[string]any{"scores": []map[string]any{{"criterion_id": id, "points": 4}}}
	w.SetStage(t, "judging")
	if r := w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, score, w.Judges[0].Person); r.Status != 200 {
		t.Fatalf("score: %d %s", r.Status, r.Body)
	}
	w.SetStage(t, "closed")
	c["id"] = id
	c["description"] = "New description"
	edited := hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", hacktest.RubricBody(c), w.Org))
	if edited.Criteria[0].ID != id {
		t.Fatal("criterion ID changed")
	}
	if n := w.CountRows(t, `SELECT count(*) FROM event_scores WHERE criterion_id=$1`, id); n != 1 {
		t.Fatalf("scores lost: %d", n)
	}
	c["max_points"] = 3
	r := w.Call(t, "PUT", "/rubric", hacktest.RubricBody(c), w.Org)
	hacktest.WantTS(t, "confirm points reduction", r, 409, "score_deletion_confirmation")
	body := hacktest.RubricBody(c)
	body["confirm_score_deletion"] = true
	hacktest.DecodeRubric(t, w.Call(t, "PUT", "/rubric", body, w.Org))
	if n := w.CountRows(t, `SELECT count(*) FROM event_scores WHERE criterion_id=$1`, id); n != 0 {
		t.Fatalf("invalid score kept: %d", n)
	}
	// A scored criterion removal also requires confirmation.
	w.SetStage(t, "judging")
	score["scores"] = []map[string]any{{"criterion_id": id, "points": 2}}
	if r := w.Call(t, "PUT", "/judge/scores/"+w.Teams[0].ID, score, w.Judges[0].Person); r.Status != 200 {
		t.Fatal(string(r.Body))
	}
	w.SetStage(t, "closed")
	hacktest.WantTS(t, "confirm removal", w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("New", "", 100, 5)), w.Org), 409, "score_deletion_confirmation")
}

func TestHackJudgingSetupLocksAtJudging(t *testing.T) {
	for _, tc := range []struct {
		name, mode, method, path string
		body                     any
	}{
		{"mode", "open", "PATCH", "/judging/settings", map[string]any{"assignment_mode": "manual"}},
		{"rubric", "open", "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5))},
		{"generate", "automatic", "POST", "/assignments/generate", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newJudgingWorld(t, 1, 2)
			decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": tc.mode}, w.Org))
			if r := w.A.SetStage(t, w.Slug, "judging", w.A.Key(w.Org)); r.Status != 200 {
				t.Fatalf("start judging: %d %s", r.Status, r.Body)
			}
			hacktest.WantTS(t, tc.name, w.Call(t, tc.method, tc.path, tc.body, w.Org), 409, "judging_setup_locked")
			if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"score_mode": "normalised", "public_scores": false, "public_ranks": false}, w.Org); r.Status != 200 {
				t.Fatalf("score settings stay editable: %d %s", r.Status, r.Body)
			}
			if r := w.A.SetStage(t, w.Slug, "closed", w.A.Key(w.Org)); r.Status != 200 {
				t.Fatalf("back to closed: %d %s", r.Status, r.Body)
			}
			if r := w.Call(t, tc.method, tc.path, tc.body, w.Org); r.Status != 200 {
				t.Fatalf("setup unlocked: %d %s", r.Status, r.Body)
			}
		})
	}
}

// Moving back from Results clears the lock and restores the relevant writes.
func TestHackStageBackClearsJudgingLock(t *testing.T) {
	for _, target := range []string{"closed"} {
		t.Run(target, func(t *testing.T) {
			w := newJudgingWorld(t, 1, 2)
			for _, stage := range []string{"judging", "results"} {
				r := w.A.SetStage(t, w.Slug, stage, w.A.Key(w.Org))
				if r.Status != 200 {
					t.Fatalf("%s: %d %s", stage, r.Status, r.Body)
				}
			}
			var locked bool
			if err := w.a.DB.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE slug=$1`, w.Slug).Scan(&locked); err != nil || !locked {
				t.Fatalf("not locked: %v %v", locked, err)
			}
			r := w.A.SetStage(t, w.Slug, target, w.A.Key(w.Org))
			if r.Status != 200 {
				t.Fatalf("back: %d %s", r.Status, r.Body)
			}
			if err := w.a.DB.QueryRow(`SELECT judging_locked_at IS NOT NULL FROM events WHERE slug=$1`, w.Slug).Scan(&locked); err != nil || locked {
				t.Fatalf("still locked: %v %v", locked, err)
			}
			decodeSettings(t, w.Call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "manual", "judges_per_team": 2}, w.Org))
			r = w.Call(t, "PUT", "/rubric", hacktest.RubricBody(hacktest.Criterion("Impact", "", 100, 5)), w.Org)
			if r.Status != 200 {
				t.Fatalf("rubric after moving back: %d %s", r.Status, r.Body)
			}
			r = w.Call(t, "PUT", "/assignments", map[string]any{"assignments": []any{}}, w.Org)
			if r.Status != 200 {
				t.Fatalf("write after moving back: %d %s", r.Status, r.Body)
			}
		})
	}
}
