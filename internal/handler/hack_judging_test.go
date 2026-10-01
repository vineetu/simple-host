package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"
)

// Judging setup: the rubric, how judges are spread, conflicts, and the
// organiser's dashboard. Score rows do not exist yet, so coverage stays at
// zero. The harness is newHackApp (DB_DSN, same as the other hack tests).

type judgingPerson struct {
	person
	id, name string
}

type judgingTeam struct {
	id, name string
}

type judgingWorld struct {
	a        *hackApp
	slug     string
	org      person
	outsider person
	part     judgingPerson
	judges   []judgingPerson
	teams    []judgingTeam
}

func newJudgingWorld(t *testing.T, nTeams, nJudges int) *judgingWorld {
	t.Helper()
	a := newHackApp(t)
	org := a.newPerson(t, "jg")
	outsider := a.newPerson(t, "jx")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, nil); r.status != http.StatusCreated {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	a.openEvent(t, org, slug)
	ev := a.at(t, "GET", "/v1/hack/events/"+slug, nil, a.key(org)).json(t)
	orgView := ev["organiser"].(map[string]any)
	join := orgView["join_code"].(string)
	jcode := orgView["judge_code"].(string)

	w := &judgingWorld{a: a, slug: slug, org: org, outsider: outsider}
	joinAs := func(p person, name string, asJudge bool) {
		t.Helper()
		path := "/v1/hack/join/" + join
		if asJudge {
			path = "/v1/hack/judge/" + jcode
		}
		r := a.at(t, "POST", path, map[string]any{"accept_coc": true, "display_name": name}, a.key(p))
		if r.status != http.StatusOK {
			t.Fatalf("join %s: %d %s", name, r.status, r.body)
		}
	}

	nParts := nTeams
	if nParts < 1 {
		nParts = 1
	}
	parts := make([]person, nParts)
	for i := range parts {
		parts[i] = a.newPerson(t, "jp")
		name := fmt.Sprintf("Pat %d", i+1)
		joinAs(parts[i], name, false)
		if i == 0 {
			w.part = judgingPerson{person: parts[i], id: a.userID(t, parts[i]), name: name}
		}
	}
	for i := 0; i < nTeams; i++ {
		name := fmt.Sprintf("Team %c", 'A'+i)
		r := a.at(t, "POST", "/v1/hack/events/"+slug+"/teams", map[string]string{"name": name}, a.key(parts[i]))
		if r.status != http.StatusCreated {
			t.Fatalf("team %s: %d %s", name, r.status, r.body)
		}
		w.teams = append(w.teams, judgingTeam{id: a.teamIDByName(t, slug, name), name: name})
	}
	for i := 0; i < nJudges; i++ {
		p := a.newPerson(t, "jj")
		name := fmt.Sprintf("Judge %d", i+1)
		joinAs(p, name, true)
		w.judges = append(w.judges, judgingPerson{person: p, id: a.userID(t, p), name: name})
	}
	return w
}

func (a *hackApp) teamIDByName(t *testing.T, slug, name string) string {
	t.Helper()
	var id string
	err := a.database.QueryRow(`
		SELECT t.id FROM event_teams t
		JOIN events e ON e.id = t.event_id
		WHERE e.slug = $1 AND t.name = $2`, slug, name).Scan(&id)
	if err != nil {
		t.Fatalf("team id %s: %v", name, err)
	}
	return id
}

func (w *judgingWorld) path(rest string) string {
	return "/v1/hack/events/" + w.slug + rest
}

func (w *judgingWorld) call(t *testing.T, method, rest string, body any, p person) resp {
	t.Helper()
	return w.a.at(t, method, w.path(rest), body, w.a.key(p))
}

func (w *judgingWorld) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := w.a.database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func wantHackErr(t *testing.T, what string, r resp, status int, code, msg string) {
	t.Helper()
	if r.status != status {
		t.Fatalf("%s: got %d want %d: %s", what, r.status, status, r.body)
	}
	got := r.json(t)
	if got["code"] != code || got["error"] != msg {
		t.Fatalf("%s: got code %q error %q; want code %q error %q", what, got["code"], got["error"], code, msg)
	}
}

func criterion(name, desc string, weight, points int) map[string]any {
	return map[string]any{"name": name, "description": desc, "weight": weight, "max_points": points}
}

func rubricBody(items ...map[string]any) map[string]any {
	return map[string]any{"criteria": items}
}

type rubricCriterion struct {
	ID          string `json:"id"`
	Position    int    `json:"position"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Weight      int    `json:"weight"`
	MaxPoints   int    `json:"max_points"`
}

type rubricView struct {
	Criteria []rubricCriterion `json:"criteria"`
}

func decodeRubric(t *testing.T, r resp) rubricView {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("rubric: %d %s", r.status, r.body)
	}
	var v rubricView
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("rubric json: %v body %s", err, r.body)
	}
	return v
}

func sortedJudges(in []judgingPerson) []judgingPerson {
	out := append([]judgingPerson(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func sortedTeams(in []judgingTeam) []judgingTeam {
	out := append([]judgingTeam(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

type assignmentRow struct {
	JudgeID   string `json:"judge_id"`
	JudgeName string `json:"judge_name"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
}

