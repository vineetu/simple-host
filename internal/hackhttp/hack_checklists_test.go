package hackhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	accountapp "github.com/vsriram/simple-host/internal/app/accounts"
	entryapp "github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"strings"
	"testing"
)

func checklistItems(t *testing.T, r resp) (map[string]map[string]any, map[string]any) {
	t.Helper()
	if r.status != 200 {
		t.Fatalf("checklist: %d %s", r.status, r.body)
	}
	can, ok := r.json(t)["can"].(map[string]any)
	if !ok || len(can) != 3 {
		t.Fatalf("missing abilities: %s", r.body)
	}
	c, ok := r.json(t)["checklist"].(map[string]any)
	if !ok {
		t.Fatalf("missing checklist: %s", r.body)
	}
	items := map[string]map[string]any{}
	for _, raw := range c["items"].([]any) {
		it := raw.(map[string]any)
		items[it["id"].(string)] = it
	}
	return items, c
}
func TestChecklistMarksAuthorityAndFacts(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	w.setStage(t, "draft")
	sql := func(query string, args ...any) {
		t.Helper()
		if _, err := w.a.database.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	org2 := w.a.newPerson(t, "ck2")
	id2 := w.a.userID(t, org2)
	sql(`INSERT INTO event_members(event_id,user_id,role) SELECT id,$2,'organiser' FROM events WHERE slug=$1`, w.slug, id2)
	get := func(p person) map[string]map[string]any {
		rows, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, p))
		return rows
	}
	mark := func(method, item string, p person) resp {
		return w.call(t, method, "/checklist/not-needed/"+item, nil, p)
	}
	for _, tc := range []struct {
		p                       person
		manage, judge, takePart bool
	}{{w.org, true, false, false}, {w.judges[0].person, false, true, false}, {w.part.person, false, false, true}} {
		can := w.call(t, "GET", "/checklist", nil, tc.p).json(t)["can"].(map[string]any)
		if can["manage"] != tc.manage || can["judge"] != tc.judge || can["take_part"] != tc.takePart {
			t.Fatalf("membership abilities: %v", can)
		}
	}
	if get(w.org)["o.faq"]["state"] != "optional" {
		t.Fatal("initial FAQ")
	}
	checklistItems(t, mark("POST", "o.faq", w.org))
	if get(org2)["o.faq"]["state"] != "not_needed" {
		t.Fatal("not shared")
	}
	sql(`INSERT INTO event_content(event_id,faq) SELECT id,'[{"question":"When?","answer":"Today"}]'::jsonb FROM events WHERE slug=$1 ON CONFLICT(event_id) DO UPDATE SET faq=excluded.faq`, w.slug)
	if get(w.org)["o.faq"]["state"] != "done" {
		t.Fatal("facts must win")
	}
	sql(`UPDATE event_content SET faq='[]' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug)
	if get(w.org)["o.faq"]["state"] != "not_needed" {
		t.Fatal("mark must reappear")
	}
	checklistItems(t, mark("DELETE", "o.faq", org2))
	if get(w.org)["o.faq"]["state"] != "optional" {
		t.Fatal("restore")
	}
	for _, id := range []string{"o.basics", "made-up", "j.closes"} {
		r := mark("POST", id, w.org)
		if r.status != 400 {
			t.Fatalf("%s %d %s", id, r.status, r.body)
		}
	}
	for _, p := range []person{w.part.person, w.judges[0].person} {
		r := mark("POST", "o.faq", p)
		if r.status != 403 {
			t.Fatalf("wrong role %d %s", r.status, r.body)
		}
	}
	r := w.a.at(t, "POST", w.path("/checklist/not-needed/o.faq"), nil, w.a.adminH())
	if r.status != 404 {
		t.Fatalf("synthetic admin write %d", r.status)
	}
	r = w.a.at(t, "GET", w.path("/checklist"), nil, w.a.adminH())
	items, c := checklistItems(t, r)
	if c["next"] != nil || items["o.faq"]["can_dismiss"] != false {
		t.Fatal("admin read only")
	}
	if can := r.json(t)["can"].(map[string]any); can["manage"] != false || can["judge"] != false || can["take_part"] != false {
		t.Fatalf("inspection abilities: %v", can)
	}
	// Actual organiser membership wins over the platform-admin status.
	sql(`INSERT INTO event_members(event_id,user_id,role) SELECT e.id,u.id,'organiser' FROM events e CROSS JOIN users u WHERE e.slug=$1 AND u.is_admin ON CONFLICT DO NOTHING`, w.slug)
	checklistItems(t, w.a.at(t, "POST", w.path("/checklist/not-needed/o.faq"), nil, w.a.adminH()))
	w.setStage(t, "archived")
	if mark("POST", "o.faq", w.org).status != 409 {
		t.Fatal("archived mark")
	}
}

func TestChecklistAcknowledgementsAndCleanup(t *testing.T) {
	w := newJudgingWorld(t, 1, 2)
	read := func(p person, role string) map[string]map[string]any {
		rows, _ := checklistItems(t, w.call(t, "GET", "/checklist?role="+role, nil, p))
		return rows
	}
	ack := func(p person, id, version string) resp {
		return w.call(t, "POST", "/checklist/acknowledgements/"+id, map[string]string{"version": version}, p)
	}
	j := w.judges[0].person
	items := read(j, "judge")
	v := items["j.rubric"]["version"].(string)
	checklistItems(t, ack(j, "j.rubric", v))
	if read(j, "judge")["j.rubric"]["state"] != "done" || read(w.judges[1].person, "judge")["j.rubric"]["state"] != "todo" {
		t.Fatal("own-only")
	}
	checklistItems(t, ack(j, "j.conflicts", ""))
	if read(j, "judge")["j.conflicts"]["state"] != "done" {
		t.Fatal("conflicts ack")
	}
	r := ack(w.org, "o.rubric-ready", v)
	if r.status != 400 || r.json(t)["code"] != "invalid_checklist_item" {
		t.Fatalf("removed acknowledgement accepted: %d %s", r.status, r.body)
	}
	if _, err := w.a.database.Exec(`UPDATE rubric_criteria SET description='Changed' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug); err != nil {
		t.Fatal(err)
	}
	if ack(j, "j.rubric", v).status != 409 {
		t.Fatal("stale version accepted")
	}
	if read(j, "judge")["j.rubric"]["state"] != "todo" {
		t.Fatal("judge rubric read remained done after the rubric changed")
	}
	if read(j, "judge")["j.conflicts"]["state"] != "done" {
		t.Fatal("conflicts versioned")
	}
	if ack(w.part.person, "j.rubric", v).status != 403 {
		t.Fatal("participant acknowledgement")
	}
	if ack(j, "j.coc", "").status != 400 {
		t.Fatal("conduct fabricated")
	}
	// Shared deletion path: leaving, organiser removal and person removal all call it.
	r = w.call(t, "POST", "/leave", map[string]any{"confirm": w.slug}, j)
	if r.status != 200 {
		t.Fatalf("leave %d %s", r.status, r.body)
	}
	if n := w.countRows(t, `SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`, w.judges[0].id); n != 0 {
		t.Fatal("personal acks survived leave", n)
	}
	ev := w.call(t, "GET", "", nil, w.org).json(t)
	code := ev["organiser"].(map[string]any)["judge_code"].(string)
	r = w.a.at(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true}, w.a.key(j))
	if r.status != 200 {
		t.Fatalf("rejoin %d %s", r.status, r.body)
	}
	if read(j, "judge")["j.conflicts"]["state"] != "todo" {
		t.Fatal("reused rejoin ack")
	}
	w.setStage(t, "archived")
	if ack(j, "j.conflicts", "").status != 409 {
		t.Fatal("archived ack")
	}
}

