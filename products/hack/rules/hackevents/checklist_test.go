package hackevents

import (
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"strings"
	"testing"
	"time"
)

func TestChecklistCatalogueStates(t *testing.T) {
	for _, d := range ChecklistCatalogue() {
		t.Run(d.ID, func(t *testing.T) {
			role := map[byte]string{'o': "organiser", 'j': "judge", 'p': "participant"}[d.ID[0]]
			for _, done := range []bool{false, true} {
				for _, mark := range []bool{false, true} {
					out := BuildChecklist(ChecklistInput{Role: role, Stage: d.Stage, MembershipRole: role, AssignmentMode: "manual", Facts: map[string]ChecklistFact{d.ID: {Done: done, Count: intTestPointer(1)}}, Marks: map[string]bool{d.ID: mark}}, time.Time{})
					found := false
					for _, it := range out.Items {
						if it.ID != d.ID {
							continue
						}
						found = true
						want := "todo"
						if done {
							want = "done"
						} else if d.Optional {
							want = "optional"
							if mark && role == "organiser" {
								want = "not_needed"
							}
						}
						if it.State != want {
							t.Fatalf("done=%v mark=%v: %s want %s", done, mark, it.State, want)
						}
						if it.CanDismiss != (!done && role == "organiser" && d.Optional) {
							t.Fatal("dismiss authority")
						}
					}
					if !found {
						t.Fatal("missing row")
					}
				}
			}
		})
	}
}
func TestChecklistCarryPriorityAndHistory(t *testing.T) {
	facts := map[string]ChecklistFact{}
	for _, d := range ChecklistCatalogue() {
		facts[d.ID] = ChecklistFact{Done: true}
	}
	facts["o.entry-rules"] = ChecklistFact{}
	facts["o.deadline"] = ChecklistFact{}
	facts["o.scores"] = ChecklistFact{}
	facts["o.faq"] = ChecklistFact{}
	in := ChecklistInput{Role: "organiser", MembershipRole: "organiser", Stage: "results", AssignmentMode: "open", Facts: facts, Forward: &ChecklistNext{Kind: "end_event", Cue: "due"}}
	out := BuildChecklist(in, time.Time{})
	if out.Next.ItemID != "o.entry-rules" || out.Next.Cue != "due" {
		t.Fatalf("next %+v", out.Next)
	}
	seen := map[string]bool{}
	for _, it := range out.Items {
		if seen[it.ID] {
			t.Fatal("duplicate", it.ID)
		}
		seen[it.ID] = true
		if it.ID == "o.assignments" {
			t.Fatal("open assignments")
		}
		if it.ID == "o.faq" && it.Group != "earlier_optional" {
			t.Fatal(it)
		}
	}
	facts["o.entry-rules"] = ChecklistFact{BlockedReason: "entry_rules_closed"}
	out = BuildChecklist(in, time.Time{})
	if out.Next.ItemID != "o.deadline" {
		t.Fatal(out.Next)
	}
	for _, it := range out.Items {
		if it.ID == "o.entry-rules" {
			t.Fatal("blocked earlier required row must be hidden", it)
		}
		if it.Group == "earlier_required" && (!it.Actionable || it.State == "done") {
			t.Fatal("only actionable unfinished rows belong above current", it)
		}
	}
	in.Stage = "archived"
	out = BuildChecklist(in, time.Time{})
	if out.Next != nil {
		t.Fatal(out.Next)
	}
	for _, it := range out.Items {
		if it.Actionable || it.CanDismiss {
			t.Fatal(it)
		}
	}
	in.Stage = "draft"
	out = BuildChecklist(in, time.Time{})
	for _, it := range out.Items {
		if !strings.HasPrefix(it.ID, "o.") || it.OriginStage != "draft" {
			t.Fatal(it)
		}
	}
}
func TestChecklistFingerprint(t *testing.T) {
	r := []model.RubricCriterion{{ID: "a", Name: "Idea", Weight: 100, MaxPoints: 5}}
	v := RubricFingerprint(r)
	if v == "" || RubricFingerprint(nil) != "" {
		t.Fatal(v)
	}
	for _, change := range []func(*model.RubricCriterion){func(c *model.RubricCriterion) { c.ID = "b" }, func(c *model.RubricCriterion) { c.Name = "Build" }, func(c *model.RubricCriterion) { c.Description = "new" }, func(c *model.RubricCriterion) { c.Weight = 50 }, func(c *model.RubricCriterion) { c.MaxPoints = 10 }} {
		copy := append([]model.RubricCriterion{}, r...)
		change(&copy[0])
		if RubricFingerprint(copy) == v {
			t.Fatal("unchanged fingerprint")
		}
	}
}

