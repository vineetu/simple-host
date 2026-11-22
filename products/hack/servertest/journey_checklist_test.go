package servertest

import (
	"testing"

	"github.com/vsriram/simple-host/products/hack/hacktest"
)

func TestChecklistAcknowledgementsAndCleanup(t *testing.T) {
	w := newFeatJudgingWorld(t, 1, 2)
	read := func(p hacktest.Person, role string) map[string]map[string]any {
		rows, _ := checklistItems(t, w.Call(t, "GET", "/checklist?role="+role, nil, p))
		return rows
	}
	ack := func(p hacktest.Person, id, version string) hacktest.Resp {
		return w.Call(t, "POST", "/checklist/acknowledgements/"+id, map[string]string{"version": version}, p)
	}
	j := w.Judges[0].Person
	items := read(j, "judge")
	v := items["j.rubric"]["version"].(string)
	checklistItems(t, ack(j, "j.rubric", v))
	if read(j, "judge")["j.rubric"]["state"] != "done" || read(w.Judges[1].Person, "judge")["j.rubric"]["state"] != "todo" {
		t.Fatal("own-only")
	}
	checklistItems(t, ack(j, "j.conflicts", ""))
	if read(j, "judge")["j.conflicts"]["state"] != "done" {
		t.Fatal("conflicts ack")
	}
	r := ack(w.Org, "o.rubric-ready", v)
	if r.Status != 400 || r.JSON(t)["code"] != "invalid_checklist_item" {
		t.Fatalf("removed acknowledgement accepted: %d %s", r.Status, r.Body)
	}
	if _, err := w.a.DB.Exec(`UPDATE rubric_criteria SET description='Changed' WHERE event_id=(SELECT id FROM events WHERE slug=$1)`, w.Slug); err != nil {
		t.Fatal(err)
	}
	if ack(j, "j.rubric", v).Status != 409 {
		t.Fatal("stale version accepted")
	}
	if read(j, "judge")["j.rubric"]["state"] != "todo" {
		t.Fatal("judge rubric read remained done after the rubric changed")
	}
	if read(j, "judge")["j.conflicts"]["state"] != "done" {
		t.Fatal("conflicts versioned")
	}
	if ack(w.Part.Person, "j.rubric", v).Status != 403 {
		t.Fatal("participant acknowledgement")
	}
	if ack(j, "j.coc", "").Status != 400 {
		t.Fatal("conduct fabricated")
	}
	// Shared deletion path: leaving, organiser removal and person removal all call it.
	r = w.Call(t, "POST", "/leave", map[string]any{"confirm": w.Slug}, j)
	if r.Status != 200 {
		t.Fatalf("leave %d %s", r.Status, r.Body)
	}
	if n := w.CountRows(t, `SELECT count(*) FROM event_checklist_acknowledgements WHERE subject_id=$1`, w.Judges[0].ID); n != 0 {
		t.Fatal("personal acks survived leave", n)
	}
	ev := w.Call(t, "GET", "", nil, w.Org).JSON(t)
	code := ev["organiser"].(map[string]any)["judge_code"].(string)
	r = w.a.At(t, "POST", "/v1/hack/judge/"+code, map[string]any{"accept_coc": true}, w.a.Key(j))
	if r.Status != 200 {
		t.Fatalf("rejoin %d %s", r.Status, r.Body)
	}
	if read(j, "judge")["j.conflicts"]["state"] != "todo" {
		t.Fatal("reused rejoin ack")
	}
	w.SetStage(t, "archived")
	if ack(j, "j.conflicts", "").Status != 409 {
		t.Fatal("archived ack")
	}
}

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
