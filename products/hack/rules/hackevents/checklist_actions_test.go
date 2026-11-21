package hackevents

import (
	"testing"
	"time"
)

// Every actionable checklist row links somewhere real; blocked rows, archived
// events and informational rows never offer an action.
func TestChecklistActionsResolve(t *testing.T) {
	informational := map[string]bool{"o.scores": true, "j.invite": true, "j.coc": true, "j.closes": true}
	for _, role := range []string{"organiser", "judge", "participant"} {
		for _, stage := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
			for _, mode := range []string{"open", "panel"} {
				for _, state := range []string{"missing", "blocked"} {
					facts := map[string]ChecklistFact{}
					for _, d := range ChecklistCatalogue() {
						f := ChecklistFact{Count: intTestPointer(1)}
						if state == "blocked" {
							f.BlockedReason = "unavailable"
						}
						facts[d.ID] = f
					}
					list := BuildChecklist(ChecklistInput{Role: role, Stage: stage, AssignmentMode: mode, JudgesPerTeam: 2, Facts: facts}, time.Time{})
					for _, it := range list.Items {
						if !it.Actionable {
							continue
						}
						if informational[it.ID] || state == "blocked" || stage == "archived" {
							t.Fatalf("%s/%s/%s/%s: %s is actionable", role, stage, mode, state, it.ID)
						}
						if _, err := Resolve(it.TargetID, "test", map[string]string{"team": "team"}); err != nil {
							t.Fatalf("%s: %v", it.ID, err)
						}
					}
				}
			}
		}
	}
}

func TestDoneEditableChecklistItemsKeepActions(t *testing.T) {
	facts := map[string]ChecklistFact{}
	for _, id := range []string{"o.rubric", "o.mode", "o.reminders"} {
		facts[id] = ChecklistFact{Done: true}
	}
	for _, locked := range []bool{false, true} {
		if locked {
			for id, f := range facts {
				f.BlockedReason = "scores_locked"
				facts[id] = f
			}
		}
		list := BuildChecklist(ChecklistInput{Role: "organiser", Stage: "draft", Facts: facts}, time.Time{})
		for _, it := range list.Items {
			if _, ok := facts[it.ID]; ok && it.Actionable == locked {
				t.Fatalf("locked=%v: %s actionable=%v", locked, it.ID, it.Actionable)
			}
		}
	}
}

// Checklist/ChecklistNext expose no global disabled/read-only flag. A nil Next
// can remove the suggested action, so organisers must retain one before archive.
// ConfirmationRequired opens a dialog and DeadlinePassed changes its copy; only
// Next.BlockedReason disables the action (organiser-overview.js).
func TestChecklistNeverBlocks(t *testing.T) {
	stages := []string{"draft", "open", "building", "closed", "judging", "results", "archived"}
	for _, role := range []string{"organiser", "judge", "participant"} {
		for i, stage := range stages {
			for _, mode := range []string{"open", "panel"} {
				for _, state := range []string{"missing", "blocked", "dismissed"} {
					reasons := []string{""}
					if stage == "results" {
						reasons = append(reasons, "archive_needs_participants")
					}
					for _, reason := range reasons {
						t.Run(role+"/"+stage+"/"+mode+"/"+state+"/"+reason, func(t *testing.T) {
							facts, marks := map[string]ChecklistFact{}, map[string]bool{}
							for _, d := range ChecklistCatalogue() {
								f := ChecklistFact{Done: state == "done", Count: intTestPointer(1)}
								if state == "blocked" {
									f.BlockedReason = "checklist_item_unavailable"
								}
								facts[d.ID] = f
								marks[d.ID] = state == "dismissed" && d.Optional
							}
							var forward *ChecklistNext
							if i+1 < len(stages) {
								forward = &ChecklistNext{Kind: "stage", Stage: stages[i+1], Label: "Forward", BlockedReason: reason, ConfirmationRequired: true, DeadlinePassed: true}
							}
							out := BuildChecklist(ChecklistInput{Role: role, Stage: stage, AssignmentMode: mode, Facts: facts, Marks: marks, Forward: forward, ParticipantNext: "summary"}, time.Time{})
							if role == "organiser" && forward != nil && out.Next == nil {
								t.Fatal("checklist removed the suggested action")
							}
							if out.Next != nil {
								if out.Next.Kind == "" || out.Next.Kind == "none" {
									t.Fatal("checklist suppressed the suggested control")
								}
								blocked := out.Next.BlockedReason
								if blocked != nil && blocked != "" && blocked != reason {
									t.Fatalf("checklist introduced a block: %v (forward: %q)", blocked, reason)
								}
							}
							if forward != nil && forward.BlockedReason != reason {
								t.Fatal("checklist mutated the server block")
							}
						})
					}
				}
			}
		}
	}
}
