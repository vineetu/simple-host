package checklist

import (
	"context"
	"database/sql"
	"testing"

	hacktest "github.com/vsriram/simple-host/products/hack/hacktest"

	"github.com/vsriram/simple-host/host/ops"

	accountapp "github.com/vsriram/simple-host/host/access"
)

func checklistItems(t *testing.T, r hacktest.Resp) (map[string]map[string]any, map[string]any) {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("checklist: %d %s", r.Status, r.Body)
	}
	can, ok := r.JSON(t)["can"].(map[string]any)
	if !ok || len(can) != 3 {
		t.Fatalf("missing abilities: %s", r.Body)
	}
	c, ok := r.JSON(t)["checklist"].(map[string]any)
	if !ok {
		t.Fatalf("missing checklist: %s", r.Body)
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
	w.SetStage(t, "draft")
	sql := func(query string, args ...any) {
		t.Helper()
		if _, err := w.a.DB.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	org2 := w.a.NewPerson(t, "ck2")
	w.AddOrganiser(t, w.a.Key(org2))
	get := func(p hacktest.Person) map[string]map[string]any {
		rows, _ := checklistItems(t, w.Call(t, "GET", "/checklist", nil, p))
		return rows
	}
	mark := func(method, item string, p hacktest.Person) hacktest.Resp {
		return w.Call(t, method, "/checklist/not-needed/"+item, nil, p)
	}
	for _, tc := range []struct {
		p                       hacktest.Person
		manage, judge, takePart bool
	}{{w.Org, true, false, false}, {w.Judges[0].Person, false, true, false}, {w.Part.Person, false, false, true}} {
		can := w.Call(t, "GET", "/checklist", nil, tc.p).JSON(t)["can"].(map[string]any)
		if can["manage"] != tc.manage || can["judge"] != tc.judge || can["take_part"] != tc.takePart {
			t.Fatalf("membership abilities: %v", can)
		}
	}
	if get(w.Org)["o.faq"]["state"] != "optional" {
		t.Fatal("initial FAQ")
	}
	checklistItems(t, mark("POST", "o.faq", w.Org))
	if get(org2)["o.faq"]["state"] != "not_needed" {
		t.Fatal("not shared")
	}
	sql(`INSERT INTO event_content(event_id,faq) SELECT id,'[{"question":"When?","answer":"Today"}]'::jsonb FROM events WHERE slug=$1 ON CONFLICT(event_id) DO UPDATE SET faq=excluded.faq`, w.Slug)
	if get(w.Org)["o.faq"]["state"] != "done" {
		t.Fatal("facts must win")
	}
	sql(`UPDATE event_content SET faq='[]' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.Slug)
	if get(w.Org)["o.faq"]["state"] != "not_needed" {
		t.Fatal("mark must reappear")
	}
	checklistItems(t, mark("DELETE", "o.faq", org2))
	if get(w.Org)["o.faq"]["state"] != "optional" {
		t.Fatal("restore")
	}
	for _, id := range []string{"o.basics", "made-up", "j.closes"} {
		r := mark("POST", id, w.Org)
		if r.Status != 400 {
			t.Fatalf("%s %d %s", id, r.Status, r.Body)
		}
	}
	for _, p := range []hacktest.Person{w.Part.Person, w.Judges[0].Person} {
		r := mark("POST", "o.faq", p)
		if r.Status != 403 {
			t.Fatalf("wrong role %d %s", r.Status, r.Body)
		}
	}
	r := w.a.At(t, "POST", w.Path("/checklist/not-needed/o.faq"), nil, w.a.AdminH())
	if r.Status != 404 {
		t.Fatalf("synthetic admin write %d", r.Status)
	}
	r = w.a.At(t, "GET", w.Path("/checklist"), nil, w.a.AdminH())
	items, c := checklistItems(t, r)
	if c["next"] != nil || items["o.faq"]["can_dismiss"] != false {
		t.Fatal("admin read only")
	}
	if can := r.JSON(t)["can"].(map[string]any); can["manage"] != false || can["judge"] != false || can["take_part"] != false {
		t.Fatalf("inspection abilities: %v", can)
	}
	// Actual organiser membership wins over the platform-admin status.
	w.AddOrganiser(t, w.a.AdminH())
	checklistItems(t, w.a.At(t, "POST", w.Path("/checklist/not-needed/o.faq"), nil, w.a.AdminH()))
	w.SetStage(t, "archived")
	if mark("POST", "o.faq", w.Org).Status != 409 {
		t.Fatal("archived mark")
	}
}

func TestChecklistAccountDeletionClearsTextSubjects(t *testing.T) {
	w := newJudgingWorld(t, 0, 1)
	j := w.Judges[0]
	checklistItems(t, w.Call(t, "POST", "/checklist/acknowledgements/j.conflicts", map[string]any{}, j.Person))
	_, err := ops.Cutoff{DB: w.a.DB}.ErasePerson(context.Background(), accountapp.DeleteAccountInput{ID: j.ID, Operator: true})
	if err != nil {
		t.Fatal(err)
	}
	if n := w.CountRows(t, `SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`, j.ID); n != 0 {
		t.Fatal("text subject survived account deletion")
	}
}

func TestStageChangeDismissesOnlyTeamMessages(t *testing.T) {
	w := newJudgingWorld(t, 1, 1)
	other := newJudgingWorldApp(t, w.a, 1, 1)
	insert := func(slug, kind string, seen bool) string {
		t.Helper()
		var id string
		err := w.a.DB.QueryRow(`INSERT INTO event_notices(event_id,user_id,kind,seen_at) SELECT id,$2,$3,CASE WHEN $4 THEN now()-interval '1 day' END FROM events WHERE slug=$1 RETURNING id`, slug, w.Part.ID, kind, seen).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	message := insert(w.Slug, "team_message", false)
	dismissed := insert(w.Slug, "team_message", true)
	unrelated := insert(w.Slug, "deadline_changed", false)
	otherMessage := insert(other.Slug, "team_message", false)
	seen := func(id string) sql.NullTime {
		t.Helper()
		var at sql.NullTime
		if err := w.a.DB.QueryRow(`SELECT seen_at FROM event_notices WHERE id=$1`, id).Scan(&at); err != nil {
			t.Fatal(err)
		}
		return at
	}
	before := seen(dismissed)
	move := func(stage string) {
		t.Helper()
		r := w.A.SetStage(t, w.Slug, stage, w.A.Key(w.Org))
		if r.Status != 200 {
			t.Fatalf("stage: %d %s", r.Status, r.Body)
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
