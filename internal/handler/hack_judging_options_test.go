package handler

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/db"
)

func TestTrackRanksKeepResolvedTieGroupsSeparate(t *testing.T) {
	track := &db.EventTrack{ID: "track-a"}
	score := 80.0
	one, two := 1, 2
	rows := []teamResult{
		{TeamID: "A", Total: &score, Rank: &one, Tied: true, Track: track},
		{TeamID: "B", Total: &score, Rank: &one, Tied: true, Track: track},
		{TeamID: "C", Total: &score, Rank: &two, Tied: true, Track: track, TieDecider: "criterion"},
		{TeamID: "D", Total: &score, Rank: &two, Tied: true, Track: track},
	}
	markTrackWinners(rows)
	for i, want := range []int{1, 1, 2, 2} {
		if rows[i].TrackRank == nil || *rows[i].TrackRank != want || rows[i].TrackWinner != (want == 1) {
			t.Fatalf("track rank %d: got %+v, want %d", i, rows[i], want)
		}
	}
	if err := applyRankOverrides(rows, []rankOverride{{TeamID: "C", Rank: 2}, {TeamID: "D", Rank: 1}}); err != nil {
		t.Fatal(err)
	}
	if rows[0].TeamID != "A" || rows[1].TeamID != "B" || rows[2].TeamID != "D" || rows[3].TeamID != "C" || rows[3].TieDecider != "organiser" || rows[0].TrackRank == nil || *rows[0].TrackRank != 1 || rows[2].TrackRank == nil || *rows[2].TrackRank != 2 {
		t.Fatalf("override reordered a previously resolved group: %+v", rows)
	}
}

func TestOrganiserOverridePreservesAutomaticTieDeciders(t *testing.T) {
	score := 75.0
	one, two, three := 1, 2, 3
	rows := []teamResult{
		{TeamID: "A", Total: &score, Rank: &one, TieDecider: "score"},
		{TeamID: "B", Total: &score, Rank: &two, TieDecider: "criterion"},
		{TeamID: "C", Total: &score, Rank: &three, Tied: true, TieDecider: "unresolved"},
		{TeamID: "D", Total: &score, Rank: &three, Tied: true, TieDecider: "unresolved"},
	}
	if err := applyRankOverrides(rows, []rankOverride{{TeamID: "C", Rank: 2}, {TeamID: "D", Rank: 1}}); err != nil {
		t.Fatal(err)
	}
	if rows[0].TeamID != "A" || rows[1].TeamID != "B" || rows[1].TieDecider != "criterion" || rows[2].TeamID != "D" || rows[3].TeamID != "C" || rows[2].TieDecider != "organiser" || rows[3].TieDecider != "organiser" {
		t.Fatalf("organiser override disturbed decided ordering: %+v", rows)
	}
}

func nearScore(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.02 {
		t.Fatalf("%s: got %.4f want %.4f", name, got, want)
	}
}

func TestJudgingNormalisationHandCalculated(t *testing.T) {
	entries := []judgeEntry{{team: "A", judge: "J1", raw: 100}, {team: "B", judge: "J1", raw: 90}, {team: "A", judge: "J2", raw: 0}, {team: "B", judge: "J2", raw: 80}, {team: "C", judge: "J2", raw: 100}}
	got := normalisedEntries(entries)
	// Global population mean=74, spread=sqrt(1424)=37.7359.
	// J1 mean/spread=95/5; J2=60/sqrt(1866.67).
	// Raw means A=50, B=85; normalised means A=66.67, B=63.87.
	nearScore(t, "A", (got[judgeTeamKey("J1", "A")]+got[judgeTeamKey("J2", "A")])/2, 66.66545)
	nearScore(t, "B", (got[judgeTeamKey("J1", "B")]+got[judgeTeamKey("J2", "B")])/2, 63.86621)
	if got[judgeTeamKey("J1", "A")] <= got[judgeTeamKey("J1", "B")] {
		t.Fatal("within-judge order reversed")
	}
	flat := normalisedEntries([]judgeEntry{{team: "A", judge: "J1", raw: 20}, {team: "B", judge: "J1", raw: 20}, {team: "A", judge: "J2", raw: 40}, {team: "B", judge: "J2", raw: 40}})
	for _, score := range flat {
		nearScore(t, "zero judge spread fallback", score, 30)
	}
	zero := normalisedEntries([]judgeEntry{{team: "A", judge: "J1", raw: 50}, {team: "B", judge: "J1", raw: 50}})
	for _, score := range zero {
		nearScore(t, "zero global spread fallback", score, 50)
		if math.IsNaN(score) {
			t.Fatal("NaN")
		}
	}
}