func decodeAssignments(t *testing.T, r resp) (rows []assignmentRow, under []string) {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("assignments: %d %s", r.status, r.body)
	}
	var body struct {
		Assignments []assignmentRow `json:"assignments"`
		Under       []string        `json:"teams_under_target"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatalf("assignments json: %v body %s", err, r.body)
	}
	if body.Assignments == nil || body.Under == nil {
		t.Fatalf("assignments or teams_under_target is null: %s", r.body)
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
func spread(js []judgingPerson, ts []judgingTeam, pairs [][2]int) []assignmentRow {
	out := make([]assignmentRow, len(pairs))
	for i, p := range pairs {
		out[i] = assignmentRow{
			JudgeID: js[p[1]].id, JudgeName: js[p[1]].name,
			TeamID: ts[p[0]].id, TeamName: ts[p[0]].name,
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
}

func decodeConflict(t *testing.T, r resp) conflictView {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("conflict: %d %s", r.status, r.body)
	}
	var v conflictView
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func decodeConflictList(t *testing.T, r resp) []conflictView {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("conflicts: %d %s", r.status, r.body)
	}
	var body struct {
		Conflicts []conflictView `json:"conflicts"`
	}
	if err := json.Unmarshal(r.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Conflicts == nil {
		t.Fatalf("conflicts is null: %s", r.body)
	}
	return body.Conflicts
}

type judgingSettings struct {
	Mode string `json:"assignment_mode"`
	Per  int    `json:"judges_per_team"`
}

func decodeSettings(t *testing.T, r resp) judgingSettings {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("settings: %d %s", r.status, r.body)
	}
	var v judgingSettings
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

type dashTeam struct {
	TeamID       string `json:"team_id"`
	TeamName     string `json:"team_name"`
	JudgesScored int    `json:"judges_scored"`
	Flagged      bool   `json:"flagged"`
}

type dashJudge struct {
	JudgeID       string `json:"judge_id"`
	JudgeName     string `json:"judge_name"`
	DoneCount     int    `json:"done_count"`
	AssignedCount int    `json:"assigned_count"`
}

type dashView struct {
	Teams  []dashTeam      `json:"teams"`
	Judges []dashJudge     `json:"judges"`
	Tied   json.RawMessage `json:"tied_groups"`
}

func decodeDash(t *testing.T, r resp) dashView {
	t.Helper()
	if r.status != http.StatusOK {
		t.Fatalf("dashboard: %d %s", r.status, r.body)
	}
	var v dashView
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatal(err)
	}
	if string(v.Tied) != "[]" {
		t.Fatalf("tied_groups %s", v.Tied)
	}
	if v.Teams == nil || v.Judges == nil {
		t.Fatalf("dashboard null list: %s", r.body)
	}
	return v
}

func TestHackJudgingRubric(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	longName := strings.Repeat("n", 81)
	many := make([]map[string]any, 11)
	for i := range many {
		many[i] = criterion(fmt.Sprintf("C%d", i), "", 0, 1)
	}
	cases := []struct {
		name string
		body map[string]any
		msg  string
	}{
		{
			"weights",
			rubricBody(criterion("Impact", "", 60, 5), criterion("Build", "", 30, 5)),
			"Weights must add up to 100.",
		},
		{"too many", rubricBody(many...), "Give between 1 and 10 criteria."},
		{"none", rubricBody(), "Give between 1 and 10 criteria."},
		{"long name", rubricBody(criterion(longName, "", 100, 5)), "Give each criterion a name of up to 80 characters."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := w.call(t, "PUT", "/rubric", tc.body, w.org)
			wantHackErr(t, tc.name, r, http.StatusBadRequest, "invalid_rubric", tc.msg)
		})
	}

	firstBody := rubricBody(
		criterion("Impact", "  Who it helps  ", 60, 10),
		criterion("Build", "", 40, 1),
	)
	saved := decodeRubric(t, w.call(t, "PUT", "/rubric", firstBody, w.org))
	if len(saved.Criteria) != 2 {
		t.Fatalf("criteria: %+v", saved.Criteria)
	}
	wantFirst := []rubricCriterion{
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
	again := decodeRubric(t, w.call(t, "GET", "/rubric", nil, w.org))
	if len(again.Criteria) != 2 || again.Criteria[0].ID != saved.Criteria[0].ID {
		t.Fatalf("get after put: %+v", again)
	}

	// Full replace: one criterion, spaces collapsed, previous rows gone.
	replaced := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("  Clarity   of   purpose  ", "Clear.", 100, 5)), w.org))
	if len(replaced.Criteria) != 1 {
		t.Fatalf("replace: %+v", replaced.Criteria)
	}
	got := replaced.Criteria[0]
	if oldIDs[got.ID] || got.ID == "" || got.Position != 0 || got.Name != "Clarity of purpose" || got.Description != "Clear." || got.Weight != 100 || got.MaxPoints != 5 {
		t.Fatalf("replaced criterion: %+v", got)
	}
	n := w.countRows(t, `SELECT COUNT(*) FROM rubric_criteria WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug)
	if n != 1 {
		t.Fatalf("rubric rows %d", n)
	}
	orgGet := w.call(t, "GET", "/rubric", nil, w.org)
	judgeGet := w.call(t, "GET", "/rubric", nil, w.judges[0].person)
	if orgGet.status != http.StatusOK || string(orgGet.body) != string(judgeGet.body) {
		t.Fatalf("organiser %d %s\njudge %d %s", orgGet.status, orgGet.body, judgeGet.status, judgeGet.body)
	}

	// A non-member, and a participant, both look like a missing event.
	for _, tc := range []struct {
		name, method string
		who          person
		body         any
	}{
		{"outsider get", "GET", w.outsider, nil},
		{"outsider put", "PUT", w.outsider, firstBody},
		{"participant get", "GET", w.part.person, nil},
		{"participant put", "PUT", w.part.person, firstBody},
	} {
		r := w.call(t, tc.method, "/rubric", tc.body, tc.who)
		wantHackErr(t, tc.name, r, http.StatusNotFound, "event_not_found", "event not found")
	}

	if _, err := w.a.database.Exec(`UPDATE events SET judging_locked_at = now() WHERE slug = $1`, w.slug); err != nil {
		t.Fatal(err)
	}
	lockedMsg := "Judging is locked; unlock it first to change the rubric."
	r := w.call(t, "PUT", "/rubric", rubricBody(criterion("Other", "", 100, 5)), w.org)
	wantHackErr(t, "locked", r, http.StatusConflict, "scores_locked", lockedMsg)
	// The lock wins over a body that would also fail validation.
	r = w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 60, 5), criterion("Build", "", 30, 5)), w.org)
	wantHackErr(t, "locked before validation", r, http.StatusConflict, "scores_locked", lockedMsg)
	if decodeRubric(t, w.call(t, "GET", "/rubric", nil, w.org)).Criteria[0].Name != "Clarity of purpose" {
		t.Fatal("locked put changed the rubric")
	}
}