func TestChecklistJudgingCompletionAndTies(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	if r := w.call(t, "POST", "/stage", map[string]any{"stage": "judging"}, w.org); r.status != 200 {
		t.Fatalf("start judging: %d %s", r.status, r.body)
	}
	items := func() map[string]map[string]any {
		r, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		return r
	}
	score := func(team string, all bool) {
		t.Helper()
		var criteria []string
		rows, err := w.a.database.Query(`SELECT id FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$1) ORDER BY position`, w.slug)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			criteria = append(criteria, id)
		}
		rows.Close()
		if !all {
			criteria = criteria[:1]
		}
		for _, id := range criteria {
			_, err := w.a.database.Exec(`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT id,$2,$3,$4,3 FROM events WHERE slug=$1 ON CONFLICT(judge_id,team_id,criterion_id) DO UPDATE SET points=3`, w.slug, w.judges[0].id, team, id)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	score(w.teams[1].id, true)
	score(w.teams[0].id, false)
	var eventID string
	if err := w.a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, w.slug).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	ownFacts, err := entryapp.New(w.a.database, nil).ChecklistJudging(context.Background(), model.Event{ID: eventID, Stage: "judging", JudgeAssignmentMode: "open"}, w.judges[0].id, false)
	if err != nil {
		t.Fatal(err)
	}
	if ownFacts.Judges != 0 || ownFacts.Organisers != 0 || ownFacts.JudgesRequired != 0 || ownFacts.HasStandings || ownFacts.OwnDone != 1 || ownFacts.OwnTotal != 2 {
		t.Fatalf("judge-only gathered organiser facts or changed personal counts: %+v", ownFacts)
	}
	for _, judgeID := range []string{"", w.judges[0].id} {
		counts, err := entryrepo.JudgeCompletionCounts(context.Background(), w.a.database, eventID, judgeID)
		if err != nil {
			t.Fatal(err)
		}
		for _, team := range w.teams {
			for _, criteriaCount := range []int{0, 1, 2, 3} {
				done, err := entryrepo.JudgeDoneForTeam(context.Background(), w.a.database, eventID, w.judges[0].id, team.id, criteriaCount)
				if err != nil {
					t.Fatal(err)
				}
				if got := criteriaCount > 0 && counts[w.judges[0].id][team.id] >= criteriaCount; got != done {
					t.Fatalf("batch completion differs: team %s criteria %d", team.id, criteriaCount)
				}
			}
		}
	}

	if items()["o.scores"]["state"] != "todo" {
		t.Fatal("partial criterion completed judge")
	}
	personal, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.judges[0].person))
	if got := personal["j.scores"]; got["count"] != float64(1) || got["total"] != float64(2) || got["state"] != "todo" {
		t.Fatalf("partial personal: %v", got)
	}
	if personal["j.rubric"]["state"] != "todo" {
		t.Fatal("partially scored queue must still require rubric acknowledgement")
	}
	score(w.teams[0].id, true)
	personal, _ = checklistItems(t, w.call(t, "GET", "/checklist", nil, w.judges[0].person))
	if got := personal["j.scores"]; got["count"] != float64(2) || got["state"] != "done" {
		t.Fatalf("complete personal: %v", got)
	}
	if personal["j.rubric"]["state"] != "done" {
		t.Fatal("fully scored queue must complete rubric without acknowledgement")
	}
	checklistItems(t, w.call(t, "POST", "/checklist/acknowledgements/j.conflicts", map[string]any{}, w.judges[0].person))
	_, personalChecklist := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.judges[0].person))
	if next := personalChecklist["next"].(map[string]any); next["item_id"] != "j.scores" {
		t.Fatalf("completed judge must reach review scores hero: %v", next)
	}
	if got := items()["o.scores"]; got["state"] != "done" || got["total"] != float64(1) {
		t.Fatalf("unscored open organiser counted: %v", got)
	}
	w.setStage(t, "results")
	personal, _ = checklistItems(t, w.call(t, "GET", "/checklist", nil, w.judges[0].person))
	if personal["j.closes"]["state"] == "done" {
		t.Fatal("unlocked results completed judging closes")
	}
	if items()["o.ties"]["state"] != "todo" {
		t.Fatal("unresolved ties done")
	}
	if _, err := w.a.database.Exec(`UPDATE events SET judging_locked_at=now() WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}
	if r := w.call(t, "POST", "/results/publish", map[string]any{}, w.org); r.status != 200 {
		t.Fatalf("publish unresolved: %d %s", r.status, r.body)
	}
	if items()["o.ties"]["state"] != "todo" {
		t.Fatal("unresolved publication completed ties")
	}
	overrides := []map[string]any{{"team_id": w.teams[0].id, "rank": 1}, {"team_id": w.teams[1].id, "rank": 2}}
	r := w.call(t, "POST", "/results/publish", map[string]any{"rank_overrides": overrides}, w.org)
	if r.status != 200 {
		t.Fatalf("publish %d %s", r.status, r.body)
	}
	personal, _ = checklistItems(t, w.call(t, "GET", "/checklist", nil, w.judges[0].person))
	if personal["j.closes"]["state"] != "done" {
		t.Fatal("published locked results did not complete closes")
	}
	if items()["o.ties"]["state"] != "done" {
		t.Fatal("published tie unresolved")
	}
	// Change both scores equally: current tie remains but the snapshot no longer matches.
	if _, err := w.a.database.Exec(`UPDATE event_scores SET points=4 WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug); err != nil {
		t.Fatal(err)
	}
	if items()["o.ties"]["state"] != "todo" {
		t.Fatal("stale snapshot completed ties")
	}
	r = w.call(t, "POST", "/results/publish", map[string]any{"rank_overrides": overrides}, w.org)
	if r.status != 200 {
		t.Fatalf("republish %d %s", r.status, r.body)
	}
	if items()["o.ties"]["state"] != "done" {
		t.Fatal("fresh publication did not resolve ties")
	}
}