func TestHackManualAndPanelJudgingAccess(t *testing.T) {
	w := newJudgingWorld(t, 2, 2)
	criterion := decodeRubric(t, w.call(t, "GET", "/rubric", nil, w.org)).Criteria[0].ID
	settings := w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "manual"}, w.org)
	if settings.status != 200 {
		t.Fatalf("manual mode: %d %s", settings.status, settings.body)
	}
	payload := map[string]any{"assignments": []map[string]string{{"judge_id": w.judges[0].id, "team_id": w.teams[0].id}}}
	r := w.call(t, "PUT", "/assignments", payload, w.org)
	if r.status != 200 {
		t.Fatalf("manual assignments: %d %s", r.status, r.body)
	}
	if r = w.call(t, "PUT", "/assignments", payload, w.outsider); r.status != 404 {
		t.Fatalf("outsider assignment write: %d %s", r.status, r.body)
	}
	queue := w.call(t, "GET", "/judge/queue", nil, w.judges[0].person).json(t)["queue"].([]any)
	if len(queue) != 1 || queue[0].(map[string]any)["team_id"] != w.teams[0].id {
		t.Fatalf("manual queue: %v", queue)
	}
	dashboard := w.call(t, "GET", "/judging/dashboard", nil, w.org).json(t)
	manualJudges := dashboard["judges"].([]any)
	manualAssigned := 0.0
	for _, row := range manualJudges {
		manualAssigned += row.(map[string]any)["assigned_count"].(float64)
	}
	if manualAssigned != 1 {
		t.Fatalf("manual dashboard ignored assignments: %v", manualJudges)
	}
	denied := w.call(t, "PUT", "/judge/scores/"+w.teams[1].id, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 5}}}, w.judges[0].person)
	if denied.status != 403 {
		t.Fatalf("unassigned score: %d %s", denied.status, denied.body)
	}
	tracks := w.call(t, "PUT", "/tracks", map[string]any{"tracks": []map[string]string{{"slug": "climate", "name": "Climate", "challenge": "Build", "prize": "Trophy"}}}, w.org)
	if tracks.status != 200 {
		t.Fatalf("tracks: %d %s", tracks.status, tracks.body)
	}
	var listed []map[string]any
	if err := json.Unmarshal(tracks.body, &listed); err != nil || len(listed) != 1 {
		t.Fatalf("track response: %s %v", tracks.body, err)
	}
	trackID := listed[0]["id"].(string)
	if r = w.call(t, "PUT", "/team/track", map[string]string{"track": "climate"}, w.part.person); r.status != 200 {
		t.Fatalf("team track: %d %s", r.status, r.body)
	}
	if r = w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "panel"}, w.org); r.status != 200 {
		t.Fatalf("panel mode: %d %s", r.status, r.body)
	}
	panel := map[string]any{"panels": []map[string]string{{"track_id": trackID, "judge_id": w.judges[1].id}}}
	if r = w.call(t, "PUT", "/judging/panels", panel, w.org); r.status != 200 {
		t.Fatalf("panel members: %d %s", r.status, r.body)
	}
	if r = w.call(t, "GET", "/judge/queue", nil, w.judges[0].person); len(r.json(t)["queue"].([]any)) != 0 {
		t.Fatalf("wrong judge panel queue: %s", r.body)
	}
	if r = w.call(t, "GET", "/judge/queue", nil, w.judges[1].person); len(r.json(t)["queue"].([]any)) != 1 {
		t.Fatalf("panel queue: %s", r.body)
	}
	dashboard = w.call(t, "GET", "/judging/dashboard", nil, w.org).json(t)
	panelJudges := dashboard["judges"].([]any)
	assigned := 0.0
	for _, row := range panelJudges {
		assigned += row.(map[string]any)["assigned_count"].(float64)
	}
	if assigned != 1 {
		t.Fatalf("panel dashboard ignored memberships: %v", panelJudges)
	}
	if r = w.call(t, "PUT", "/judge/scores/"+w.teams[0].id, map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 5}}}, w.judges[0].person); r.status != 403 {
		t.Fatalf("non-panel score: %d %s", r.status, r.body)
	}
	other := uniqueSlug()
	if r = w.a.createEvent(t, w.outsider, other, nil); r.status != 201 {
		t.Fatalf("other event: %d %s", r.status, r.body)
	}
	foreign := w.a.at(t, "PUT", "/v1/hack/events/"+other+"/tracks", map[string]any{"tracks": []map[string]string{{"slug": "foreign", "name": "Foreign"}}}, w.a.key(w.outsider))
	var foreignRows []map[string]any
	if err := json.Unmarshal(foreign.body, &foreignRows); err != nil || len(foreignRows) != 1 {
		t.Fatalf("foreign track: %s %v", foreign.body, err)
	}
	if r = w.call(t, "PUT", "/judging/panels", map[string]any{"panels": []map[string]string{{"track_id": foreignRows[0]["id"].(string), "judge_id": w.judges[1].id}}}, w.org); r.status != 404 {
		t.Fatalf("foreign panel: %d %s", r.status, r.body)
	}
	if r = w.call(t, "POST", "/conflicts", map[string]any{"team_id": w.teams[0].id}, w.judges[1].person); r.status != 200 {
		t.Fatalf("conflict: %d %s", r.status, r.body)
	}
	if r = w.call(t, "GET", "/judge/queue", nil, w.judges[1].person); len(r.json(t)["queue"].([]any)) != 0 {
		t.Fatalf("conflict queue: %s", r.body)
	}
}