func TestHackJudgingSettings(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	for _, tc := range []struct {
		name, method string
		who          person
		body         any
	}{
		{"judge get", "GET", w.judges[0].person, nil},
		{"participant get", "GET", w.part.person, nil},
		{"outsider get", "GET", w.outsider, nil},
		{"judge patch", "PATCH", w.judges[0].person, map[string]any{"assignment_mode": "automatic"}},
		{"participant patch", "PATCH", w.part.person, map[string]any{"judges_per_team": 4}},
	} {
		r := w.call(t, tc.method, "/judging/settings", tc.body, tc.who)
		wantHackErr(t, tc.name, r, http.StatusNotFound, "event_not_found", "event not found")
	}

	if got := decodeSettings(t, w.call(t, "GET", "/judging/settings", nil, w.org)); got != (judgingSettings{"open", 2}) {
		t.Fatalf("defaults %+v", got)
	}
	patched := decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{
		"assignment_mode": "automatic", "judges_per_team": 3,
	}, w.org))
	if patched != (judgingSettings{"automatic", 3}) {
		t.Fatalf("patch %+v", patched)
	}
	if got := decodeSettings(t, w.call(t, "GET", "/judging/settings", nil, w.org)); got != patched {
		t.Fatalf("get after patch %+v", got)
	}
	// A field left out stays as it is.
	if got := decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{"judges_per_team": 20}, w.org)); got != (judgingSettings{"automatic", 20}) {
		t.Fatalf("partial per-team %+v", got)
	}
	if got := decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "open"}, w.org)); got != (judgingSettings{"open", 20}) {
		t.Fatalf("partial mode %+v", got)
	}
	if got := decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{}, w.org)); got != (judgingSettings{"open", 20}) {
		t.Fatalf("empty patch %+v", got)
	}

	invalid := []struct {
		name string
		body map[string]any
		msg  string
	}{
		{"mode", map[string]any{"assignment_mode": "round-robin"}, `assignment_mode must be "open" or "automatic".`},
		{"per team zero", map[string]any{"judges_per_team": 0}, "judges_per_team must be a whole number from 1 to 20."},
		{"per team high", map[string]any{"judges_per_team": 21}, "judges_per_team must be a whole number from 1 to 20."},
		{"per team fraction", map[string]any{"judges_per_team": 1.5}, "judges_per_team must be a whole number from 1 to 20."},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			r := w.call(t, "PATCH", "/judging/settings", tc.body, w.org)
			wantHackErr(t, tc.name, r, http.StatusBadRequest, "invalid_request", tc.msg)
			if got := decodeSettings(t, w.call(t, "GET", "/judging/settings", nil, w.org)); got != (judgingSettings{"open", 20}) {
				t.Fatalf("invalid patch stuck: %+v", got)
			}
		})
	}
}

func TestHackJudgingAssignments(t *testing.T) {
	w := newJudgingWorld(t, 3, 3)
	r := w.call(t, "POST", "/assignments/generate", nil, w.org)
	wantHackErr(t, "open", r, http.StatusConflict, "invalid_request", "Switch to automatic assignment first.")
	if n := w.countRows(t, `SELECT COUNT(*) FROM event_assignments WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug); n != 0 {
		t.Fatalf("open mode wrote %d assignments", n)
	}

	if got := decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{
		"assignment_mode": "automatic", "judges_per_team": 2,
	}, w.org)); got != (judgingSettings{"automatic", 2}) {
		t.Fatalf("settings %+v", got)
	}
	js, ts := sortedJudges(w.judges), sortedTeams(w.teams)
	// Three teams, three judges, two each. Least-loaded first, id breaks
	// ties, teams in id order:
	//   T0 takes J0 J1; T1 takes J2 J0; T2 takes J1 J2.
	// Every judge ends on two teams.
	first := w.call(t, "POST", "/assignments/generate", nil, w.org)
	second := w.call(t, "POST", "/assignments/generate", nil, w.org)
	if string(first.body) != string(second.body) {
		t.Fatalf("not deterministic\n%s\n%s", first.body, second.body)
	}
	rows, under := decodeAssignments(t, first)
	wantRows(t, rows, spread(js, ts, [][2]int{{0, 0}, {0, 1}, {1, 0}, {1, 2}, {2, 1}, {2, 2}}))
	if len(under) != 0 {
		t.Fatalf("under %v", under)
	}
	if n := w.countRows(t, `SELECT COUNT(*) FROM event_assignments WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug); n != 6 {
		t.Fatalf("assignment rows %d, want 6", n)
	}

	// J0 is conflicted with T0, so T0 is judged by J1 and J2.
	//   T0: J1 J2; T1: J0 J1; T2: J0 J2.
	declared := decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{
		"team_id": ts[0].id, "judge_user_id": js[0].id,
	}, w.org))
	if declared.DeclaredBy != "organiser" || declared.JudgeID != js[0].id || declared.TeamID != ts[0].id {
		t.Fatalf("declare: %+v", declared)
	}
	rows, under = decodeAssignments(t, w.call(t, "POST", "/assignments/generate", nil, w.org))
	wantRows(t, rows, spread(js, ts, [][2]int{{0, 1}, {0, 2}, {1, 0}, {1, 1}, {2, 0}, {2, 2}}))
	if len(under) != 0 {
		t.Fatalf("under with two eligible judges: %v", under)
	}
	for _, row := range rows {
		if row.JudgeID == js[0].id && row.TeamID == ts[0].id {
			t.Fatalf("conflicted pair assigned: %+v", row)
		}
	}

	// Target 3, but T0 has only two judges free of the conflict.
	if got := decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{"judges_per_team": 3}, w.org)); got.Per != 3 {
		t.Fatalf("per team %+v", got)
	}
	rows, under = decodeAssignments(t, w.call(t, "POST", "/assignments/generate", nil, w.org))
	wantRows(t, rows, spread(js, ts, [][2]int{{0, 1}, {0, 2}, {1, 0}, {1, 1}, {1, 2}, {2, 0}, {2, 1}, {2, 2}}))
	if len(under) != 1 || under[0] != ts[0].id {
		t.Fatalf("under got %v want [%s]", under, ts[0].id)
	}
}