func TestChecklistBasicsDateBoundaries(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	start := time.Date(2026, 11, 7, 0, 0, 0, 0, loc)
	base := model.Event{Title: "Demo", TimeZone: "Asia/Jakarta"}
	for _, tt := range []struct {
		name       string
		start, end time.Time
		want       bool
	}{
		{"same date", start, start, true},
		{"timed start date end", start.Add(12 * time.Hour), start, true},
		{"date boundary equality", start.AddDate(0, 0, 1), start, true},
		{"after date end", start.AddDate(0, 0, 1).Add(time.Second), start, false},
		{"timed equality", start.Add(time.Hour), start.Add(time.Hour), true},
		{"invalid order", start.Add(2 * time.Hour), start.Add(time.Hour), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ev := base
			ev.StartsAt = sql.NullTime{Time: tt.start, Valid: true}
			ev.EndsAt = sql.NullTime{Time: tt.end, Valid: true}
			if ChecklistBasicsReady(ev) != tt.want {
				t.Fatal(ev)
			}
		})
	}
	if ChecklistBasicsReady(base) {
		t.Fatal("missing dates")
	}
	base.StartsAt = sql.NullTime{Time: start, Valid: true}
	base.EndsAt = base.StartsAt
	base.TimeZone = "invalid/zone"
	if ChecklistBasicsReady(base) {
		t.Fatal("invalid zone")
	}
}

func TestChecklistNextActions(t *testing.T) {
	facts := map[string]ChecklistFact{}
	for _, d := range ChecklistCatalogue() {
		facts[d.ID] = ChecklistFact{Done: true}
	}
	for _, tt := range []struct{ stage, id, kind, to string }{{"judging", "o.lock", "stage", "results"}, {"results", "o.lock", "link", ""}, {"results", "o.publish", "publish_results", ""}, {"results", "o.end", "end_event", "archived"}} {
		t.Run(tt.stage+tt.id, func(t *testing.T) {
			copy := map[string]ChecklistFact{}
			for k, v := range facts {
				copy[k] = v
			}
			copy[tt.id] = ChecklistFact{}
			out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: tt.stage, AssignmentMode: "open", Facts: copy}, time.Time{})
			if out.Next == nil || out.Next.Kind != tt.kind || out.Next.Stage != tt.to {
				t.Fatal(out.Next)
			}
		})
	}
	out := BuildChecklist(ChecklistInput{Role: "judge", MembershipRole: "organiser", Stage: "closed", Facts: facts}, time.Time{})
	for _, it := range out.Items {
		if it.ID == "j.invite" || it.ID == "j.coc" {
			t.Fatal(it)
		}
	}
	out = BuildChecklist(ChecklistInput{Role: "organiser", Stage: "draft", ReadOnly: true}, time.Time{})
	if out.Next != nil {
		t.Fatal(out.Next)
	}
	for _, it := range out.Items {
		if it.Actionable || it.CanDismiss {
			t.Fatal(it)
		}
	}
}

func TestChecklistCatalogueTargetsInNavigation(t *testing.T) {
	for _, item := range ChecklistCatalogue() {
		if _, err := Resolve(item.Target, "demo", map[string]string{"team": "demo"}); err != nil {
			t.Errorf("%s: %v", item.ID, err)
		}
	}
}

func TestChecklistAssignmentsFollowMode(t *testing.T) {
	for _, mode := range []string{"open", "automatic", "manual", "panel"} {
		t.Run(mode, func(t *testing.T) {
			out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: "closed", AssignmentMode: mode, JudgesPerTeam: 2}, time.Time{})
			for _, item := range out.Items {
				if item.ID != "o.assignments" {
					continue
				}
				if mode == "open" {
					t.Fatal("Open must omit assignments")
				}
				label, target := "Assign judges", "o.assignments"
				if mode == "automatic" {
					label, target = "Enough judges for 2 per team", "o.judges"
				}
				if item.Label != label || item.TargetID != target {
					t.Fatalf("got %+v; want %q targeting %s", item, label, target)
				}
				return
			}
			if mode != "open" {
				t.Fatal("missing assignments")
			}
		})
	}
}

