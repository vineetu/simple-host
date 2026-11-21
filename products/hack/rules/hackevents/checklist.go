package hackevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/products/hack/model"
	"strings"
	"time"
)

// ChecklistLink is a resolved destination from the shared navigation map.
type ChecklistLink struct {
	Screen  string  `json:"screen"`
	Section string  `json:"section"`
	Tab     *string `json:"tab"`
	Href    string  `json:"href"`
}

type ChecklistItem struct {
	ID            string         `json:"id"`
	Label         string         `json:"label"`
	State         string         `json:"state"`
	Optional      bool           `json:"optional"`
	CanDismiss    bool           `json:"can_dismiss"`
	OriginStage   string         `json:"origin_stage"`
	Group         string         `json:"group"`
	Actionable    bool           `json:"actionable"`
	TargetID      string         `json:"target_id"`
	Link          *ChecklistLink `json:"link"`
	Detail        string         `json:"detail"`
	Count         *int           `json:"count"`
	Total         *int           `json:"total"`
	Settled       bool           `json:"settled"`
	BlockedReason *string        `json:"blocked_reason"`
	Version       string         `json:"version,omitempty"`
}

type ChecklistNext struct {
	Kind                 string         `json:"kind"`
	ItemID               string         `json:"item_id,omitempty"`
	TargetID             string         `json:"target_id,omitempty"`
	Label                string         `json:"label"`
	Link                 *ChecklistLink `json:"link"`
	Stage                string         `json:"stage,omitempty"`
	ConfirmationRequired bool           `json:"confirmation_required"`
	BlockedReason        any            `json:"blocked_reason"`
	Cue                  any            `json:"cue,omitempty"`
	DeadlinePassed       bool           `json:"deadline_passed,omitempty"`
}

type Checklist struct {
	Role       string          `json:"role"`
	Stage      string          `json:"stage"`
	MapVersion int             `json:"map_version"`
	Items      []ChecklistItem `json:"items"`
	Next       *ChecklistNext  `json:"next"`
}

type ChecklistDefinition struct {
	ID, Label, Target, Stage string
	Optional                 bool
}

// ChecklistCatalogue is returned by value so callers cannot change the catalogue.
func ChecklistCatalogue() []ChecklistDefinition {
	return []ChecklistDefinition{
		{"o.entry-rules", "Choose what teams hand in", "o.entry-rules", "draft", false},
		{"o.basics", "Add event basics", "o.basics", "draft", false},
		{"o.times", "Add start and end times", "o.times", "draft", true},
		{"o.about", "Describe the event", "o.about", "draft", false},
		{"o.schedule", "Add a schedule", "o.schedule", "draft", true},
		{"o.faq", "Add common questions", "o.faq", "draft", true},
		{"o.sponsors", "Add sponsors", "o.sponsors", "draft", true},
		{"o.tracks", "Add tracks", "o.tracks", "draft", true},
		{"o.rubric", "Set the rubric", "o.rubric", "draft", false},
		{"o.mode", "Choose how judges score", "o.judging-mode", "draft", false},
		{"o.tie-break", "Choose a tie-break", "o.tie-break", "draft", true},
		{"o.judging-closes", "Set when judging closes", "o.judging-closes", "draft", true},
		{"o.reminders", "Set reminder times", "o.reminders", "draft", true},
		{"o.announcement", "Send an announcement", "o.announcements", "open", true},
		{"o.deadline", "Set the entry deadline", "o.deadline", "open", false},
		{"o.judges", "Have judges ready", "o.judges", "closed", false},
		{"o.assignments", "Assign judges", "o.assignments", "closed", false},
		{"o.judges-short", "Teams have fewer judges than the target", "o.scores", "judging", true},
		{"o.scores", "Every judge done", "o.judging", "judging", false},
		{"o.lock", "Finish judging", "o.lock", "judging", false},
		{"o.ties", "Resolve tied places", "o.ties", "results", false},
		{"o.publish", "Publish results", "o.publish", "results", false},
		{"o.end", "End the event", "o.end", "results", false},
		{"j.invite", "Accept the judge invite", "j.invite", "draft", false},
		{"j.coc", "Accept the code of conduct", "j.conduct", "draft", false},
		{"j.rubric", "Read the rubric", "j.rubric", "draft", false},
		{"j.conflicts", "Check who you know", "j.conflicts", "draft", false},
		{"j.scores", "Score each team", "j.scoring", "closed", false},
		{"j.closes", "Judging closes", "j.home", "closed", true},
		{"p.team", "Join or start a team", "p.team", "draft", false},
		{"p.summary", "Fill in the summary card", "p.entry", "draft", false},
		{"p.evidence", "Add evidence", "p.entry", "draft", false},
		{"p.submit", "Submit before the deadline", "p.entry", "draft", false},
	}
}