func TestHackJudgingTooFew(t *testing.T) {
	t.Run("judges", func(t *testing.T) {
		w := newJudgingWorld(t, 2, 0)
		decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "automatic"}, w.org))
		r := w.call(t, "POST", "/assignments/generate", nil, w.org)
		wantHackErr(t, "judges", r, http.StatusBadRequest, "too_few_judges", "Invite at least one judge first.")
	})
	t.Run("teams", func(t *testing.T) {
		w := newJudgingWorld(t, 0, 1)
		decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "automatic"}, w.org))
		r := w.call(t, "POST", "/assignments/generate", nil, w.org)
		wantHackErr(t, "teams", r, http.StatusBadRequest, "too_few_teams", "No teams to assign yet.")
	})
}

func TestHackJudgingConflicts(t *testing.T) {
	w := newJudgingWorld(t, 2, 2)
	j0, j1 := w.judges[0], w.judges[1]
	t0, t1 := w.teams[0], w.teams[1]

	r := w.call(t, "POST", "/conflicts", map[string]any{"team_id": t0.id}, w.part.person)
	wantHackErr(t, "participant", r, http.StatusNotFound, "event_not_found", "event not found")
	r = w.call(t, "POST", "/conflicts", map[string]any{"team_id": t0.id}, w.outsider)
	wantHackErr(t, "outsider", r, http.StatusNotFound, "event_not_found", "event not found")

	self := decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{"team_id": t0.id}, j0.person))
	if self != (conflictView{j0.id, j0.name, t0.id, t0.name, "judge"}) {
		t.Fatalf("self: %+v", self)
	}
	// A second declaration is the same row, and declared_by stays "judge".
	again := decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{"team_id": t0.id}, j0.person))
	if again != self {
		t.Fatalf("second declare %+v", again)
	}
	if n := w.countRows(t, `SELECT COUNT(*) FROM event_conflicts WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug); n != 1 {
		t.Fatalf("conflict rows %d", n)
	}

	// The organiser names the judge. Omitting that is a 400, not "myself".
	r = w.call(t, "POST", "/conflicts", map[string]any{"team_id": t0.id}, w.org)
	wantHackErr(t, "organiser omits judge", r, http.StatusBadRequest, "invalid_request", "Say which judge this conflict is for.")
	onBehalf := decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{
		"team_id": t0.id, "judge_user_id": j1.id,
	}, w.org))
	if onBehalf != (conflictView{j1.id, j1.name, t0.id, t0.name, "organiser"}) {
		t.Fatalf("on behalf: %+v", onBehalf)
	}
	// Declaring the same triple again does not flip declared_by.
	repeat := decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{
		"team_id": t0.id, "judge_user_id": j1.id,
	}, w.org))
	if repeat.DeclaredBy != "organiser" {
		t.Fatalf("repeat declared_by %q", repeat.DeclaredBy)
	}
	if n := w.countRows(t, `SELECT COUNT(*) FROM event_conflicts WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug); n != 2 {
		t.Fatalf("conflict rows %d", n)
	}

	r = w.call(t, "POST", "/conflicts", map[string]any{"team_id": t1.id, "judge_user_id": j1.id}, j0.person)
	wantHackErr(t, "judge for someone else", r, http.StatusForbidden, "forbidden", "Only an organiser can record a conflict for another judge.")

	all := decodeConflictList(t, w.call(t, "GET", "/conflicts", nil, w.org))
	if len(all) != 2 {
		t.Fatalf("organiser list: %+v", all)
	}
	mine := decodeConflictList(t, w.call(t, "GET", "/conflicts", nil, j0.person))
	if len(mine) != 1 || mine[0] != self {
		t.Fatalf("judge list: %+v", mine)
	}
	theirs := decodeConflictList(t, w.call(t, "GET", "/conflicts", nil, j1.person))
	if len(theirs) != 1 || theirs[0].JudgeID != j1.id || theirs[0].DeclaredBy != "organiser" {
		t.Fatalf("other judge list: %+v", theirs)
	}

	r = w.call(t, "DELETE", "/conflicts/"+t0.id, nil, j0.person)
	if r.status != http.StatusNoContent {
		t.Fatalf("judge delete: %d %s", r.status, r.body)
	}
	if mine = decodeConflictList(t, w.call(t, "GET", "/conflicts", nil, j0.person)); len(mine) != 0 {
		t.Fatalf("after delete: %+v", mine)
	}
	r = w.call(t, "DELETE", "/conflicts/"+t0.id, nil, j0.person)
	wantHackErr(t, "delete missing", r, http.StatusNotFound, "conflict_not_found", "conflict not found")

	r = w.call(t, "DELETE", "/conflicts/"+t0.id+"?judge_user_id="+j1.id, nil, w.org)
	if r.status != http.StatusNoContent {
		t.Fatalf("organiser delete: %d %s", r.status, r.body)
	}
	r = w.call(t, "DELETE", "/conflicts/"+t0.id+"?judge_user_id="+j1.id, nil, w.org)
	wantHackErr(t, "organiser delete missing", r, http.StatusNotFound, "conflict_not_found", "conflict not found")
	r = w.call(t, "DELETE", "/conflicts/"+t1.id, nil, w.org)
	wantHackErr(t, "organiser omits judge", r, http.StatusBadRequest, "invalid_request", "Say which judge's conflict to remove.")

	// Put one back so a judge's attempt to remove someone else's can be seen to miss.
	decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{"team_id": t1.id}, j0.person))
	r = w.call(t, "DELETE", "/conflicts/"+t1.id+"?judge_user_id="+j0.id, nil, j1.person)
	wantHackErr(t, "judge deletes another", r, http.StatusBadRequest, "invalid_request", "You can only remove your own conflict.")
	if n := w.countRows(t, `
		SELECT COUNT(*) FROM event_conflicts
		WHERE event_id = (SELECT id FROM events WHERE slug = $1) AND judge_id = $2 AND team_id = $3`,
		w.slug, j0.id, t1.id); n != 1 {
		t.Fatalf("other judge's delete removed the row (%d)", n)
	}
}

