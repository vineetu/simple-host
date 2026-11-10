package hackhttp

import (
	"os"
	"reflect"
	"testing"
)

func TestOrganiserJudgeSelectionAndAlias(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	orgID := w.a.userID(t, w.org)
	settings := func(body map[string]any) map[string]any {
		t.Helper()
		r := w.call(t, "PATCH", "/judging/settings", body, w.org)
		if r.status != 200 {
			t.Fatalf("settings: %d %s", r.status, r.body)
		}
		return r.json(t)
	}
	selected := func(want int) {
		t.Helper()
		r := w.call(t, "GET", "/judging/settings", nil, w.org).json(t)
		if len(r["organiser_judges"].([]any)) != want || len(r["judges"].([]any)) != want+1 {
			t.Fatalf("judge set: %v", r)
		}
	}
	selected(0)
	settings(map[string]any{"organiser_judges": []string{orgID}})
	selected(1)
	settings(map[string]any{"public_scores": false})
	selected(1)
	settings(map[string]any{"include_organisers": false})
	selected(0)
	settings(map[string]any{"include_organisers": true})
	selected(1)
	settings(map[string]any{"include_organisers": true, "organiser_judges": []string{}})
	selected(0)
	if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{w.judges[0].id}}, w.org); r.status < 400 {
		t.Fatal("accepted non-organiser")
	}
	settings(map[string]any{"assignment_mode": "automatic", "include_organisers": true})
	for _, body := range []map[string]any{nil, {"include_organisers": false}, {"include_organisers": true}} {
		r := w.call(t, "POST", "/assignments/generate", body, w.org)
		if r.status != 200 {
			t.Fatalf("generation: %d %s", r.status, r.body)
		}
		want := 1
		if body != nil && body["include_organisers"] == false {
			want = 0
		}
		selected(want)
	}
	// Preview is read-only and uses the same judge set and conflicts as generation.
	before := w.call(t, "GET", "/assignments", nil, w.org).json(t)
	dash := w.call(t, "GET", "/judging/dashboard", nil, w.org).json(t)
	if len(dash["coverage"].(map[string]any)["short"].([]any)) != 0 {
		t.Fatalf("selected organiser missing from coverage: %v", dash)
	}
	after := w.call(t, "GET", "/assignments", nil, w.org).json(t)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("coverage persisted assignments")
	}
	settings(map[string]any{"include_organisers": false})
	dash = w.call(t, "GET", "/judging/dashboard", nil, w.org).json(t)
	if len(dash["coverage"].(map[string]any)["short"].([]any)) != 2 {
		t.Fatal("coverage failed to report shortage")
	}
}

func TestUntickedOrganiserCannotJudgeAnyMode(t *testing.T) {
	for _, mode := range []string{"open", "automatic", "manual", "panel"} {
		t.Run(mode, func(t *testing.T) {
			w := newJudgingWorld(t, 1, 1)
			id := w.a.userID(t, w.org)
			if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": mode}, w.org); r.status != 200 {
				t.Fatal(string(r.body))
			}
			w.setStage(t, "judging")
			event := w.a.at(t, "GET", "/v1/hack/events/"+w.slug, nil, w.a.key(w.org)).json(t)
			if event["can"].(map[string]any)["judge"] != false {
				t.Fatal("unticked can.judge")
			}
			for _, path := range []string{"/judge/queue", "/checklist?role=judge", "/judge/scores/" + w.teams[0].id} {
				if r := w.call(t, "GET", path, nil, w.org); r.status < 400 {
					t.Fatalf("unticked organiser admitted: %s %s", path, r.body)
				}
			}
			rubric := decodeRubric(t, w.call(t, "GET", "/rubric", nil, w.org))
			score := map[string]any{"scores": []map[string]any{{"criterion_id": rubric.Criteria[0].ID, "points": 3}}}
			if r := w.call(t, "PUT", "/judge/scores/"+w.teams[0].id, score, w.org); r.status < 400 {
				t.Fatal("unticked score accepted")
			}
			w.setStage(t, "closed")
			if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{id}}, w.org); r.status != 200 {
				t.Fatal(string(r.body))
			}
			event = w.a.at(t, "GET", "/v1/hack/events/"+w.slug, nil, w.a.key(w.org)).json(t)
			if event["can"].(map[string]any)["judge"] != true {
				t.Fatal("selected can.judge false")
			}
			if r := w.call(t, "GET", "/judge/queue", nil, w.org); r.status != 200 {
				t.Fatalf("selected queue: %d %s", r.status, r.body)
			}
			w.setStage(t, "judging")
			if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{}}, w.org); r.status != 409 {
				t.Fatal("selection not setup-locked")
			}
		})
	}
}

