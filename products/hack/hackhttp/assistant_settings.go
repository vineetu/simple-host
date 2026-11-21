package hackhttp

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

// This event's current settings, as plain lines in the organiser's own words, for the assistant's prompt. Built from an allow-list (never the raw JSON views pushed through the secret-key deny-list, which would drop video_url/code_url and hide two hand-in fields). Every label is the UI's label; TestAssistantSettingsLabelsInHelp holds each to a label in navigation-help.json, so a renamed UI label fails a test instead of the assistant using an old name. A failed read is shown as "unavailable", never as an empty or default value.

// assistantEntryFieldLabels: entry-rule field id -> the row label on
// Settings > Entries. "description" has no row in today's editor.
var assistantEntryFieldLabels = []struct{ ID, Label string }{
	{"title", "Project name"},
	{"problem", "Problem, in one line"},
	{"tagline", "Solution, in one line"},
	{"audience", "Who it's for"},
	{"track", "Track"},
	{"tools", "Tools and AI"},
	{"maturity", "What works today"},
	{"video_url", "Video link"},
	{"site", "Website"},
	{"code_url", "Code link"},
	{"screenshot", "Screenshot"},
}

var assistantFieldModeLabels = map[string]string{
	"required": "Required", "optional": "Optional", "off": "Off", "one_of": "One of",
}

var assistantPresetLabels = map[string]string{
	"video_or_website":   "Video or website",
	"video_website_code": "Video, website and code link",
}

// Older presets an event may still be on: they keep working but can no
// longer be picked (rules/hackentries Presets).
var assistantLegacyPresetLabels = map[string]string{
	"build": "AI build", "code": "Coding", "ideas": "Ideas and design",
}

var assistantJudgeFirstLabels = map[string]string{"video": "Video", "site": "Website"}

var assistantAssignmentModeLabels = map[string]string{
	"open": "Open", "automatic": "Automatic", "manual": "Manual", "panel": "Panel",
}

var assistantScoreModeLabels = map[string]string{
	"normalised": "Average, adjusted for strict and generous judges",
	"raw":        "Average score",
}

var assistantEligibilityLabels = map[string]string{
	"all_signed_in": "Anyone signed in",
	"event_members": "Approved event members",
	"participants":  "Approved participants only",
}

var assistantWebsiteModeLabels = map[string]string{"builtin": "Built-in event page", "custom": "Your own website"}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func labelOr(m map[string]string, v string) string {
	if l, ok := m[v]; ok {
		return l
	}
	if v == "" {
		return "not set"
	}
	return v
}

func assistantTime(ev model.Event, t time.Time) string {
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil || ev.TimeZone == "" {
		loc = time.UTC
	}
	return t.In(loc).Format("2 January 2006, 15:04") + " (" + loc.String() + ")"
}

func assistantOffsetLabel(m int64) string {
	switch {
	case m >= 1440 && m%1440 == 0:
		d := m / 1440
		if d == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", d)
	case m >= 60 && m%60 == 0:
		hr := m / 60
		if hr == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hr)
	case m == 1:
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}

type settingsText struct {
	strings.Builder
	ev model.Event
}

func (b *settingsText) line(format string, args ...any) {
	b.WriteString("- ")
	fmt.Fprintf(b, format, args...)
	b.WriteString("\n")
}

func (b *settingsText) timeOr(t sql.NullTime) string {
	if !t.Valid {
		return "not set"
	}
	return assistantTime(b.ev, t.Time)
}

func (b *settingsText) unavailable(what, label string, err error) {
	log.Printf("hack: assistant settings %s %s: %v", what, b.ev.Slug, err)
	b.line("%s: unavailable", label)
}

func (h *assistantHTTP) assistantCurrentSettings(ctx context.Context, ev model.Event) string {
	b := &settingsText{ev: ev}
	h.signupSettings(ctx, b)
	entrySettings(b)
	h.judgingSettings(ctx, b)
	h.resultSettings(ctx, b)
	h.pageSettings(ctx, b)
	return b.String()
}

func (h *assistantHTTP) signupSettings(ctx context.Context, b *settingsText) {
	ev := b.ev
	if questions, approval, err := h.members.Registration(ctx, ev.ID); err != nil {
		b.unavailable("registration", "Sign-up", err)
	} else {
		b.line("Approve people who join with your link: %s", onOff(approval))
		b.line("Sign-up questions: %d", len(questions))
	}
	b.line("Let people apply without your join link (Applications): %s", onOff(ev.ApplicationsOpen))
	b.line("List this event on Find a hackathon: %s", onOff(ev.DirectoryListed))
	b.line("Team finding: %s", onOff(ev.TeamFinding))
	b.line("Most people per team: %d", ev.TeamSizeMax)
}

func entrySettings(b *settingsText) {
	ev := b.ev
	b.line("Entries close: %s", b.timeOr(ev.SubmissionDeadline))
	saved := "not yet saved by an organiser; this is what the event starts with"
	if ev.EntryRulesChosen {
		saved = "saved by an organiser"
	}
	b.line("What teams hand in: %s (%s)", presetLabel(ev.EntryPreset), saved)
	fields := entries.LoadRules(ev.EntryRules)
	b.line("Summary card and Show your work rows: %s", entryFieldRows(fields))
	if fields["description"] == "required" {
		// No editor row today; only older rules can still require it.
		b.line("Longer description (an older field with no row in today's editor): Required")
	}
	b.line("Judges see, after the card, first: %s", labelOr(assistantJudgeFirstLabels, ev.JudgeFirst))
	if len(ev.ReminderOffsets) == 0 {
		b.line("Reminder times: none (reminders are off)")
		return
	}
	offs := make([]string, len(ev.ReminderOffsets))
	for i, m := range ev.ReminderOffsets {
		offs[i] = assistantOffsetLabel(m)
	}
	b.line("Reminder times: %s before", strings.Join(offs, ", "))
}