func TestChecklistOrganiserFactRows(t *testing.T) {
	w := newJudgingWorld(t, 0, 0)
	w.setStage(t, "draft")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	get := func() map[string]map[string]any {
		it, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		return it
	}
	// All mutable setup facts, including date-only same-day and timed-start/date-only-end.
	for _, start := range []string{"2026-11-07T00:00:00Z", "2026-11-07T12:00:00Z"} {
		exec(`UPDATE events SET title='Demo',tagline='Build',starts_at=$2,ends_at='2026-11-07T00:00:00Z',time_zone='UTC',about='Details',entry_rules_chosen=true WHERE slug=$1`, w.slug, start)
		if get()["o.basics"]["state"] != "done" {
			t.Fatal("date-only same day", start)
		}
	}
	if get()["o.times"]["state"] != "optional" {
		t.Fatal("date-only time recommendation")
	}
	exec(`UPDATE events SET ends_at='2026-11-07T18:00:00Z' WHERE slug=$1`, w.slug)
	if get()["o.times"]["state"] != "done" {
		t.Fatal("timed event")
	}
	doc := map[string]any{"schedule": []map[string]any{{"title": "Build", "start_at": "2026-11-07T12:00:00Z", "end_at": "2026-11-07T13:00:00Z"}}, "faq": []map[string]any{{"question": "When?", "answer": "Now"}}, "sponsors": []map[string]any{{"name": "Community"}}}
	raw, _ := json.Marshal(doc)
	r := w.call(t, "PUT", "/content", string(raw), w.org)
	if r.status != 200 {
		t.Fatalf("content %d %s", r.status, r.body)
	}
	for _, id := range []string{"o.entry-rules", "o.basics", "o.about", "o.times", "o.schedule", "o.faq", "o.sponsors", "o.rubric", "o.mode", "o.reminders"} {
		if get()[id]["state"] != "done" {
			t.Fatal(id, get()[id])
		}
	}
	exec(`UPDATE events SET reminder_offsets='{}',entry_rules_chosen=false,about='' WHERE slug=$1`, w.slug)
	for id, want := range map[string]string{"o.reminders": "optional", "o.entry-rules": "todo", "o.about": "todo"} {
		if get()[id]["state"] != want {
			t.Fatal(id)
		}
	}
	for _, bad := range []string{"bad/zone", "Mars"} {
		exec(`UPDATE events SET time_zone=$2 WHERE slug=$1`, w.slug, bad)
		if get()["o.basics"]["state"] != "todo" {
			t.Fatal("invalid zone")
		}
	}
	for _, mode := range []string{"open", "automatic", "manual", "panel"} {
		exec(`UPDATE events SET judge_assignment_mode=$2 WHERE slug=$1`, w.slug, mode)
		if get()["o.mode"]["state"] != "done" {
			t.Fatal(fmt.Sprint(mode))
		}
	}
}

