package hackevents

import (
	"testing"
	"time"
)

// This map names the screen/anchor with the actual control, independently of navigation.json.
// Browser coverage verifies those controls are visible and enabled.
func TestEveryChecklistActionHasItsFix(t *testing.T) {
	type fix struct{ screen, anchor, tab string }
	allowed := map[string][]fix{
		"o.entry-rules":    {{"organiser-settings", "entry-rules", "entries"}},
		"o.basics":         {{"organiser-event-page", "", "basics"}},
		"o.times":          {{"organiser-event-page", "f-start-time", "basics"}},
		"o.about":          {{"organiser-event-page", "", "about"}},
		"o.schedule":       {{"organiser-event-page", "", "schedule"}},
		"o.faq":            {{"organiser-event-page", "", "faq"}},
		"o.sponsors":       {{"organiser-event-page", "", "sponsors"}},
		"o.tracks":         {{"organiser-event-page", "", "tracks"}},
		"o.rubric":         {{"organiser-settings", "rubric", "judging"}},
		"o.mode":           {{"organiser-settings", "mode", "judging"}},
		"o.tie-break":      {{"organiser-settings", "tie-break", "judging"}, {"organiser-settings", "rubric", "judging"}},
		"o.judging-closes": {{"organiser-settings", "closes", "judging"}},
		"o.reminders":      {{"organiser-settings", "", "reminders"}},
		"o.announcement":   {{"organiser-announcements", "", ""}},
		"o.deadline":       {{"organiser-settings", "deadline", "entries"}},
		"o.judges":         {{"organiser-settings", "organiser-judges", "judging"}},
		"o.rubric-ready":   {{"organiser-settings", "rubric", "judging"}},
		"o.assignments":    {{"organiser-settings", "organiser-judges", "judging"}, {"organiser-judging-assignments", "jg-grid-rows", ""}, {"organiser-team-detail", "team-track", ""}},
		"o.lock":           {{"organiser-overview", "ov-next-move", ""}},
		"o.ties":           {{"organiser-overview", "ov-next-move", ""}},
		"o.publish":        {{"organiser-overview", "ov-next-move", ""}},
		"o.end":            {{"organiser-overview", "ov-end-target", ""}},
		"j.rubric":         {{"judge-rubric", "", ""}},
		"j.conflicts":      {{"judge-queue", "conflicts", "conflicts"}},
		"j.scores":         {{"judge-scoring", "", ""}, {"judge-queue", "", ""}},
		"p.team":           {{"participant-team", "", ""}},
		"p.summary":        {{"participant-entry", "", ""}},
		"p.evidence":       {{"participant-entry", "", ""}, {"participant-website", "", ""}, {"participant-entry", "en-video_url", ""}, {"participant-entry", "en-code_url", ""}, {"participant-entry", "en-shot", ""}},
		"p.submit":         {{"participant-entry", "", ""}},
	}
	allowed["o.assignments"] = append(allowed["o.assignments"], fix{"organiser-event-page", "", "tracks"}, fix{"organiser-judging", "jg-conflicts-card", ""})
	informational := map[string]bool{"o.scores": true, "j.invite": true, "j.coc": true, "j.closes": true}
	visited := map[string]bool{}
	for _, role := range []string{"organiser", "judge", "participant"} {
		for _, stage := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
			for _, mode := range []string{"open", "automatic", "manual", "panel"} {
				for _, state := range []string{"missing", "done", "blocked", "dismissed", "read-only"} {
					for _, variant := range []string{"default", "no-rubric", "no-judges", "no-tracks", "conflicts", "no-standings", "setup-locked", "panel-track", "unlocked-publish", "evidence-video", "evidence-code", "evidence-screenshot", "evidence-site"} {
						facts := map[string]ChecklistFact{}
						marks := map[string]bool{}
						for _, d := range ChecklistCatalogue() {
							f := ChecklistFact{Done: state == "done"}
							if state == "blocked" {
								f.BlockedReason = "unavailable"
							}
							if state == "dismissed" && d.Optional {
								marks[d.ID] = true
							}
							switch variant {
							case "no-rubric":
								if d.ID == "o.rubric-ready" || d.ID == "o.tie-break" {
									f.TargetID = "o.rubric"
								}
								if d.ID == "j.rubric" {
									f.BlockedReason = "no_rubric"
								}
							case "no-judges":
								if d.ID == "o.judges" && (stage == "results" || stage == "archived") {
									f.BlockedReason = "judging_setup_locked"
								}
								if d.ID == "o.assignments" {
									f.TargetID = "o.organiser-judges"
								}
							case "conflicts":
								if d.ID == "o.assignments" {
									f.TargetID = "o.conflicts"
								}
							case "no-tracks":
								if d.ID == "o.assignments" && mode == "panel" {
									f.TargetID = "o.tracks"
								}
							case "no-standings":
								if d.ID == "o.ties" {
									f.BlockedReason = "informational"
								}
							case "setup-locked":
								if d.ID == "o.rubric" || d.ID == "o.mode" || d.ID == "o.tie-break" || d.ID == "o.assignments" || d.ID == "o.judges" {
									f.BlockedReason = "judging_setup_locked"
								}
							case "panel-track":
								if d.ID == "o.assignments" && mode == "panel" {
									f.TargetID = "o.team-track"
								}
							case "unlocked-publish":
								if d.ID == "o.publish" {
									f.TargetID = "o.lock"
								}
							case "evidence-video":
								if d.ID == "p.evidence" {
									f.TargetID = "p.evidence-video"
								}
							case "evidence-code":
								if d.ID == "p.evidence" {
									f.TargetID = "p.evidence-code"
								}
							case "evidence-screenshot":
								if d.ID == "p.evidence" {
									f.TargetID = "p.evidence-screenshot"
								}
							case "evidence-site":
								if d.ID == "p.evidence" {
									f.TargetID = "p.website"
								}
							}
							facts[d.ID] = f
						}
						list := BuildChecklist(ChecklistInput{Role: role, Stage: stage, AssignmentMode: mode, JudgesPerTeam: 2, Facts: facts, Marks: marks, ReadOnly: state == "read-only"}, time.Time{})
						for _, it := range list.Items {
							visited[it.ID] = true
							if variant == "no-judges" && stage == "results" && it.ID == "o.judges" && (it.Actionable || it.BlockedReason == nil) {
								t.Fatal("Results without judges must be blocked")
							}
							if !it.Actionable {
								continue
							}
							if informational[it.ID] || state == "blocked" || stage == "archived" {
								t.Fatalf("%s/%s/%s/%s: informational action: %s", role, stage, mode, state, it.ID)
							}
							link, err := Resolve(it.TargetID, "test", map[string]string{"team": "team"})
							if err != nil {
								t.Fatal(err)
							}
							anchor, tab := "", ""
							if link.Anchor != nil {
								anchor = *link.Anchor
							}
							if link.Tab != nil {
								tab = *link.Tab
							}
							found := false
							for _, control := range allowed[it.ID] {
								if control == (fix{link.Screen, anchor, tab}) {
									found = true
								}
							}
							if !found {
								t.Fatalf("%s/%s/%s/%s/%s: %s has no fix at %s #%s tab=%s", role, stage, mode, state, variant, it.ID, link.Screen, anchor, tab)
							}
						}
					}
				}
			}
		}
	}
	for _, d := range ChecklistCatalogue() {
		if !visited[d.ID] {
			t.Errorf("catalogue item never audited: %s", d.ID)
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