func TestChecklistBlockedEarlierOrganiserItems(t *testing.T) {
	for _, stage := range []string{"judging", "results", "archived"} {
		for _, done := range []bool{false, true} {
			out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: stage, Facts: map[string]ChecklistFact{"o.tie-break": {Done: done, BlockedReason: "tie_rule_closed"}}}, time.Time{})
			found := false
			for _, item := range out.Items {
				found = found || item.ID == "o.tie-break"
			}
			// Completed earlier optional tasks remain visible; unfinished
			// blocked tasks have nothing left for the organiser to do.
			if found != (done || stage == "archived") {
				t.Fatalf("stage=%s done=%v found=%v", stage, done, found)
			}
		}
	}
	out := BuildChecklist(ChecklistInput{Role: "judge", Stage: "judging", Facts: map[string]ChecklistFact{"j.rubric": {BlockedReason: "no_rubric"}}}, time.Time{})
	for _, item := range out.Items {
		if item.ID == "j.rubric" {
			return
		}
	}
	t.Fatal("judge's blocked rubric was hidden")
}

func intTestPointer(n int) *int { return &n }

func TestChecklistDeadlineDoesNotGateBuilding(t *testing.T) {
	facts := map[string]ChecklistFact{}
	for _, d := range ChecklistCatalogue() {
		facts[d.ID] = ChecklistFact{Done: true}
	}
	facts["o.deadline"] = ChecklistFact{}
	forward := &ChecklistNext{Kind: "stage", Stage: "building", Label: "Start building"}
	out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: "open", Facts: facts, Forward: forward}, time.Time{})
	if out.Next != forward {
		t.Fatalf("next: %+v", out.Next)
	}
	for _, it := range out.Items {
		if it.ID == "o.deadline" {
			if it.State != "todo" || !it.Actionable {
				t.Fatal(it)
			}
			return
		}
	}
	t.Fatal("missing deadline")
}

func TestChecklistDeadlineDoesNotGateClosing(t *testing.T) {
	facts := map[string]ChecklistFact{}
	for _, d := range ChecklistCatalogue() {
		facts[d.ID] = ChecklistFact{Done: true}
	}
	facts["o.deadline"] = ChecklistFact{}
	forward := &ChecklistNext{Kind: "stage", Stage: "closed", Label: "Close entries"}
	out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: "building", Facts: facts, Forward: forward}, time.Time{})
	if out.Next != forward {
		t.Fatalf("next: %+v", out.Next)
	}
	for _, it := range out.Items {
		if it.ID == "o.deadline" {
			if it.State != "todo" || !it.Actionable {
				t.Fatal(it)
			}
			return
		}
	}
	t.Fatal("missing deadline")
}

func TestChecklistJudgesShortOptional(t *testing.T) {
	for _, n := range []int{0, 1, 3} {
		facts := map[string]ChecklistFact{}
		for _, d := range ChecklistCatalogue() {
			facts[d.ID] = ChecklistFact{Done: true}
		}
		facts["o.judges-short"] = ChecklistFact{Count: &n}
		facts["o.lock"] = ChecklistFact{}
		out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: "judging", Facts: facts}, time.Time{})
		if out.Next == nil || out.Next.ItemID != "o.lock" {
			t.Fatal(out.Next)
		}
		found := false
		for _, it := range out.Items {
			if it.ID == "o.judges-short" {
				found = true
				want := "3 teams have fewer judges than the target"
				if n == 1 {
					want = "1 team has fewer judges than the target"
				}
				if !it.Optional || it.TargetID != "o.scores" || it.Label != want {
					t.Fatal(it)
				}
			}
		}
		if found != (n > 0) {
			t.Fatalf("count=%d found=%v", n, found)
		}
	}
}

func TestChecklistClosedNextStartsJudging(t *testing.T) {
	facts := map[string]ChecklistFact{}
	for _, d := range ChecklistCatalogue() {
		facts[d.ID] = ChecklistFact{Done: true}
	}
	forward := &ChecklistNext{Kind: "stage", Stage: "judging", Label: "Start judging"}
	out := BuildChecklist(ChecklistInput{Role: "organiser", Stage: "closed", AssignmentMode: "manual", Facts: facts, Forward: forward}, time.Time{})
	if out.Next != forward {
		t.Fatalf("next: %+v; want Start judging", out.Next)
	}
	for _, role := range []string{"organiser", "judge", "participant"} {
		for _, stage := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
			out := BuildChecklist(ChecklistInput{Role: role, Stage: stage, Facts: facts}, time.Time{})
			for _, item := range out.Items {
				if item.ID == "o.rubric-ready" {
					t.Fatalf("removed item in %s at %s", role, stage)
				}
			}
		}
	}
}