func TestChecklistAssignmentModesAndOwnConflicts(t *testing.T) {
	w := newJudgingWorld(t, 1, 2)
	w.setStage(t, "judging")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p person, role string) map[string]map[string]any {
		it, _ := checklistItems(t, w.call(t, "GET", "/checklist?role="+role, nil, p))
		return it
	}
	first := read(w.judges[0].person, "judge")["j.scores"]
	if !strings.Contains(first["link"].(map[string]any)["href"].(string), "team="+w.teams[0].id) {
		t.Fatal("scoring link must use team ID", first)
	}
	// Another judge's declaration never completes my conflict review.
	exec(`INSERT INTO event_conflicts(event_id,judge_id,team_id) SELECT id,$2,$3 FROM events WHERE slug=$1`, w.slug, w.judges[1].id, w.teams[0].id)
	if read(w.judges[0].person, "judge")["j.conflicts"]["state"] != "todo" {
		t.Fatal("other judge's conflict leaked")
	}
	exec(`INSERT INTO event_conflicts(event_id,judge_id,team_id) SELECT id,$2,$3 FROM events WHERE slug=$1`, w.slug, w.judges[0].id, w.teams[0].id)
	own := read(w.judges[0].person, "judge")
	if own["j.conflicts"]["state"] != "done" || own["j.scores"]["state"] != "done" || own["j.scores"]["actionable"] != false {
		t.Fatal("all-conflicted queue", own)
	}
	exec(`DELETE FROM event_conflicts WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug)
	// Passing the advertised close time alone never completes j.closes or closes scoring.
	exec(`UPDATE events SET judging_ends_at=now()-interval '1 day' WHERE slug=$1`, w.slug)
	own = read(w.judges[0].person, "judge")
	if own["j.closes"]["state"] != "optional" || own["j.scores"]["actionable"] != true {
		t.Fatal("time alone closed scoring")
	}
	w.setStage(t, "closed")
	for _, mode := range []string{"automatic", "manual", "panel"} {
		t.Run(mode, func(t *testing.T) {
			exec(`UPDATE events SET judge_assignment_mode=$2 WHERE slug=$1`, w.slug, mode)
			if mode == "automatic" {
				item := read(w.org, "organiser")["o.assignments"]
				if item["state"] != "done" || item["detail"] != "Every team has at least 2 judges" {
					t.Fatal("automatic readiness follows prospective coverage", item)
				}
				return
			}
			if read(w.org, "organiser")["o.assignments"]["state"] != "todo" {
				t.Fatal("no assignments ready")
			}
			if mode == "panel" {
				exec(`INSERT INTO event_tracks(event_id,slug,name) SELECT id,'ideas','Ideas' FROM events WHERE slug=$1`, w.slug)
				for _, j := range w.judges {
					exec(`INSERT INTO event_track_panels(event_id,track_id,judge_id) SELECT event_id,id,$2 FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, j.id)
				}
				if read(w.org, "organiser")["o.assignments"]["state"] != "todo" {
					t.Fatal("missing team track ready")
				}
				exec(`UPDATE event_teams SET track_id=(SELECT id FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)) WHERE id=$2`, w.slug, w.teams[0].id)
			} else {
				for _, j := range w.judges {
					exec(`INSERT INTO event_assignments(event_id,judge_id,team_id) SELECT id,$2,$3 FROM events WHERE slug=$1 ON CONFLICT DO NOTHING`, w.slug, j.id, w.teams[0].id)
				}
			}
			if read(w.org, "organiser")["o.assignments"]["state"] != "done" {
				t.Fatal("covered team not ready")
			}
			exec(`INSERT INTO event_conflicts(event_id,judge_id,team_id) SELECT id,$2,$3 FROM events WHERE slug=$1`, w.slug, w.judges[0].id, w.teams[0].id)
			if read(w.org, "organiser")["o.assignments"]["state"] != "todo" {
				t.Fatal("conflicted assignment counted")
			}
			exec(`DELETE FROM event_conflicts WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug)
			exec(`DELETE FROM event_assignments WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug)
		})
	}
}