func TestOrganiserJudgeMigrationBackfillsOnlyScoredCurrentOrganisers(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	id := w.a.userID(t, w.org)
	co := w.a.newPerson(t, "migration-org")
	if _, err := w.a.database.Exec(`INSERT INTO event_members(event_id,user_id,role) SELECT id,$2,'organiser' FROM events WHERE slug=$1`, w.slug, w.a.userID(t, co)); err != nil {
		t.Fatal(err)
	}

	// Any partial score counts, irrespective of today's scoring eligibility.
	_, err := w.a.database.Exec(`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT event_id,$2,$3,id,1 FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$1) LIMIT 1`, w.slug, id, w.teams[0].id)
	if err != nil {
		t.Fatal(err)
	}
	sql, err := os.ReadFile("../../db/migrations/0013-hack-organiser-judges.sql")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := w.a.database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for range 2 {
		if _, err = tx.Exec(string(sql)); err != nil {
			t.Fatal(err)
		}
	}
	var selected bool
	if err = tx.QueryRow(`SELECT organiser_judges=ARRAY[$2::uuid] FROM events WHERE slug=$1`, w.slug, id).Scan(&selected); err != nil || !selected {
		t.Fatalf("backfill: %v %v", selected, err)
	}
	var scores int
	if err = tx.QueryRow(`SELECT count(*) FROM event_scores WHERE judge_id=$1`, id).Scan(&scores); err != nil || scores != 1 {
		t.Fatalf("scores changed: %d %v", scores, err)
	}
}