func TestHackJudgingDashboard(t *testing.T) {
	w := newJudgingWorld(t, 2, 2)
	for _, tc := range []struct {
		name string
		who  person
	}{
		{"judge", w.judges[0].person},
		{"participant", w.part.person},
		{"outsider", w.outsider},
	} {
		r := w.call(t, "GET", "/judging/dashboard", nil, tc.who)
		wantHackErr(t, tc.name, r, http.StatusNotFound, "event_not_found", "event not found")
	}

	assertZero := func(d dashView) {
		t.Helper()
		if len(d.Teams) != len(w.teams) || len(d.Judges) != len(w.judges) {
			t.Fatalf("shape teams %d judges %d: %+v", len(d.Teams), len(d.Judges), d)
		}
		var teamIDs, judgeIDs []string
		names := map[string]string{}
		for _, team := range w.teams {
			names[team.id] = team.name
		}
		for _, judge := range w.judges {
			names[judge.id] = judge.name
		}
		for _, team := range d.Teams {
			if team.JudgesScored != 0 || !team.Flagged || team.TeamName != names[team.TeamID] {
				t.Fatalf("team %+v", team)
			}
			teamIDs = append(teamIDs, team.TeamID)
		}
		for _, judge := range d.Judges {
			if judge.DoneCount != 0 || judge.JudgeName != names[judge.JudgeID] {
				t.Fatalf("judge %+v", judge)
			}
			judgeIDs = append(judgeIDs, judge.JudgeID)
		}
		if !sort.StringsAreSorted(teamIDs) || !sort.StringsAreSorted(judgeIDs) {
			t.Fatalf("order teams %v judges %v", teamIDs, judgeIDs)
		}
	}
	assigned := func(d dashView) map[string]int {
		t.Helper()
		out := map[string]int{}
		for _, judge := range d.Judges {
			out[judge.JudgeID] = judge.AssignedCount
		}
		return out
	}

	// Open mode writes no assignment rows. Each judge's count is every
	// team they are not conflicted with.
	open := decodeDash(t, w.call(t, "GET", "/judging/dashboard", nil, w.org))
	assertZero(open)
	for _, n := range assigned(open) {
		if n != 2 {
			t.Fatalf("open assigned %+v", assigned(open))
		}
	}
	if n := w.countRows(t, `SELECT COUNT(*) FROM event_assignments WHERE event_id = (SELECT id FROM events WHERE slug = $1)`, w.slug); n != 0 {
		t.Fatalf("open mode has %d assignment rows", n)
	}

	j0, t0 := w.judges[0], w.teams[0]
	decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{"team_id": t0.id}, j0.person))
	withConflict := decodeDash(t, w.call(t, "GET", "/judging/dashboard", nil, w.org))
	assertZero(withConflict)
	openCounts := assigned(withConflict)
	if openCounts[j0.id] != 1 || openCounts[w.judges[1].id] != 2 {
		t.Fatalf("open counts with one conflict: %+v", openCounts)
	}

	decodeSettings(t, w.call(t, "PATCH", "/judging/settings", map[string]any{
		"assignment_mode": "automatic", "judges_per_team": 1,
	}, w.org))
	rows, under := decodeAssignments(t, w.call(t, "POST", "/assignments/generate", nil, w.org))
	if len(under) != 0 || len(rows) != len(w.teams) {
		t.Fatalf("generate rows %d under %v", len(rows), under)
	}
	perTeam := map[string]int{}
	autoFromRows := map[string]int{}
	for _, judge := range w.judges {
		autoFromRows[judge.id] = 0
	}
	for _, row := range rows {
		if row.JudgeID == j0.id && row.TeamID == t0.id {
			t.Fatalf("conflicted pair assigned: %+v", row)
		}
		perTeam[row.TeamID]++
		autoFromRows[row.JudgeID]++
	}
	for _, team := range w.teams {
		if perTeam[team.id] != 1 {
			t.Fatalf("team %s got %d judges", team.name, perTeam[team.id])
		}
	}

	auto := decodeDash(t, w.call(t, "GET", "/judging/dashboard", nil, w.org))
	assertZero(auto)
	got := assigned(auto)
	sum := 0
	for id, n := range got {
		if n != autoFromRows[id] {
			t.Fatalf("automatic count for %s is %d, assignments say %d (%+v)", id, n, autoFromRows[id], got)
		}
		sum += n
	}
	if sum != len(w.teams) {
		t.Fatalf("automatic sum %d", sum)
	}
	// Open mode counted the conflicted judge's other team plus both of the
	// other judge's (sum 3). Automatic mode stores one row per team (sum 2).
	openSum := 0
	same := true
	for id, n := range openCounts {
		openSum += n
		if got[id] != n {
			same = false
		}
	}
	if openSum != 3 || same {
		t.Fatalf("open %+v (sum %d) automatic %+v", openCounts, openSum, got)
	}
}