// ChecklistFact carries evaluated server facts, never persisted completion flags.
type ChecklistFact struct {
	Done          bool
	Detail        string
	Count, Total  *int
	BlockedReason string
	Settled       bool
	Version       string
	TargetID      string
}
type ChecklistInput struct {
	JudgesPerTeam                               int
	Role, Stage, MembershipRole, AssignmentMode string
	ReadOnly                                    bool
	Facts                                       map[string]ChecklistFact
	Marks                                       map[string]bool
	Forward                                     *ChecklistNext
	ParticipantNext                             string
}

func ChecklistState(done, marked, optional bool) string {
	if done {
		return "done"
	}
	if marked {
		return "not_needed"
	}
	if optional {
		return "optional"
	}
	return "todo"
}
func stageIndex(s string) int {
	for i, v := range []string{"draft", "open", "building", "closed", "judging", "results", "archived"} {
		if s == v {
			return i
		}
	}
	return -1
}

// BuildChecklist selects and deduplicates stage rows and the first actionable required task.
func BuildChecklist(in ChecklistInput, now time.Time) Checklist {
	out := Checklist{Role: in.Role, Stage: in.Stage, MapVersion: 1, Items: []ChecklistItem{}}
	if in.Role == "" {
		return out
	}
	_ = now // Evaluation timestamps are supplied in facts; never read an ambient clock.
	out.Items = checklistItems(in)
	if !in.ReadOnly && in.Stage != "archived" {
		out.Next = checklistNextStep(out.Items, in)
	}
	return out
}

// checklistItems is the role's rows, grouped in display order: earlier required, current, earlier optional, history.
func checklistItems(in ChecklistInput) []ChecklistItem {
	groups := map[string][]ChecklistItem{}
	for _, d := range ChecklistCatalogue() {
		d, ok := checklistDefinition(d, in)
		if !ok {
			continue
		}
		f := in.Facts[d.ID]
		item := checklistItem(d, f, in)
		if item.Group = checklistGroup(d, f, item, in); item.Group != "" {
			groups[item.Group] = append(groups[item.Group], item)
		}
	}
	items := []ChecklistItem{}
	for _, g := range []string{"earlier_required", "current", "earlier_optional", "history"} {
		items = append(items, groups[g]...)
	}
	return items
}

// checklistNextStep is the first item that can lead; failing that, the organiser's stage move forward, or a judge's review of finished scores.
func checklistNextStep(items []ChecklistItem, in ChecklistInput) *ChecklistNext {
	for _, it := range items {
		if checklistLeads(it, in) {
			return checklistNext(it, in)
		}
	}
	switch in.Role {
	case "organiser":
		return in.Forward
	case "judge":
		if f := in.Facts["j.scores"]; f.Done && f.BlockedReason == "" && f.Total != nil && *f.Total > 0 {
			return &ChecklistNext{Kind: "link", ItemID: "j.scores", TargetID: "j.queue", Label: "Review my scores"}
		}
	}
	return nil
}

// checklistLeads reports whether it can be the next step.
func checklistLeads(it ChecklistItem, in ChecklistInput) bool {
	if it.Optional || it.State != "todo" || !it.Actionable {
		return false
	}
	if it.ID == "o.deadline" && in.Forward != nil && (in.Forward.Stage == "building" || in.Forward.Stage == "closed") {
		return false
	}
	return in.Role != "participant" || it.ID == "p."+in.ParticipantNext
}

// checklistDefinition is d as this role, mode and stage show it; false leaves it out.
func checklistDefinition(d ChecklistDefinition, in ChecklistInput) (ChecklistDefinition, bool) {
	switch {
	case d.ID[0] != in.Role[0]:
		return d, false
	case d.ID == "o.assignments" && in.AssignmentMode == "open":
		return d, false
	case (d.ID == "j.invite" || d.ID == "j.coc") && in.MembershipRole == "organiser":
		return d, false
	case d.ID == "o.assignments" && in.AssignmentMode == "automatic":
		d.Label = fmt.Sprintf("Enough judges for %d per team", in.JudgesPerTeam)
		d.Target = "o.judges"
	case d.ID == "o.judges-short":
		f := in.Facts[d.ID]
		if in.Stage != "judging" || f.Count == nil || *f.Count == 0 {
			return d, false
		}
		d.Label = fmt.Sprintf("%d teams have fewer judges than the target", *f.Count)
		if *f.Count == 1 {
			d.Label = "1 team has fewer judges than the target"
		}
	}
	return d, true
}

