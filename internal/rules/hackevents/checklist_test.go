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
					out := BuildChecklist(ChecklistInput{Role: role, Stage: d.Stage, MembershipRole: role, AssignmentMode: "manual", Facts: map[string]ChecklistFact{d.ID: {Done: done}}, Marks: map[string]bool{d.ID: mark}}, time.Time{})
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
	base := model.Event{Title: "Demo", Tagline: "Build", TimeZone: "Asia/Jakarta"}
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