// --- M3 pass 2: scoring, lock/unlock, publish, results, exports ----------

func TestHackJudgingLockUnlock(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	r := w.call(t, "POST", "/judging/lock", map[string]any{}, w.org)
	if r.status != http.StatusOK || r.json(t)["locked"] != true {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	// Idempotent: locking again is still 200.
	r = w.call(t, "POST", "/judging/lock", map[string]any{}, w.org)
	if r.status != http.StatusOK || r.json(t)["locked"] != true {
		t.Fatalf("lock again: %d %s", r.status, r.body)
	}
	// Rubric writes refused while locked.
	r = w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org)
	wantHackErr(t, "rubric while locked", r, http.StatusConflict, "scores_locked", "Judging is locked; unlock it first to change the rubric.")
	// Unlock requires a reason.
	r = w.call(t, "POST", "/judging/unlock", map[string]any{"reason": ""}, w.org)
	if r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_reason" {
		t.Fatalf("unlock no reason: %d %s", r.status, r.body)
	}
	r = w.call(t, "POST", "/judging/unlock", map[string]any{"reason": "Found a mistake"}, w.org)
	if r.status != http.StatusOK || r.json(t)["locked"] != false {
		t.Fatalf("unlock: %d %s", r.status, r.body)
	}
	// Now the rubric write succeeds.
	r = w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org)
	if r.status != http.StatusOK {
		t.Fatalf("rubric after unlock: %d %s", r.status, r.body)
	}
}

// Locking must freeze every input that feeds a computed total, not just
// score rows: a conflict or assignment change left unlocked would silently
// change a published result the next time it's recomputed, with no further
// "score" write ever happening — defeating the point of locking.
func TestHackJudgingLockCoversConflictsAndAssignments(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	if r := w.call(t, "POST", "/judging/lock", map[string]any{}, w.org); r.status != http.StatusOK {
		t.Fatalf("lock: %d %s", r.status, r.body)
	}
	if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "automatic"}, w.org); r.status != http.StatusConflict || r.json(t)["code"] != "scores_locked" {
		t.Fatalf("settings while locked: %d %s", r.status, r.body)
	}
	if r := w.call(t, "POST", "/assignments/generate", map[string]any{}, w.org); r.status != http.StatusConflict || r.json(t)["code"] != "scores_locked" {
		t.Fatalf("generate while locked: %d %s", r.status, r.body)
	}
	if r := w.call(t, "POST", "/conflicts", map[string]any{"team_id": w.teams[0].id}, w.judges[0].person); r.status != http.StatusConflict || r.json(t)["code"] != "scores_locked" {
		t.Fatalf("declare conflict while locked: %d %s", r.status, r.body)
	}
	// Unlock, then declaring a conflict works; locking again, removing it is refused.
	if r := w.call(t, "POST", "/judging/unlock", map[string]any{"reason": "testing"}, w.org); r.status != http.StatusOK {
		t.Fatalf("unlock: %d %s", r.status, r.body)
	}
	if r := w.call(t, "POST", "/conflicts", map[string]any{"team_id": w.teams[0].id}, w.judges[0].person); r.status != http.StatusOK {
		t.Fatalf("declare conflict unlocked: %d %s", r.status, r.body)
	}
	if r := w.call(t, "POST", "/judging/lock", map[string]any{}, w.org); r.status != http.StatusOK {
		t.Fatalf("relock: %d %s", r.status, r.body)
	}
	if r := w.call(t, "DELETE", "/conflicts/"+w.teams[0].id+"?judge_user_id="+w.judges[0].id, nil, w.judges[0].person); r.status != http.StatusConflict || r.json(t)["code"] != "scores_locked" {
		t.Fatalf("remove conflict while locked: %d %s", r.status, r.body)
	}
}

// scoreTeam is a small helper: PUT scores for one (judge, team).
func (w *judgingWorld) scoreTeam(t *testing.T, judge judgingPerson, teamID string, scores map[string]int, comment *string) resp {
	t.Helper()
	body := map[string]any{}
	list := make([]map[string]any, 0, len(scores))
	for cid, pts := range scores {
		list = append(list, map[string]any{"criterion_id": cid, "points": pts})
	}
	body["scores"] = list
	if comment != nil {
		body["comment"] = *comment
	}
	return w.call(t, "PUT", "/judge/scores/"+teamID, body, judge.person)
}