func TestChecklistPersonalRemovalPaths(t *testing.T) {
	for _, co := range []bool{false, true} {
		t.Run(fmt.Sprint("coorganiser=", co), func(t *testing.T) {
			w := newJudgingWorld(t, 0, 1)
			p := w.judges[0].person
			id := w.judges[0].id
			if co {
				if _, err := w.a.database.Exec(`UPDATE event_members SET role='organiser' WHERE user_id=$1`, id); err != nil {
					t.Fatal(err)
				}
			}
			if co {
				w.setStage(t, "closed")
				wantTS(t, "select organiser judges", w.call(t, "PATCH", "/judging/settings", map[string]any{"include_organisers": true}, w.org), 200, "")
			}
			r := w.call(t, "POST", "/checklist/acknowledgements/j.conflicts", map[string]any{}, p)
			checklistItems(t, r)
			route := "/people/"
			if co {
				route = "/organisers/"
			}
			r = w.call(t, "DELETE", route+id, nil, w.org)
			if r.status != 204 {
				t.Fatalf("remove %d %s", r.status, r.body)
			}
			if n := w.countRows(t, `SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`, id); n != 0 {
				t.Fatal("acks survived removal")
			}
		})
	}
}

func TestChecklistParticipantAdapterPreservesProgress(t *testing.T) {
	w := newJudgingWorld(t, 1, 0)
	w.setStage(t, "building")
	compare := func() {
		t.Helper()
		r := w.call(t, "GET", "", nil, w.part.person)
		view := r.json(t)
		items, c := checklistItems(t, r)
		progress := view["progress"].(map[string]any)
		for _, raw := range progress["steps"].([]any) {
			step := raw.(map[string]any)
			item := items["p."+step["id"].(string)]
			if (item["state"] == "done") != step["done"].(bool) {
				t.Fatal("progress changed", step, item)
			}
		}
		other := w.call(t, "GET", "/checklist", nil, w.part.person).json(t)["checklist"]
		a, _ := json.Marshal(c)
		b, _ := json.Marshal(other)
		if string(a) != string(b) {
			t.Fatalf("event/checklist mismatch\n%s\n%s", a, b)
		}
	}
	compare()
	if _, err := w.a.database.Exec(`UPDATE event_entries SET submitted_at=NULL,submit_settled_at=now() WHERE team_id=$1`, w.teams[0].id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.a.database.Exec(`UPDATE events SET submission_deadline=now()-interval '1 hour' WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}
	items, c := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.part.person))
	if items["p.submit"]["state"] != "todo" || items["p.submit"]["settled"] != true || c["next"] != nil {
		t.Fatal("settled incomplete", c)
	}
	// Published results hide participant actions even after a backwards stage move.
	if _, err := w.a.database.Exec(`UPDATE events SET results_published_at=now(),submission_deadline=NULL WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}
	_, c = checklistItems(t, w.call(t, "GET", "/checklist", nil, w.part.person))
	if c["next"] != nil {
		t.Fatal("published backwards action")
	}
}

func TestChecklistRemainingOrganiserFacts(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]map[string]any {
		it, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		return it
	}
	w.setStage(t, "draft")
	exec(`INSERT INTO event_tracks(event_id,slug,name) SELECT id,'track','Track' FROM events WHERE slug=$1`, w.slug)
	if read()["o.tracks"]["state"] != "done" {
		t.Fatal("track missing")
	}
	exec(`DELETE FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug)
	if read()["o.tracks"]["state"] != "optional" {
		t.Fatal("deleted track retained")
	}
	exec(`UPDATE events SET tie_criterion_id=(SELECT id FROM rubric_criteria WHERE event_id=events.id LIMIT 1),judging_ends_at=now() WHERE slug=$1`, w.slug)
	for _, id := range []string{"o.tie-break", "o.judging-closes"} {
		if read()[id]["state"] != "done" {
			t.Fatal(id)
		}
	}
	exec(`UPDATE events SET tie_criterion_id=NULL,judging_ends_at=NULL WHERE slug=$1`, w.slug)
	for _, id := range []string{"o.tie-break", "o.judging-closes"} {
		if read()[id]["state"] != "optional" {
			t.Fatal(id)
		}
	}
	w.setStage(t, "open")
	if read()["o.deadline"]["state"] != "todo" {
		t.Fatal("deadline inferred from end")
	}
	exec(`UPDATE events SET submission_deadline=now()-interval '1 day' WHERE slug=$1`, w.slug)
	if read()["o.deadline"]["state"] != "done" {
		t.Fatal("past deadline not set")
	}
	r := w.call(t, "POST", "/announcements", map[string]any{"title": "Welcome", "body": "Build", "email": false}, w.org)
	if r.status != 201 {
		t.Fatalf("announcement %d %s", r.status, r.body)
	}
	if read()["o.announcement"]["state"] != "done" {
		t.Fatal("saved announcement")
	}
	w.setStage(t, "closed")
	if read()["o.judges"]["state"] != "done" {
		t.Fatal("eligible judges")
	}
	w.setStage(t, "results")
	if read()["o.lock"]["state"] != "todo" {
		t.Fatal("unlocked")
	}
	exec(`UPDATE events SET judging_locked_at=now() WHERE slug=$1`, w.slug)
	if read()["o.lock"]["state"] != "done" {
		t.Fatal("lock")
	}
	w.setStage(t, "archived")
	if read()["o.end"]["state"] != "done" {
		t.Fatal("ended")
	}
}

type checklistDeletionTeams struct{}

func (checklistDeletionTeams) AdminTeamIDs(context.Context, *sql.Tx, string) ([]string, error) {
	return nil, nil
}
func (checklistDeletionTeams) FinishTeams(context.Context, *sql.Tx, []string) ([]string, error) {
	return nil, nil
}
func TestChecklistAccountDeletionClearsTextSubjects(t *testing.T) {
	w := newJudgingWorld(t, 0, 1)
	j := w.judges[0]
	checklistItems(t, w.call(t, "POST", "/checklist/acknowledgements/j.conflicts", map[string]any{}, j.person))
	_, err := accountapp.New(w.a.database).DeleteAccount(context.Background(), accountapp.DeleteAccountInput{ID: j.id, Operator: true, Teams: checklistDeletionTeams{}})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.countRows(t, `SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`, j.id); n != 0 {
		t.Fatal("text subject survived account deletion")
	}
}

func TestChecklistRemovedJudgesExcludedEntriesAndScores(t *testing.T) {
	w := newJudgingWorld(t, 1, 2)
	w.setStage(t, "judging")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		items, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		return items["o.scores"]
	}
	exec(`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT event_id,$2,$3,id,3 FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, w.judges[0].id, w.teams[0].id)
	if row := read(); row["state"] != "todo" || row["total"] != float64(2) {
		t.Fatal(row)
	}
	r := w.call(t, "DELETE", "/people/"+w.judges[1].id, nil, w.org)
	if r.status != 204 {
		t.Fatalf("remove %d %s", r.status, r.body)
	}
	if row := read(); row["state"] != "done" || row["total"] != float64(1) {
		t.Fatal("removed judge required", row)
	}
	exec(`UPDATE event_entries SET excluded_at=now() WHERE team_id=$1`, w.teams[0].id)
	if read()["state"] != "todo" {
		t.Fatal("no judgeable entries completed scores")
	}
	exec(`UPDATE event_entries SET excluded_at=NULL WHERE team_id=$1`, w.teams[0].id)
	if read()["state"] != "done" {
		t.Fatal("restored entry not counted")
	}
	exec(`DELETE FROM event_scores WHERE judge_id=$1 AND criterion_id=(SELECT id FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$2) LIMIT 1)`, w.judges[0].id, w.slug)
	if read()["state"] != "todo" {
		t.Fatal("deleted criterion score still complete")
	}
	// Leave records retained scores, but never a continuing scoring obligation.
	r = w.call(t, "POST", "/leave", map[string]any{"confirm": w.slug}, w.judges[0].person)
	if r.status != 200 {
		t.Fatalf("leave %d %s", r.status, r.body)
	}
	if row := read(); row["state"] != "todo" || row["total"] != float64(0) || row["detail"] != "No scores yet" {
		t.Fatal("former judge still required", row)
	}
}

