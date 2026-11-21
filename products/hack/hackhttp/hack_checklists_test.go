package hackhttp

import (
	"context"
	"database/sql"
	"testing"

	accountapp "github.com/vsriram/simple-host/host/access"
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
	w.addOrganiser(t, w.a.key(org2))
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
	w.addOrganiser(t, w.a.adminH())
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

func TestChecklistAccountDeletionClearsTextSubjects(t *testing.T) {
	w := newJudgingWorld(t, 0, 1)
	j := w.judges[0]
	checklistItems(t, w.call(t, "POST", "/checklist/acknowledgements/j.conflicts", map[string]any{}, j.person))
	_, err := accountapp.New(w.a.database).DeleteAccount(context.Background(), accountapp.DeleteAccountInput{ID: j.id, Operator: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.countRows(t, `SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`, j.id); n != 0 {
		t.Fatal("text subject survived account deletion")
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