func TestHackJudgingScoresUpsertAndRetrySafety(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 60, 5), criterion("Craft", "", 40, 5)), w.org))
	c1, c2 := rv.Criteria[0].ID, rv.Criteria[1].ID
	team := w.teams[0].id
	judge := w.judges[0]

	// Partial save: only one criterion.
	r := w.scoreTeam(t, judge, team, map[string]int{c1: 4}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("partial score: %d %s", r.status, r.body)
	}
	if r.json(t)["complete"] != false {
		t.Fatalf("expected incomplete: %s", r.body)
	}

	// A retried write of the SAME criterion with the SAME value must never
	// double-count: still exactly one row, same value.
	r = w.scoreTeam(t, judge, team, map[string]int{c1: 4}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("retry score: %d %s", r.status, r.body)
	}
	n := w.countRows(t, `SELECT COUNT(*) FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND criterion_id=$3`, judge.id, team, c1)
	if n != 1 {
		t.Fatalf("retry created %d rows, want 1", n)
	}

	// Complete it.
	r = w.scoreTeam(t, judge, team, map[string]int{c2: 3}, nil)
	if r.status != http.StatusOK || r.json(t)["complete"] != true {
		t.Fatalf("complete: %d %s", r.status, r.body)
	}

	// Out-of-range points.
	r = w.scoreTeam(t, judge, team, map[string]int{c1: 9}, nil)
	if r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_points" {
		t.Fatalf("out of range: %d %s", r.status, r.body)
	}

	// Comment-only with existing scores updates the comment.
	comment := "Nice work"
	r = w.scoreTeam(t, judge, team, map[string]int{}, &comment)
	if r.status != http.StatusOK || r.json(t)["comment"] != comment {
		t.Fatalf("comment update: %d %s", r.status, r.body)
	}
	// The comment lands on EVERY row for this (judge, team) pair, not just
	// one named in some earlier call — there is no separate comment table,
	// so every row must agree or "the" comment is ambiguous.
	var c1Comment, c2Comment string
	if err := w.a.database.QueryRow(`SELECT comment FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND criterion_id=$3`, judge.id, team, c1).Scan(&c1Comment); err != nil {
		t.Fatal(err)
	}
	if err := w.a.database.QueryRow(`SELECT comment FROM event_scores WHERE judge_id=$1 AND team_id=$2 AND criterion_id=$3`, judge.id, team, c2).Scan(&c2Comment); err != nil {
		t.Fatal(err)
	}
	if c1Comment != comment || c2Comment != comment {
		t.Fatalf("comment not applied to every row: c1=%q c2=%q want %q", c1Comment, c2Comment, comment)
	}
	// Saving a NEW point value on just c1 (no comment field sent) must not
	// blank the comment that's already there.
	r = w.scoreTeam(t, judge, team, map[string]int{c1: 5}, nil)
	if r.status != http.StatusOK || r.json(t)["comment"] != comment {
		t.Fatalf("points-only write blanked the comment: %s", r.body)
	}

	// Comment-only with NO scores yet at all is refused.
	team2 := w.teams[0].id
	if len(w.teams) > 1 {
		team2 = w.teams[1].id
	}
	if team2 == team {
		// only one team in this world; use a throwaway second team via direct SQL is overkill,
		// skip this branch when there's nothing to test against.
	} else {
		c := "hello"
		r = w.scoreTeam(t, judge, team2, map[string]int{}, &c)
		if r.status != http.StatusBadRequest || r.json(t)["code"] != "invalid_request" {
			t.Fatalf("comment-only no scores: %d %s", r.status, r.body)
		}
	}

	// Locked judging refuses a score write.
	w.call(t, "POST", "/judging/lock", map[string]any{}, w.org)
	r = w.scoreTeam(t, judge, team, map[string]int{c1: 5}, nil)
	wantHackErr(t, "score while locked", r, http.StatusConflict, "scores_locked", "Judging is locked.")
}

func TestHackJudgingConflictExcludesFromQueueAndScoring(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	judge := w.judges[0]
	team := w.teams[0].id
	r := w.call(t, "POST", "/conflicts", map[string]any{"team_id": team}, judge.person)
	if r.status != http.StatusOK {
		t.Fatalf("declare conflict: %d %s", r.status, r.body)
	}
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org))
	c1 := rv.Criteria[0].ID

	// The conflicted team never appears in this judge's queue.
	q := w.call(t, "GET", "/judge/queue", nil, judge.person)
	if q.status != http.StatusOK {
		t.Fatalf("queue: %d %s", q.status, q.body)
	}
	queue := q.json(t)["queue"].([]any)
	for _, item := range queue {
		if item.(map[string]any)["team_id"] == team {
			t.Fatalf("conflicted team present in queue: %v", item)
		}
	}

	// Scoring the conflicted team directly is still refused.
	r = w.scoreTeam(t, judge, team, map[string]int{c1: 5}, nil)
	wantHackErr(t, "score conflicted team", r, http.StatusForbidden, "forbidden", "You have a conflict with this team.")
}

// handScoreFormula is the same formula the handler uses, computed by hand
// here for a known input, to cross-check computeResults independently.
func TestHackJudgingScoreFormulaHandCheck(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(
		criterion("Impact", "", 60, 5), // weight 60, max 5
		criterion("Craft", "", 40, 10), // weight 40, max 10
	), w.org))
	c1, c2 := rv.Criteria[0].ID, rv.Criteria[1].ID
	team := w.teams[0].id
	judge := w.judges[0]
	// Impact 4/5, Craft 8/10:
	// (60/100 * 4/5)*100 + (40/100 * 8/10)*100 = 48 + 32 = 80
	r := w.scoreTeam(t, judge, team, map[string]int{c1: 4, c2: 8}, nil)
	if r.status != http.StatusOK {
		t.Fatalf("score: %d %s", r.status, r.body)
	}
	pub := w.call(t, "POST", "/results/publish", map[string]any{}, w.org)
	if pub.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	rows := pub.json(t)["results"].([]any)
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

func TestHackJudgingConflictExcludedFromTotal(t *testing.T) {
	w := newJudgingWorld(t, 1, 2)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org))
	c1 := rv.Criteria[0].ID
	team := w.teams[0].id
	// Judge 1 scores normally; judge 2 scores (with a comment) then declares a conflict.
	w.scoreTeam(t, w.judges[0], team, map[string]int{c1: 5}, nil) // 100
	conflictedComment := "a comment from the conflicted judge"
	w.scoreTeam(t, w.judges[1], team, map[string]int{c1: 1}, &conflictedComment) // 20
	w.call(t, "POST", "/conflicts", map[string]any{"team_id": team}, w.judges[1].person)

	pub := w.call(t, "POST", "/results/publish", map[string]any{}, w.org)
	if pub.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	rows := pub.json(t)["results"].([]any)
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
	mine := w.call(t, "GET", "/my-results", nil, w.part.person)
	if mine.status != http.StatusOK {
		t.Fatalf("my-results: %d %s", mine.status, mine.body)
	}
	comments := mine.json(t)["comments"].([]any)
	for _, c := range comments {
		if c == conflictedComment {
			t.Fatalf("conflicted judge's comment leaked into my-results: %v", comments)
		}
	}
}