func TestChecklistOrganiserObligationUsesCurrentAssignmentMode(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	w.call(t, "PATCH", "/judging/settings", map[string]any{"include_organisers": true}, w.org)
	w.setStage(t, "judging")
	orgID := w.a.userID(t, w.org)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := w.a.database.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	total := func() float64 {
		it, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		return it["o.scores"]["total"].(float64)
	}
	exec(`UPDATE events SET judge_assignment_mode='manual' WHERE slug=$1`, w.slug)
	for _, id := range []string{orgID, w.judges[0].id} {
		exec(`INSERT INTO event_assignments(event_id,judge_id,team_id) SELECT id,$2,$3 FROM events WHERE slug=$1`, w.slug, id, w.teams[0].id)
	}
	if total() != 2 {
		t.Fatal("manual organiser not required")
	}
	exec(`INSERT INTO event_tracks(event_id,slug,name) SELECT id,'track','Track' FROM events WHERE slug=$1`, w.slug)
	exec(`UPDATE event_teams SET track_id=(SELECT id FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)) WHERE id=$2`, w.slug, w.teams[0].id)
	exec(`INSERT INTO event_track_panels(event_id,track_id,judge_id) SELECT event_id,id,$2 FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, w.judges[0].id)
	exec(`UPDATE events SET judge_assignment_mode='panel' WHERE slug=$1`, w.slug)
	if total() != 1 {
		t.Fatal("stale manual organiser assignment counted in panel mode")
	}
	exec(`INSERT INTO event_track_panels(event_id,track_id,judge_id) SELECT event_id,id,$2 FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, orgID)
	if total() != 2 {
		t.Fatal("panel organiser not required")
	}
	exec(`DELETE FROM event_assignments WHERE judge_id=$1`, orgID)
	exec(`UPDATE events SET judge_assignment_mode='manual' WHERE slug=$1`, w.slug)
	if total() != 1 {
		t.Fatal("stale panel membership counted in manual mode")
	}
	exec(`UPDATE events SET judge_assignment_mode='open' WHERE slug=$1`, w.slug)
	if total() != 2 {
		t.Fatal("selected Open-mode organiser missing")
	}
	// Any stored score creates an organiser obligation, even if that score
	// currently does not count (for example, its entry was excluded).
	exec(`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT event_id,$2,$3,id,3 FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$1) LIMIT 1`, w.slug, orgID, w.teams[0].id)
	exec(`UPDATE event_entries SET excluded_at=now() WHERE team_id=$1`, w.teams[0].id)
	if total() != 0 {
		t.Fatal("judges with no eligible teams counted")
	}
	exec(`UPDATE event_entries SET excluded_at=NULL WHERE team_id=$1`, w.teams[0].id)
	if total() != 2 {
		t.Fatal("organiser who scored not required in Open mode")
	}
}