func presetLabel(preset string) string {
	if l, ok := assistantPresetLabels[preset]; ok {
		return l
	}
	if l, ok := assistantLegacyPresetLabels[preset]; ok {
		return l + " (an older preset that can no longer be picked; the event keeps it until another preset is chosen)"
	}
	return "Custom (rows changed from a preset)"
}

// entryFieldRows is each Entries row with its mode; the title is always
// required and an unset row is optional.
func entryFieldRows(fields map[string]string) string {
	rows := make([]string, 0, len(assistantEntryFieldLabels))
	for _, f := range assistantEntryFieldLabels {
		mode := fields[f.ID]
		if f.ID == "title" {
			mode = "required"
		}
		if mode == "" {
			mode = "optional"
		}
		rows = append(rows, f.Label+": "+labelOr(assistantFieldModeLabels, mode))
	}
	return strings.Join(rows, "; ")
}

func (h *assistantHTTP) judgingSettings(ctx context.Context, b *settingsText) {
	ev := b.ev
	b.line("Judging closes: %s", b.timeOr(ev.JudgingEndsAt))
	h.rubricSettings(ctx, b)
	b.line("How judges are assigned: %s; judges per team: %d", labelOr(assistantAssignmentModeLabels, ev.JudgeAssignmentMode), ev.JudgesPerTeam)
	if judges, err := h.entries.EventJudges(ctx, ev.ID); err == nil {
		names := make([]string, 0, len(judges))
		for _, j := range judges {
			if j.Role == "organiser" {
				j.Name += " (organiser)"
			}
			names = append(names, j.Name)
		}
		b.line("Who judges (joined judges plus selected organisers): %s", strings.Join(names, ", "))
	}
	if ev.JudgingLockedAt.Valid {
		b.line("Judging locked: yes, since %s", assistantTime(ev, ev.JudgingLockedAt.Time))
	} else {
		b.line("Judging locked: no")
	}
}

func (h *assistantHTTP) rubricSettings(ctx context.Context, b *settingsText) {
	ev := b.ev
	items, err := h.entries.Rubric(ctx, ev.ID)
	if err != nil {
		b.unavailable("rubric", "Rubric", err)
		if ev.TieCriterionID.Valid {
			b.line("First tie criterion: unavailable")
		} else {
			b.line("First tie criterion: not chosen")
		}
		return
	}
	tie := "not chosen"
	crit := make([]string, 0, len(items))
	for _, c := range items {
		crit = append(crit, fmt.Sprintf("%s (weight %d%%, max %d points)", c.Name, c.Weight, c.MaxPoints))
		if ev.TieCriterionID.Valid && c.ID == ev.TieCriterionID.String && c.Name != "" {
			tie = c.Name
		}
	}
	b.line("Rubric (%d criteria): %s", len(items), strings.Join(crit, "; "))
	b.line("First tie criterion: %s", tie)
}

func (h *assistantHTTP) resultSettings(ctx context.Context, b *settingsText) {
	ev := b.ev
	b.line("Score used for ranking: %s", labelOr(assistantScoreModeLabels, ev.ScoreMode))
	res, err := h.entries.EventResults(ctx, ev.ID)
	switch {
	case err != nil:
		b.unavailable("results", "Results", err)
	case !res.Published:
		b.line("Results published: no (Visibility can be set once results are published)")
		b.line("Show scores: %s; Show ranks: %s", onOff(ev.PublicScores), onOff(ev.PublicRanks))
	default:
		vis := "Winners only"
		if res.FullRanking {
			vis = "Full ranking"
		}
		b.line("Results published: yes, last on %s", assistantTime(ev, res.PublishedAt.Time))
		b.line("Visibility on the public page: %s; Show scores: %s; Show ranks: %s", vis, onOff(ev.PublicScores), onOff(ev.PublicRanks))
	}
	v, err := h.entries.ReadVotingSettings(ctx, ev, time.Now)
	if err != nil {
		b.unavailable("voting", "People's choice", err)
		return
	}
	window := ""
	if v.Settings.OpensAt.Valid {
		window += "; starts " + assistantTime(ev, v.Settings.OpensAt.Time)
	}
	if v.Settings.ClosesAt.Valid {
		window += "; ends " + assistantTime(ev, v.Settings.ClosesAt.Time)
	}
	b.line("People's choice (Let visitors vote): %s%s; Who can vote: %s", onOff(v.Settings.Enabled), window, labelOr(assistantEligibilityLabels, v.Settings.Eligibility))
}

// pageSettings is Look and Website, plus the limits Simple Hack fixes (said
// as such, or the model offers to "change this in Settings").
func (h *assistantHTTP) pageSettings(ctx context.Context, b *settingsText) {
	ev := b.ev
	b.line("Show entries on the event page (gallery): %s", onOff(ev.GalleryOpen))
	b.line("What shows at your address: %s", labelOr(assistantWebsiteModeLabels, ev.WebsiteMode))
	b.line("Website zip upload limit: %d MB (fixed by Simple Hack; organisers can't change it)", h.website.uploadLimit(ctx, ev)>>20)
	if ev.KeepSites {
		b.line("Team sites after the event ends: kept, this event is exempt from cleanup (set by Simple Hack; organisers can't change it)")
		return
	}
	b.line("Team sites after the event ends: removed %d days after it ends (fixed by Simple Hack; organisers can't change it)", int(h.limits.EventSitesKeep.Hours()/24))
}