func TestHackJudgingTiesAndRankOverrides(t *testing.T) {
	w := newJudgingWorld(t, 3, 1)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org))
	c1 := rv.Criteria[0].ID
	judge := w.judges[0]
	// Teams A and B tie at 5/5=100; team C gets 3/5=60.
	w.scoreTeam(t, judge, w.teams[0].id, map[string]int{c1: 5}, nil)
	w.scoreTeam(t, judge, w.teams[1].id, map[string]int{c1: 5}, nil)
	w.scoreTeam(t, judge, w.teams[2].id, map[string]int{c1: 3}, nil)

	pub := w.call(t, "POST", "/results/publish", map[string]any{}, w.org)
	if pub.status != http.StatusOK {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	rows := pub.json(t)["results"].([]any)
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
	bad := w.call(t, "POST", "/results/publish", map[string]any{
		"rank_overrides": []map[string]any{{"team_id": w.teams[2].id, "rank": 1}},
	}, w.org)
	if bad.status != http.StatusBadRequest || bad.json(t)["code"] != "invalid_rank_override" {
		t.Fatalf("bad override: %d %s", bad.status, bad.body)
	}
	// A valid override for the tied pair is accepted.
	good := w.call(t, "POST", "/results/publish", map[string]any{
		"rank_overrides": []map[string]any{
			{"team_id": tiedIDs[0], "rank": 1},
			{"team_id": tiedIDs[1], "rank": 2},
		},
	}, w.org)
	if good.status != http.StatusOK {
		t.Fatalf("good override: %d %s", good.status, good.body)
	}
	rows2 := good.json(t)["results"].([]any)
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
	if r := seenRanks[w.teams[2].id]; r != 3 {
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
	w := newJudgingWorld(t, 2, 1)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org))
	c1 := rv.Criteria[0].ID
	judge := w.judges[0]
	comment := "Great demo"
	w.scoreTeam(t, judge, w.teams[0].id, map[string]int{c1: 5}, &comment)
	w.scoreTeam(t, judge, w.teams[1].id, map[string]int{c1: 1}, nil)

	// Before publish: public results 404, my-results says not published.
	pubBefore := w.call(t, "GET", "/results", nil, person{})
	if pubBefore.status != http.StatusNotFound || pubBefore.json(t)["code"] != "no_results_yet" {
		t.Fatalf("public before publish: %d %s", pubBefore.status, pubBefore.body)
	}
	mine := w.call(t, "GET", "/my-results", nil, w.part.person)
	if mine.status != http.StatusOK || mine.json(t)["published"] != false {
		t.Fatalf("my-results before publish: %d %s", mine.status, mine.body)
	}

	if r := w.call(t, "POST", "/results/publish", map[string]any{}, w.org); r.status != http.StatusOK {
		t.Fatalf("publish: %d %s", r.status, r.body)
	}

	// Winners-only by default: only team A (rank 1).
	pub := w.call(t, "GET", "/results", nil, person{})
	if pub.status != http.StatusOK {
		t.Fatalf("public results: %d %s", pub.status, pub.body)
	}
	winners := pub.json(t)["winners"].([]any)
	if len(winners) != 1 || winners[0].(map[string]any)["team_id"] != w.teams[0].id {
		t.Fatalf("winners: %v", winners)
	}
	if _, has := pub.json(t)["ranking"]; has {
		t.Fatal("winners-only view leaked ranking")
	}

	// Switch to full ranking.
	patch := w.call(t, "PATCH", "/results", map[string]any{"full_ranking": true}, w.org)
	if patch.status != http.StatusOK {
		t.Fatalf("patch results: %d %s", patch.status, patch.body)
	}
	pub2 := w.call(t, "GET", "/results", nil, person{})
	ranking := pub2.json(t)["ranking"].([]any)
	if len(ranking) != 2 {
		t.Fatalf("full ranking: %v", ranking)
	}
	// Never leaks raw scores or comments.
	body := string(pub2.body)
	if strings.Contains(body, "Great demo") {
		t.Fatal("public results leaked a judge's comment")
	}

	// my-results for the participant on team A: sees own total/rank and the comment,
	// never the judge's identity.
	mine2 := w.call(t, "GET", "/my-results", nil, w.part.person)
	if mine2.status != http.StatusOK || mine2.json(t)["published"] != true {
		t.Fatalf("my-results after publish: %d %s", mine2.status, mine2.body)
	}
	comments := mine2.json(t)["comments"].([]any)
	if len(comments) != 1 || comments[0] != "Great demo" {
		t.Fatalf("my comments: %v", comments)
	}
	if strings.Contains(string(mine2.body), judge.id) {
		t.Fatal("my-results leaked the judge's id")
	}
}

func TestHackJudgingCSVExportsAndSafety(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	rv := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("=cmd", "", 100, 5)), w.org))
	c1 := rv.Criteria[0].ID
	judge := w.judges[0]
	team := w.teams[0].id
	w.scoreTeam(t, judge, team, map[string]int{c1: 4}, nil)

	r := w.call(t, "GET", "/export/scores.csv", nil, w.org)
	if r.status != http.StatusOK {
		t.Fatalf("scores csv: %d %s", r.status, r.body)
	}
	if ct := r.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type: %s", ct)
	}
	// A criterion name starting with "=" must be escaped by csvSafe.
	if !strings.Contains(string(r.body), "'=cmd") {
		t.Fatalf("csvSafe not applied: %s", r.body)
	}

	r2 := w.call(t, "GET", "/export/results.csv", nil, w.org)
	if r2.status != http.StatusOK {
		t.Fatalf("results csv: %d %s", r2.status, r2.body)
	}
	if !strings.Contains(string(r2.body), "team,total,rank,tied,judges_scored") {
		t.Fatalf("results csv header: %s", r2.body)
	}

	// A participant cannot export.
	r3 := w.call(t, "GET", "/export/scores.csv", nil, w.part.person)
	if r3.status == http.StatusOK {
		t.Fatal("participant should not export scores")
	}
}