func TestSetupCoverageAllModesAccountsForConflicts(t *testing.T) {
	for _, mode := range []string{"open", "automatic", "manual", "panel"} {
		t.Run(mode, func(t *testing.T) {
			w := newJudgingWorld(t, 1, 2)
			teamID := w.teams[0].id
			if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": mode, "judges_per_team": 2}, w.org); r.status != 200 {
				t.Fatal(string(r.body))
			}
			if mode == "manual" {
				assignments := []map[string]string{}
				for _, j := range w.judges {
					assignments = append(assignments, map[string]string{"judge_id": j.id, "team_id": teamID})
				}
				if r := w.call(t, "PUT", "/assignments", map[string]any{"assignments": assignments}, w.org); r.status != 200 {
					t.Fatal(string(r.body))
				}
			}
			if mode == "panel" {
				r := w.call(t, "PUT", "/tracks", map[string]any{"tracks": []map[string]string{{"slug": "ideas", "name": "Ideas"}}}, w.org)
				if r.status != 200 {
					t.Fatal(string(r.body))
				}
				var trackID, teamSlug string
				if err := w.a.database.QueryRow(`SELECT id FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug).Scan(&trackID); err != nil {
					t.Fatal(err)
				}
				if err := w.a.database.QueryRow(`SELECT slug FROM event_teams WHERE id=$1`, teamID).Scan(&teamSlug); err != nil {
					t.Fatal(err)
				}
				r = w.call(t, "PUT", "/team/track", map[string]string{"team": teamSlug, "track": "ideas"}, w.org)
				if r.status != 200 {
					t.Fatalf("organiser track fix: %d %s", r.status, r.body)
				}
				panels := []map[string]string{}
				for _, j := range w.judges {
					panels = append(panels, map[string]string{"judge_id": j.id, "track_id": trackID})
				}
				if r = w.call(t, "PUT", "/judging/panels", map[string]any{"panels": panels}, w.org); r.status != 200 {
					t.Fatal(string(r.body))
				}
			}
			coverage := func() map[string]any {
				t.Helper()
				return w.call(t, "GET", "/judging/dashboard", nil, w.org).json(t)["coverage"].(map[string]any)
			}
			if c := coverage(); len(c["short"].([]any)) != 0 || c["min"] != float64(2) || c["max"] != float64(2) {
				t.Fatalf("full coverage: %v", c)
			}
			if r := w.call(t, "POST", "/conflicts", map[string]any{"judge_user_id": w.judges[0].id, "team_id": teamID}, w.org); r.status != 200 && r.status != 201 {
				t.Fatal(string(r.body))
			}
			short := coverage()["short"].([]any)
			if len(short) != 1 || short[0].(map[string]any)["have"] != float64(1) || coverage()["min"] != float64(1) {
				t.Fatalf("conflicted coverage: %v", short)
			}
		})
	}
}

func TestUntickingPreservesScoresAndFormerJudgeCounts(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	orgID := w.a.userID(t, w.org)
	if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{orgID}}, w.org); r.status != 200 {
		t.Fatal(string(r.body))
	}
	w.setStage(t, "judging")
	rubric := decodeRubric(t, w.call(t, "GET", "/rubric", nil, w.org))
	points := map[string]int{}
	for _, c := range rubric.Criteria {
		points[c.ID] = 1
	}
	w.scoreTeam(t, w.judges[0], w.teams[0].id, points, nil)
	scoreRows := []map[string]any{}
	for _, c := range rubric.Criteria {
		scoreRows = append(scoreRows, map[string]any{"criterion_id": c.ID, "points": 1})
	}
	if r := w.call(t, "PUT", "/judge/scores/"+w.teams[0].id, map[string]any{"scores": scoreRows}, w.org); r.status != 200 {
		t.Fatalf("selected score: %d %s", r.status, r.body)
	}
	if r := w.call(t, "POST", "/leave", map[string]string{"confirm": w.slug}, w.judges[0].person); r.status != 200 {
		t.Fatalf("judge leave: %d %s", r.status, r.body)
	}
	scored := func() float64 {
		t.Helper()
		return w.call(t, "GET", "/judging/dashboard", nil, w.org).json(t)["teams"].([]any)[0].(map[string]any)["judges_scored"].(float64)
	}
	if got := scored(); got != 2 {
		t.Fatalf("before untick: %v", got)
	}
	w.setStage(t, "closed")
	if r := w.call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{}}, w.org); r.status != 200 {
		t.Fatal(string(r.body))
	}
	if got := scored(); got != 1 {
		t.Fatalf("untick changed former scores or counted unticked organiser: %v", got)
	}
	var rows int
	if err := w.a.database.QueryRow(`SELECT count(*) FROM event_scores WHERE judge_id=$1`, orgID).Scan(&rows); err != nil || rows != len(rubric.Criteria) {
		t.Fatalf("untick deleted score rows: %d %v", rows, err)
	}
}

func TestGenerateAliasRespectsSetupLock(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	wantTS(t, "automatic", w.call(t, "PATCH", "/judging/settings", map[string]any{"assignment_mode": "automatic"}, w.org), 200, "")
	for _, stage := range []string{"judging", "results"} {
		w.setStage(t, stage)
		wantTS(t, "PATCH selection locked", w.call(t, "PATCH", "/judging/settings", map[string]any{"include_organisers": true}, w.org), 409, "judging_setup_locked")
		wantTS(t, "generate selection locked", w.call(t, "POST", "/assignments/generate", map[string]any{"include_organisers": true}, w.org), 409, "judging_setup_locked")
		if stage == "results" {
			wantTS(t, "generation without alias unchanged", w.call(t, "POST", "/assignments/generate", map[string]any{}, w.org), 200, "")
		}
	}
	w = newJudgingWorld(t, 1, 0)
	w.setStage(t, "results")
	items, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
	if item, ok := items["o.judges"]; ok && (item["actionable"] != false || item["blocked_reason"] == nil) {
		t.Fatal("Results without judges must hide or block the judges item", item)
	}
}