// Items that never offer an action once done, and items that never do.
var (
	checklistDoneInert = map[string]bool{"o.lock": true, "o.ties": true, "j.rubric": true, "p.submit": true}
	checklistInert     = map[string]bool{"o.scores": true, "j.invite": true, "j.coc": true, "j.closes": true}
)

func checklistItem(d ChecklistDefinition, f ChecklistFact, in ChecklistInput) ChecklistItem {
	target := d.Target
	if f.TargetID != "" {
		target = f.TargetID
	}
	organiser, open := in.Role == "organiser", !in.ReadOnly && in.Stage != "archived"
	item := ChecklistItem{ID: d.ID, Label: d.Label, State: ChecklistState(f.Done, in.Marks[d.ID] && d.Optional && organiser, d.Optional), Optional: d.Optional, CanDismiss: !f.Done && d.Optional && organiser && open, OriginStage: d.Stage, Actionable: (f.Done || !in.Marks[d.ID]) && open && f.BlockedReason == "", TargetID: target, Detail: f.Detail, Count: f.Count, Total: f.Total, Settled: f.Settled, Version: f.Version}
	if f.Done && checklistDoneInert[d.ID] || checklistInert[d.ID] {
		item.Actionable = false
	}
	if f.BlockedReason != "" {
		reason := f.BlockedReason
		item.BlockedReason = &reason
	}
	return item
}

// checklistGroup places an item; "" leaves it out.
func checklistGroup(d ChecklistDefinition, f ChecklistFact, item ChecklistItem, in ChecklistInput) string {
	switch {
	case in.Role != "organiser":
		return "current"
	case in.Stage == "archived" && (d.ID == "o.end" || (d.ID == "o.publish" && f.Done)):
		return "current"
	case in.Stage == "archived":
		return "history"
	case checklistInStage(d, in.Stage):
		return "current"
	case stageIndex(d.Stage) >= stageIndex(in.Stage), !f.Done && f.BlockedReason != "":
		return ""
	case d.Optional || !f.Done && !item.Actionable:
		return "earlier_optional"
	case !f.Done:
		return "earlier_required"
	}
	return ""
}

// checklistInStage reports whether d belongs to stage: its own stage, open's rows while building, and scores and lock while in results.
func checklistInStage(d ChecklistDefinition, stage string) bool {
	switch stage {
	case d.Stage:
		return true
	case "building":
		return d.Stage == "open"
	case "results":
		return d.ID == "o.scores" || d.ID == "o.lock"
	}
	return false
}

// checklistNext is the next step for the first actionable required item.
func checklistNext(it ChecklistItem, in ChecklistInput) *ChecklistNext {
	n := &ChecklistNext{Kind: "link", ItemID: it.ID, TargetID: it.TargetID, Label: it.Label}
	switch it.ID {
	case "o.lock":
		if in.Stage == "judging" {
			n.Kind = "stage"
			n.Stage = "results"
			n.ConfirmationRequired = true
		}
	case "o.end":
		n.Kind = "end_event"
		n.Stage = "archived"
		n.ConfirmationRequired = true
		if in.Forward != nil {
			n.BlockedReason = in.Forward.BlockedReason
		}
	case "o.publish":
		if it.TargetID == "o.lock" {
			n.Label = "Finish judging"
			break
		}
		n.Kind = "publish_results"
		n.ConfirmationRequired = true
	case "p.submit":
		n.Kind = "submit_entry"
		n.ConfirmationRequired = true
	}
	if in.Forward != nil {
		n.Cue = in.Forward.Cue
	}
	return n
}

// RubricFingerprint ignores unrelated event settings and includes ordered criterion identity and content.
func RubricFingerprint(criteria []model.RubricCriterion) string {
	if len(criteria) == 0 {
		return ""
	}
	type criterion struct {
		ID, Name, Description string
		Weight, MaxPoints     int
	}
	rows := make([]criterion, 0, len(criteria))
	for _, c := range criteria {
		rows = append(rows, criterion{c.ID, c.Name, c.Description, c.Weight, c.MaxPoints})
	}
	b, _ := json.Marshal(rows)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ChecklistBasicsReady shares patchEvent's local-date end boundary, including equality.
func ChecklistBasicsReady(ev model.Event) bool {
	_, err := time.LoadLocation(ev.TimeZone)
	return strings.TrimSpace(ev.Title) != "" && ev.StartsAt.Valid && ev.EndsAt.Valid && (ev.TimeZone == "" || err == nil) && !EventOver(ev.EndsAt.Time, ev.TimeZone).Before(ev.StartsAt.Time)
}