func TestChecklistTakenDownGuidance(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	w.setStage(t, "closed")
	if _, err := w.a.database.Exec(`UPDATE events SET taken_down_at=now(),taken_down_reason='test' WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}
	rows, c := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
	if c["next"] != nil {
		t.Fatal("taken-down next action", c["next"])
	}
	for id, row := range rows {
		if row["actionable"] != false || row["can_dismiss"] != false || row["blocked_reason"] != "event_taken_down" {
			t.Fatalf("%s: %v", id, row)
		}
	}
	if r := w.call(t, "POST", "/checklist/not-needed/o.faq", nil, w.org); r.status != 403 {
		t.Fatalf("takedown write: %d", r.status)
	}
}

func TestChecklistPartialTiePublication(t *testing.T) {
	w := newJudgingWorld(t, 4, 1)
	if r := w.call(t, "POST", "/stage", map[string]any{"stage": "judging"}, w.org); r.status != 200 {
		t.Fatalf("start judging: %d %s", r.status, r.body)
	}
	for i, team := range w.teams {
		points := 1 + i/2
		if _, err := w.a.database.Exec(`INSERT INTO event_scores(event_id,judge_id,team_id,criterion_id,points) SELECT event_id,$2,$3,id,$4 FROM rubric_criteria WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, w.judges[0].id, team.id, points); err != nil {
			t.Fatal(err)
		}
	}
	w.setStage(t, "results")
	if _, err := w.a.database.Exec(`UPDATE events SET judging_locked_at=now() WHERE slug=$1`, w.slug); err != nil {
		t.Fatal(err)
	}
	overrides := []map[string]any{{"team_id": w.teams[2].id, "rank": 1}, {"team_id": w.teams[3].id, "rank": 2}}
	for _, all := range []bool{false, true} {
		if all {
			overrides = append(overrides, map[string]any{"team_id": w.teams[0].id, "rank": 3}, map[string]any{"team_id": w.teams[1].id, "rank": 4})
		}
		r := w.call(t, "POST", "/results/publish", map[string]any{"rank_overrides": overrides}, w.org)
		if r.status != 200 {
			t.Fatalf("publish %d %s", r.status, r.body)
		}
		rows, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		if (rows["o.ties"]["state"] == "done") != all {
			t.Fatalf("all=%v: %v", all, rows["o.ties"])
		}
	}
}

