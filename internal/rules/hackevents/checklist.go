package hackevents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/vsriram/simple-host/internal/model"
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
		{"o.times", "Add start and end times", "o.basics", "draft", true},
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
		{"o.rubric-ready", "Rubric final", "o.rubric", "closed", false},
		{"o.assignments", "Assign judges", "o.assignments", "closed", false},
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
// now is explicit to keep callers and boundary tests on the same clock.
func BuildChecklist(in ChecklistInput, now time.Time) Checklist {
	out := Checklist{Role: in.Role, Stage: in.Stage, MapVersion: 1, Items: []ChecklistItem{}}
	if in.Role == "" {
		return out
	}
	_ = now // Evaluation timestamps are supplied in facts; never read an ambient clock.
	var earlier, current, optional, history []ChecklistItem
	for _, d := range ChecklistCatalogue() {
		if string(d.ID[0]) != string(in.Role[0]) {
			continue
		}
		if d.ID == "o.assignments" && in.AssignmentMode == "open" {
			continue
		}
		if (d.ID == "j.invite" || d.ID == "j.coc") && in.MembershipRole == "organiser" {
			continue
		}
		f := in.Facts[d.ID]
		target := d.Target
		if f.TargetID != "" {
			target = f.TargetID
		}
		item := ChecklistItem{ID: d.ID, Label: d.Label, State: ChecklistState(f.Done, in.Marks[d.ID] && d.Optional && in.Role == "organiser", d.Optional), Optional: d.Optional, CanDismiss: !f.Done && d.Optional && in.Role == "organiser" && !in.ReadOnly && in.Stage != "archived", OriginStage: d.Stage, Group: "current", Actionable: !in.ReadOnly && in.Stage != "archived" && f.BlockedReason == "", TargetID: target, Detail: f.Detail, Count: f.Count, Total: f.Total, Settled: f.Settled, Version: f.Version}
		if f.BlockedReason != "" {
			reason := f.BlockedReason
			item.BlockedReason = &reason
		}
		if in.Role != "organiser" {
			current = append(current, item)
			continue
		}
		isCurrent := d.Stage == in.Stage || (in.Stage == "building" && d.Stage == "open") || (in.Stage == "results" && (d.ID == "o.scores" || d.ID == "o.lock"))
		if in.Stage == "archived" {
			item.Actionable = false
			item.CanDismiss = false
			if d.ID == "o.end" || (d.ID == "o.publish" && f.Done) {
				current = append(current, item)
			} else {
				item.Group = "history"
				history = append(history, item)
			}
		} else if isCurrent {
			current = append(current, item)
		} else if stageIndex(d.Stage) < stageIndex(in.Stage) {
			if d.Optional {
				item.Group = "earlier_optional"
				optional = append(optional, item)
			} else if !f.Done {
				item.Group = "earlier_required"
				earlier = append(earlier, item)
			}
		}
	}
	out.Items = append(out.Items, earlier...)
	out.Items = append(out.Items, current...)
	out.Items = append(out.Items, optional...)
	out.Items = append(out.Items, history...)
	if in.ReadOnly || in.Stage == "archived" {
		return out
	}
	for _, it := range out.Items {
		if it.Optional || it.State != "todo" || !it.Actionable {
			continue
		}
		if in.Role == "participant" && it.ID != "p."+in.ParticipantNext {
			continue
		}
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
			n.Kind = "publish_results"
			n.ConfirmationRequired = true
		case "p.submit":
			n.Kind = "submit_entry"
			n.ConfirmationRequired = true
		}
		if in.Forward != nil {
			n.Cue = in.Forward.Cue
		}
		out.Next = n
		return out
	}
	if in.Role == "organiser" {
		out.Next = in.Forward
	}
	if in.Role == "judge" {
		f := in.Facts["j.scores"]
		if f.Done && f.BlockedReason == "" && f.Total != nil && *f.Total > 0 {
			out.Next = &ChecklistNext{Kind: "link", ItemID: "j.scores", TargetID: "j.queue", Label: "Review my scores"}
		}
	}

	return out
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
	return strings.TrimSpace(ev.Title) != "" && strings.TrimSpace(ev.Tagline) != "" && ev.StartsAt.Valid && ev.EndsAt.Valid && (ev.TimeZone == "" || err == nil) && !EventOver(ev.EndsAt.Time, ev.TimeZone).Before(ev.StartsAt.Time)
}