func TestHackTieCriterionAndPrizeSnapshot(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	defaults := w.call(t, "GET", "/judging/settings", nil, w.org).json(t)
	if defaults["public_scores"] != true || defaults["public_ranks"] != true {
		t.Fatalf("public results defaults changed: %v", defaults)
	}
	rubric := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 50, 5), criterion("Design", "", 50, 5)), w.org))
	c1, c2 := rubric.Criteria[0].ID, rubric.Criteria[1].ID
	r := w.call(t, "PATCH", "/judging/settings", map[string]any{"tie_criterion_id": c1, "score_mode": "raw"}, w.org)
	if r.status != 200 {
		t.Fatalf("tie rule: %d %s", r.status, r.body)
	}
	w.scoreTeam(t, w.judges[0], w.teams[0].id, map[string]int{c1: 5, c2: 3}, nil)
	w.scoreTeam(t, w.judges[0], w.teams[1].id, map[string]int{c1: 3, c2: 5}, nil)
	tracks := w.call(t, "PUT", "/tracks", map[string]any{"tracks": []map[string]string{{"slug": "climate", "name": "Climate", "prize": "Trophy"}}}, w.org)
	if tracks.status != 200 {
		t.Fatalf("tracks: %d %s", tracks.status, tracks.body)
	}
	if r = w.call(t, "PUT", "/team/track", map[string]string{"track": "climate"}, w.part.person); r.status != 200 {
		t.Fatalf("team track: %d %s", r.status, r.body)
	}
	pub := w.call(t, "POST", "/results/publish", map[string]any{}, w.org)
	if pub.status != 200 {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	rows := pub.json(t)["results"].([]any)
	if rows[0].(map[string]any)["team_id"] != w.teams[0].id || rows[1].(map[string]any)["tie_decider"] != "criterion" {
		t.Fatalf("criterion tie order: %v", rows)
	}
	if rows[0].(map[string]any)["track_winner"] != true {
		t.Fatalf("track winner: %v", rows[0])
	}
	if r = w.call(t, "PATCH", "/judging/settings", map[string]any{"public_scores": false, "public_ranks": false}, w.org); r.status != 200 {
		t.Fatalf("hide public values: %d %s", r.status, r.body)
	}
	public := w.call(t, "GET", "/results", nil, w.outsider)
	if public.status != 200 || !strings.Contains(string(public.body), "Trophy") || strings.Contains(string(public.body), "raw_total") || strings.Contains(string(public.body), "\"rank\"") {
		t.Fatalf("public score privacy: %d %s", public.status, public.body)
	}
	if r = w.call(t, "PATCH", "/judging/settings", map[string]any{"public_scores": true, "public_ranks": true}, w.org); r.status != 200 {
		t.Fatalf("show public scores: %d %s", r.status, r.body)
	}
	public = w.call(t, "GET", "/results", nil, w.outsider)
	if !strings.Contains(string(public.body), "raw_total") {
		t.Fatalf("public scores missing: %s", public.body)
	}
	mine := w.call(t, "GET", "/my-results", nil, w.part.person)
	if mine.status != 200 || !strings.Contains(string(mine.body), "normalised_total") || !strings.Contains(string(mine.body), "Trophy") {
		t.Fatalf("private results: %d %s", mine.status, mine.body)
	}
	csv := w.call(t, "GET", "/export/results.csv", nil, w.org)
	if csv.status != 200 || !strings.Contains(string(csv.body), "normalised_total") || !strings.Contains(string(csv.body), "Trophy") {
		t.Fatalf("results csv: %d %s", csv.status, csv.body)
	}
	stage := w.call(t, "POST", "/stage", map[string]string{"stage": "judging"}, w.org)
	if stage.status != 200 {
		t.Fatalf("judging stage: %d %s", stage.status, stage.body)
	}
	if r = w.call(t, "PATCH", "/judging/settings", map[string]any{"tie_criterion_id": c2}, w.org); r.status != 409 {
		t.Fatalf("late tie change: %d %s", r.status, r.body)
	}
	if r = w.call(t, "PATCH", "/judging/settings", map[string]any{"score_mode": "normalised"}, w.org); r.status != 200 {
		t.Fatalf("normalised preview mode: %d %s", r.status, r.body)
	}
	preview := w.call(t, "GET", "/judging/preview", nil, w.org)
	if preview.status != 200 || preview.json(t)["deciding_mode"] != "normalised" {
		t.Fatalf("preview: %d %s", preview.status, preview.body)
	}
	// A republish records the selected mode; the earlier snapshot was raw.
	if r = w.call(t, "POST", "/results/publish", map[string]any{}, w.org); r.status != 200 || !strings.Contains(string(r.body), "normalised_total") {
		t.Fatalf("republish: %d %s", r.status, r.body)
	}
}