func TestChecklistDisplayedRubricVersionRace(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	for _, tc := range []struct {
		id string
		p  person
	}{{"j.rubric", w.judges[0].person}} {
		shown := w.call(t, "GET", "/rubric", nil, tc.p).json(t)
		version, _ := shown["version"].(string)
		if version == "" {
			t.Fatal("missing displayed rubric version")
		}
		if _, err := w.a.database.Exec(`UPDATE rubric_criteria SET description=description || ' changed' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug); err != nil {
			t.Fatal(err)
		}
		r := w.call(t, "POST", "/checklist/acknowledgements/"+tc.id, map[string]string{"version": version}, tc.p)
		if r.status != 409 || r.json(t)["code"] != "stale_checklist_version" {
			t.Fatalf("stale render acknowledged: %d %s", r.status, r.body)
		}
		fresh := w.call(t, "GET", "/rubric", nil, tc.p).json(t)
		r = w.call(t, "POST", "/checklist/acknowledgements/"+tc.id, map[string]any{"version": fresh["version"]}, tc.p)
		if r.status != 200 {
			t.Fatalf("fresh render refused: %d %s", r.status, r.body)
		}
	}
}

func TestChecklistPublicPageURL(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	w.setStage(t, "building")
	for _, domain := range []string{"simple-hack.test", "hack.example.org"} {
		w.a.hack.siteDomain = domain
		link, ok := (checklistTargets{w.a.hack}).Resolve("o.public-page", w.slug, map[string]string{})
		if !ok || link.Href != w.a.hack.EventURL(w.slug) {
			t.Fatalf("public URL: %v", link)
		}
	}
}

func TestChecklistCreatedEventBasicsWithoutTagline(t *testing.T) {
	a := newHackApp(t)
	org := a.newPerson(t, "basics")
	slug := uniqueSlug()
	if r := a.createEvent(t, org, slug, map[string]any{"starts_at": "2026-11-11", "ends_at": "2026-11-12", "time_zone": "UTC"}); r.status != 201 {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	items, _ := checklistItems(t, a.at(t, "GET", "/v1/hack/events/"+slug+"/checklist", nil, a.key(org)))
	if items["o.basics"]["state"] != "done" {
		t.Fatalf("optional tagline blocked basics: %v", items["o.basics"])
	}
	if items["o.times"]["state"] != "optional" {
		t.Fatalf("date-only times rule changed: %v", items["o.times"])
	}
}

func TestChecklistJudgingClosesHiddenAfterJudging(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	for _, stage := range []string{"results", "archived"} {
		w.setStage(t, stage)
		items, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
		if _, ok := items["o.judging-closes"]; ok != (stage == "archived") {
			t.Fatalf("%s: judging closes visibility must preserve archived history", stage)
		}
	}
}

func TestJudgingOpenNoticeOnceWhileUndismissed(t *testing.T) {
	w := newJudgingWorld(t, 1, 2)
	// Key-only judges reproduce email_state=none, while the other judge
	// covers notices whose email has already been delivered.
	if _, err := w.a.database.Exec(`UPDATE users SET username='key-only-' || id::text WHERE id=$1`, w.judges[0].id); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"judging", "closed", "judging"} {
		r := w.call(t, "POST", "/stage", map[string]any{"stage": stage}, w.org)
		if r.status != 200 {
			t.Fatalf("stage %s: %d %s", stage, r.status, r.body)
		}
		if stage == "closed" {
			if _, err := w.a.database.Exec(`UPDATE event_notices SET email_state='sent' WHERE user_id=$1 AND kind='judging_open'`, w.judges[1].id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, j := range w.judges {
		var n int
		if err := w.a.database.QueryRow(`SELECT count(*) FROM event_notices n JOIN events e ON e.id=n.event_id WHERE e.slug=$1 AND n.user_id=$2 AND n.kind='judging_open' AND n.seen_at IS NULL`, w.slug, j.id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("undismissed notices: %d, %v", n, err)
		}
	}
}

func TestStageChangeDismissesOnlyTeamMessages(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	other := newJudgingWorldApp(t, w.a, 1, 1)
	insert := func(slug, kind string, seen bool) string {
		t.Helper()
		var id string
		err := w.a.database.QueryRow(`INSERT INTO event_notices(event_id,user_id,kind,seen_at) SELECT id,$2,$3,CASE WHEN $4 THEN now()-interval '1 day' END FROM events WHERE slug=$1 RETURNING id`, slug, w.part.id, kind, seen).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	message := insert(w.slug, "team_message", false)
	dismissed := insert(w.slug, "team_message", true)
	unrelated := insert(w.slug, "deadline_changed", false)
	otherMessage := insert(other.slug, "team_message", false)
	seen := func(id string) sql.NullTime {
		t.Helper()
		var at sql.NullTime
		if err := w.a.database.QueryRow(`SELECT seen_at FROM event_notices WHERE id=$1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	before := seen(dismissed)
	move := func(stage string) {
		t.Helper()
		r := w.call(t, "POST", "/stage", map[string]any{"stage": stage}, w.org)
		if r.status != 200 {
			t.Fatalf("stage: %d %s", r.status, r.body)
		}
	}
	move("closed")
	if seen(message).Valid {
		t.Fatal("same stage dismissed message")
	}
	move("judging")
	if !seen(message).Valid || seen(unrelated).Valid || seen(otherMessage).Valid || seen(dismissed) != before {
		t.Fatal("wrong notices dismissed")
	}
}

func TestChecklistJudgesShortUsesEligibleJudges(t *testing.T) {
	for _, mode := range []string{"automatic", "manual", "panel", "open"} {
		t.Run(mode, func(t *testing.T) {
			w := newJudgingWorld(t, 1, 2)
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := w.a.database.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`UPDATE events SET judge_assignment_mode=$2,judges_per_team=2 WHERE slug=$1`, w.slug, mode)
			if mode == "panel" {
				exec(`INSERT INTO event_tracks(event_id,slug,name) SELECT id,'ideas','Ideas' FROM events WHERE slug=$1`, w.slug)
				exec(`UPDATE event_teams SET track_id=(SELECT id FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)) WHERE id=$2`, w.slug, w.teams[0].id)
				for _, j := range w.judges {
					exec(`INSERT INTO event_track_panels(event_id,track_id,judge_id) SELECT event_id,id,$2 FROM event_tracks WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.slug, j.id)
				}
			} else if mode == "automatic" || mode == "manual" {
				for _, j := range w.judges {
					exec(`INSERT INTO event_assignments(event_id,judge_id,team_id) SELECT id,$2,$3 FROM events WHERE slug=$1`, w.slug, j.id, w.teams[0].id)
				}
			}
			decodeRubric(t, w.call(t, "PUT", "/rubric", rubricBody(criterion("Idea", "", 100, 5)), w.org))
			w.setStage(t, "judging")
			read := func() map[string]any {
				t.Helper()
				items, _ := checklistItems(t, w.call(t, "GET", "/checklist", nil, w.org))
				return items["o.judges-short"]
			}
			checkDashboard := func(eligible int) {
				t.Helper()
				r := w.call(t, "GET", "/judging/dashboard", nil, w.org)
				wantTS(t, "dashboard", r, 200, "")
				rows := r.json(t)["teams"].([]any)
				if len(rows) != 1 {
					t.Fatalf("dashboard teams: %v", rows)
				}
				row := rows[0].(map[string]any)
				if row["judges_eligible"] != float64(eligible) || row["judges_scored"] != float64(0) || row["flagged"] != true {
					t.Fatalf("dashboard coverage: %v", row)
				}
			}
			if row := read(); row != nil {
				t.Fatal("fully eligible team with no scores is short", row)
			}
			checkDashboard(2)
			decodeConflict(t, w.call(t, "POST", "/conflicts", map[string]any{"team_id": w.teams[0].id}, w.judges[0].person))
			checkDashboard(1)
			row := read()
			if mode == "open" {
				if row != nil {
					t.Fatal("open mode has no target warning", row)
				}
			} else if row["count"] != float64(1) || row["optional"] != true {
				t.Fatal("conflict must make the team short", row)
			}
		})
	}
}
