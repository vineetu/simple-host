package judging

import (
	"reflect"
	"testing"
)

func TestOrganiserJudgeSelectionAndAlias(t *testing.T) {
	w := newJudgingWorld(t, 2, 1)
	orgID := w.a.UserID(t, w.Org)
	settings := func(body map[string]any) map[string]any {
		t.Helper()
		r := w.Call(t, "PATCH", "/judging/settings", body, w.Org)
		if r.Status != 200 {
			t.Fatalf("settings: %d %s", r.Status, r.Body)
		}
		return r.JSON(t)
	}
	selected := func(want int) {
		t.Helper()
		r := w.Call(t, "GET", "/judging/settings", nil, w.Org).JSON(t)
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
	if r := w.Call(t, "PATCH", "/judging/settings", map[string]any{"organiser_judges": []string{w.Judges[0].ID}}, w.Org); r.Status < 400 {
		t.Fatal("accepted non-organiser")
	}
	settings(map[string]any{"assignment_mode": "automatic", "include_organisers": true})
	for _, body := range []map[string]any{nil, {"include_organisers": false}, {"include_organisers": true}} {
		r := w.Call(t, "POST", "/assignments/generate", body, w.Org)
		if r.Status != 200 {
			t.Fatalf("generation: %d %s", r.Status, r.Body)
		}
		want := 1
		if body != nil && body["include_organisers"] == false {
			want = 0
		}
		selected(want)
	}
	// Preview is read-only and uses the same judge set and conflicts as generation.
	before := w.Call(t, "GET", "/assignments", nil, w.Org).JSON(t)
	dash := w.Call(t, "GET", "/judging/dashboard", nil, w.Org).JSON(t)
	if len(dash["coverage"].(map[string]any)["short"].([]any)) != 0 {
		t.Fatalf("selected organiser missing from coverage: %v", dash)
	}
	after := w.Call(t, "GET", "/assignments", nil, w.Org).JSON(t)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("coverage persisted assignments")
	}
	settings(map[string]any{"include_organisers": false})
	dash = w.Call(t, "GET", "/judging/dashboard", nil, w.Org).JSON(t)
	if len(dash["coverage"].(map[string]any)["short"].([]any)) != 2 {
		t.Fatal("coverage failed to report shortage")
	}
}