func TestHackNormalisedRankingAndJudgeMajority(t *testing.T) {
	w := newJudgingWorld(t, 3, 2)
	c := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 10)), w.org)).Criteria[0].ID
	for _, tc := range []struct{ judge, team, points int }{{0, 0, 10}, {0, 1, 9}, {1, 0, 0}, {1, 1, 8}, {1, 2, 10}} {
		w.scoreTeam(t, w.judges[tc.judge], w.teams[tc.team].id, map[string]int{c: tc.points}, nil)
	}
	raw := w.call(t, "GET", "/judging/preview", nil, w.org).json(t)["results"].([]any)
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
	if rank(raw, w.teams[0].id) <= rank(raw, w.teams[1].id) {
		t.Fatalf("raw should rank B ahead of A: %v", raw)
	}
	if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"score_mode": "normalised"}, w.org); r.status != 200 {
		t.Fatalf("mode: %d %s", r.status, r.body)
	}
	norm := w.call(t, "GET", "/judging/preview", nil, w.org).json(t)["results"].([]any)
	if rank(norm, w.teams[0].id) >= rank(norm, w.teams[1].id) {
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

	major := newJudgingWorld(t, 2, 3)
	c = decodeRubric(t, major.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), major.org)).Criteria[0].ID
	for _, tc := range []struct{ judge, a, b int }{{0, 5, 4}, {1, 5, 4}, {2, 1, 3}} {
		major.scoreTeam(t, major.judges[tc.judge], major.teams[0].id, map[string]int{c: tc.a}, nil)
		major.scoreTeam(t, major.judges[tc.judge], major.teams[1].id, map[string]int{c: tc.b}, nil)
	}
	rows := major.call(t, "POST", "/results/publish", map[string]any{}, major.org).json(t)["results"].([]any)
	if rows[0].(map[string]any)["team_id"] != major.teams[0].id || rows[1].(map[string]any)["tie_decider"] != "judge_majority" {
		t.Fatalf("majority tie: %v", rows)
	}
}

func TestHackMultiwayOrganiserTieDecisionRecorded(t *testing.T) {
	w := newJudgingWorld(t, 3, 1)
	c := decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Impact", "", 100, 5)), w.org)).Criteria[0].ID
	for _, team := range w.teams {
		w.scoreTeam(t, w.judges[0], team.id, map[string]int{c: 5}, nil)
	}
	pre := w.call(t, "GET", "/judging/preview", nil, w.org).json(t)["results"].([]any)
	for _, x := range pre {
		m := x.(map[string]any)
		if m["rank"] != float64(1) || m["tied"] != true {
			t.Fatalf("multiway unresolved: %v", pre)
		}
	}
	order := []map[string]any{{"team_id": w.teams[2].id, "rank": 1}, {"team_id": w.teams[0].id, "rank": 2}, {"team_id": w.teams[1].id, "rank": 3}}
	pub := w.call(t, "POST", "/results/publish", map[string]any{"rank_overrides": order}, w.org)
	if pub.status != 200 {
		t.Fatalf("publish: %d %s", pub.status, pub.body)
	}
	rows := pub.json(t)["results"].([]any)
	for i, x := range rows {
		m := x.(map[string]any)
		if m["rank"] != float64(i+1) || m["tie_decider"] != "organiser" {
			t.Fatalf("recorded choice: %v", rows)
		}
	}
	if r := w.call(t, "PATCH", "/results", map[string]any{"full_ranking": true}, w.org); r.status != 200 {
		t.Fatalf("full ranking: %d %s", r.status, r.body)
	}
	if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"public_ranks": false}, w.org); r.status != 200 {
		t.Fatalf("hide ranks: %d %s", r.status, r.body)
	}
	public := w.call(t, "GET", "/results", nil, w.outsider).json(t)["ranking"].([]any)
	for i, row := range public {
		item := row.(map[string]any)
		if item["team_name"] != w.teams[i].name || item["rank"] != nil || item["track_rank"] != nil {
			t.Fatalf("hidden ranking order or fields: %v", public)
		}
	}
}
