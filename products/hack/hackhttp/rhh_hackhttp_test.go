package hackhttp

import (
	"strings"
	"testing"

	"github.com/lib/pq"
)

func TestRhhEntrySaveRulesOnRetiredPreset(t *testing.T) {
	w := newRulesWorld(t, 0)
	a := w.a
	base := "/v1/hack/events/" + w.slug + "/entry-rules"

	// Simulate an event that already existed on the "build" preset before
	// SIM-382 (a new event today starts on video_or_website instead).
	buildRules := `{"v":1,"fields":{"title":"required","problem":"required","tagline":"required","audience":"optional","track":"required","tools":"required","maturity":"required","description":"optional","video_url":"one_of","site":"one_of","code_url":"off","screenshot":"optional"}}`
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

func TestRhhResultsOldSparseSnapshotPassesThrough(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	w.setStage(t, "results")
	snap := `[{"team_id":"t1","team_name":"Old","rank":1,"total":"n/a","legacy":"keep"}]`
	if _, err := w.a.database.Exec(`INSERT INTO event_results (event_id, full_ranking, snapshot)
		SELECT id, true, $2::jsonb FROM events WHERE slug=$1`, w.slug, snap); err != nil {
		t.Fatal(err)
	}
	org := w.call(t, "GET", "/results", nil, w.org)
	if org.status != 200 || !strings.Contains(string(org.body), `"legacy":"keep"`) || strings.Contains(string(org.body), "judges_scored") {
		t.Fatalf("organiser: %d %s", org.status, org.body)
	}
	pub := w.call(t, "GET", "/results", nil, person{})
	if pub.status != 200 || strings.Contains(string(pub.body), `"track_winner":false`) || !strings.Contains(string(pub.body), `"track_winner":null`) {
		t.Fatalf("public: %d %s", pub.status, pub.body)
	}
	csv := w.call(t, "GET", "/export/results.csv", nil, w.org)
	if csv.status != 200 || !strings.Contains(string(csv.body), "Old") {
		t.Fatalf("csv: %d %s", csv.status, csv.body)
	}
}

func TestRhhFormerJudgeConflictReversible(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "lvconf-org")
	p1 := a.newPerson(t, "lvconf-p1")
	p2 := a.newPerson(t, "lvconf-p2")
	judge := a.newPerson(t, "lvconf-j")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	a.joinParticipant(t, join, p2, "Quinn")
	teamX, _ := a.createTeamAndJoin(t, slug, p1, "TeamX")
	teamY, _ := a.createTeamAndJoin(t, slug, p2, "TeamY")
	a.giveSubmittedEntry(t, slug, teamX)
	a.giveSubmittedEntry(t, slug, teamY)
	a.joinJudge(t, judgeCode, judge, "Judge")
	if _, err := a.database.Exec(`UPDATE events SET stage='judging' WHERE slug=$1`, slug); err != nil {
		t.Fatal(err)
	}
	criterion := a.firstCriterionID(t, slug)
	wantTS(t, "score X", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamX,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 2}}}, a.key(judge)), 200, "")
	wantTS(t, "score Y", a.at(t, "PUT", "/v1/hack/events/"+slug+"/judge/scores/"+teamY,
		map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": 3}}}, a.key(judge)), 200, "")

	eventID := a.eventID(t, slug)
	judgeID := a.userID(t, judge)
	// A conflict declared on X by mistake, in place when the judge leaves.
	if _, err := a.database.Exec(`INSERT INTO event_conflicts (event_id, judge_id, team_id) VALUES ($1, $2, $3)`, eventID, judgeID, teamX); err != nil {
		t.Fatal(err)
	}
	wantTS(t, "leave", a.leave(t, slug, judge, map[string]any{"confirm": slug}), 200, "")

	var countedIDs []string
	if err := a.database.QueryRow(`SELECT counted_team_ids FROM event_former_judges WHERE event_id=$1 AND user_id=$2`, eventID, judgeID).Scan(pq.Array(&countedIDs)); err != nil {
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
	dash := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
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
	wantTS(t, "remove conflict", a.at(t, "DELETE", "/v1/hack/events/"+slug+"/conflicts/"+teamX+"?judge_user_id="+judgeID, nil, a.key(org)), 204, "")
	dash2 := a.at(t, "GET", "/v1/hack/events/"+slug+"/judging/dashboard", nil, a.key(org)).json(t)
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
	a := newHackApp(t)
	org := a.newPerson(t, "lvstableorg")
	p1 := a.newPerson(t, "lvstablep1")
	p2 := a.newPerson(t, "lvstablep2")
	judge1 := a.newPerson(t, "lvstablej1")
	judge2 := a.newPerson(t, "lvstablej2")
	slug := uniqueSlug()
	wantTS(t, "create", a.createEvent(t, org, slug, nil), 201, "")
	a.openEvent(t, org, slug)
	join, judgeCode := a.joinCodes(t, org, slug)
	a.joinParticipant(t, join, p1, "Pat")
	a.joinParticipant(t, join, p2, "Quinn")
	teamA, _ := a.createTeamAndJoin(t, slug, p1, "TeamA")
	teamB, _ := a.createTeamAndJoin(t, slug, p2, "TeamB")
	a.giveSubmittedEntry(t, slug, teamA)
	a.giveSubmittedEntry(t, slug, teamB)
	a.joinJudge(t, judgeCode, judge1, "Judge1")
	a.joinJudge(t, judgeCode, judge2, "Judge2")
	base := "/v1/hack/events/" + slug
	wantTS(t, "start judging", a.at(t, "POST", base+"/stage", map[string]string{"stage": "judging"}, a.key(org)), 200, "")
	criterion := a.firstCriterionID(t, slug)
	score := func(judge person, teamID string, points int) {
		wantTS(t, "score", a.at(t, "PUT", base+"/judge/scores/"+teamID,
			map[string]any{"scores": []map[string]any{{"criterion_id": criterion, "points": points}}}, a.key(judge)), 200, "")
	}
	score(judge1, teamA, 3)
	score(judge1, teamB, 4)
	score(judge2, teamA, 5)
	score(judge2, teamB, 1)

	snapshot := func() map[string]map[string]any {
		pub := a.at(t, "POST", base+"/results/publish", map[string]any{}, a.key(org))
		if pub.status != 200 {
			t.Fatalf("publish: %d %s", pub.status, pub.body)
		}
		out := map[string]map[string]any{}
		for _, raw := range pub.json(t)["results"].([]any) {
			row := raw.(map[string]any)
			out[row["team_id"].(string)] = row
		}
		return out
	}

	before := snapshot()
	wantTS(t, "judge1 leaves", a.leave(t, slug, judge1, map[string]any{"confirm": slug}), 200, "")
	after := snapshot()

	for _, teamID := range []string{teamA, teamB} {
		b, af := before[teamID], after[teamID]
		if b["total"] != af["total"] || b["rank"] != af["rank"] || b["judges_scored"] != af["judges_scored"] {
			t.Fatalf("team %s changed across the leave: before=%v after=%v", teamID, b, af)
		}
	}
}
