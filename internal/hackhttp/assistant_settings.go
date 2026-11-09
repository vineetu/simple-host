package hackhttp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	entries "github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/model"
)

// Organiser assistant v2 (2026-11-09): this event's current settings, as
// plain lines in the organiser's own words, for the assistant's prompt.
// Built from an allow-list (never the raw JSON views pushed through the
// secret-key deny-list, which would drop video_url/code_url and so hide
// two hand-in fields). Every label below is the UI's label; the
// TestAssistantSettingsLabelsInHelp test holds each one to a label or an
// option label in navigation-help.json, so a renamed UI label shows up as
// a failing test rather than as the assistant using an old name. A read
// that fails is shown as "unavailable", never as an empty or default
// value.

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

// assistantTime renders a stored time in the event's own time zone, the
// way the organiser sees it in the app.
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

// assistantCurrentSettings builds the CURRENT SETTINGS block for one event.
func (h *HackHandler) assistantCurrentSettings(ctx context.Context, ev model.Event) string {
	var b strings.Builder
	line := func(format string, args ...any) {
		b.WriteString("- ")
		fmt.Fprintf(&b, format, args...)
		b.WriteString("\n")
	}

	// Sign-up and teams (Settings > Sign-up, Teams).
	if questions, approval, err := h.membersSvc().Registration(ctx, ev.ID); err != nil {
		log.Printf("hack: assistant settings registration %s: %v", ev.Slug, err)
		line("Sign-up: unavailable")
	} else {
		line("Approve people who join with your link: %s", onOff(approval))
		line("Sign-up questions: %d", len(questions))
	}
	line("Let people apply without your join link (Applications): %s", onOff(ev.ApplicationsOpen))
	line("List this event on Find a hackathon: %s", onOff(ev.DirectoryListed))
	line("Team finding: %s", onOff(ev.TeamFinding))
	line("Most people per team: %d", ev.TeamSizeMax)

	// Entries (Settings > Entries).
	if ev.SubmissionDeadline.Valid {
		line("Entries close: %s", assistantTime(ev, ev.SubmissionDeadline.Time))
	} else {
		line("Entries close: not set")
	}
	fields := entries.LoadRules(ev.EntryRules)
	preset := "Custom (rows changed from a preset)"
	if l, ok := assistantPresetLabels[ev.EntryPreset]; ok {
		preset = l
	} else if l, ok := assistantLegacyPresetLabels[ev.EntryPreset]; ok {
		preset = l + " (an older preset that can no longer be picked; the event keeps it until another preset is chosen)"
	}
	if ev.EntryRulesChosen {
		line("What teams hand in: %s (saved by an organiser)", preset)
	} else {
		line("What teams hand in: %s (not yet saved by an organiser; this is what the event starts with)", preset)
	}
	var rows []string
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
	line("Summary card and Show your work rows: %s", strings.Join(rows, "; "))
	if m := fields["description"]; m == "required" {
		// No editor row today; only older rules can still require it.
		line("Longer description (an older field with no row in today's editor): Required")
	}
	line("Judges see, after the card, first: %s", labelOr(assistantJudgeFirstLabels, ev.JudgeFirst))
	if len(ev.ReminderOffsets) == 0 {
		line("Reminder times: none (reminders are off)")
	} else {
		var offs []string
		for _, m := range ev.ReminderOffsets {
			offs = append(offs, assistantOffsetLabel(m))
		}
		line("Reminder times: %s before", strings.Join(offs, ", "))
	}

	// Judging (Settings > Judging, Judging).
	if ev.JudgingEndsAt.Valid {
		line("Judging closes: %s", assistantTime(ev, ev.JudgingEndsAt.Time))
	} else {
		line("Judging closes: not set")
	}
	tieName := ""
	rubricOK := true
	if items, err := h.entries.Rubric(ctx, ev.ID); err != nil {
		log.Printf("hack: assistant settings rubric %s: %v", ev.Slug, err)
		line("Rubric: unavailable")
		rubricOK = false
	} else {
		var crit []string
		for _, c := range items {
			crit = append(crit, fmt.Sprintf("%s (weight %d%%, max %d points)", c.Name, c.Weight, c.MaxPoints))
			if ev.TieCriterionID.Valid && c.ID == ev.TieCriterionID.String {
				tieName = c.Name
			}
		}
		line("Rubric (%d criteria): %s", len(items), strings.Join(crit, "; "))
	}
	switch {
	case !rubricOK && ev.TieCriterionID.Valid:
		line("First tie criterion: unavailable")
	case tieName != "":
		line("First tie criterion: %s", tieName)
	default:
		line("First tie criterion: not chosen")
	}
	line("How judges are assigned: %s; judges per team: %d", labelOr(assistantAssignmentModeLabels, ev.JudgeAssignmentMode), ev.JudgesPerTeam)
	if ev.JudgingLockedAt.Valid {
		line("Judging locked: yes, since %s", assistantTime(ev, ev.JudgingLockedAt.Time))
	} else {
		line("Judging locked: no")
	}

	// Results (Settings > Results).
	line("Score used for ranking: %s", labelOr(assistantScoreModeLabels, ev.ScoreMode))
	if res, err := h.entries.EventResults(ctx, ev.ID); err != nil {
		log.Printf("hack: assistant settings results %s: %v", ev.Slug, err)
		line("Results: unavailable")
	} else if !res.Published {
		line("Results published: no (Visibility can be set once results are published)")
		line("Show scores: %s; Show ranks: %s", onOff(ev.PublicScores), onOff(ev.PublicRanks))
	} else {
		vis := "Winners only"
		if res.FullRanking {
			vis = "Full ranking"
		}
		line("Results published: yes, last on %s", assistantTime(ev, res.PublishedAt.Time))
		line("Visibility on the public page: %s; Show scores: %s; Show ranks: %s", vis, onOff(ev.PublicScores), onOff(ev.PublicRanks))
	}
	if v, err := h.entries.ReadVotingSettings(ctx, ev, time.Now); err != nil {
		log.Printf("hack: assistant settings voting %s: %v", ev.Slug, err)
		line("People's choice: unavailable")
	} else {
		s := v.Settings
		window := ""
		if s.OpensAt.Valid {
			window += "; starts " + assistantTime(ev, s.OpensAt.Time)
		}
		if s.ClosesAt.Valid {
			window += "; ends " + assistantTime(ev, s.ClosesAt.Time)
		}
		line("People's choice (Let visitors vote): %s%s; Who can vote: %s", onOff(s.Enabled), window, labelOr(assistantEligibilityLabels, s.Eligibility))
	}

	// Event page (Look, Website).
	line("Show entries on the event page (gallery): %s", onOff(ev.GalleryOpen))
	line("What shows at your address: %s", labelOr(assistantWebsiteModeLabels, ev.WebsiteMode))
	// Not organiser settings: fixed by the server, so said as such (the
	// model otherwise offers to "change this in Settings").
	line("Website zip upload limit: %d MB (fixed by Simple Hack; organisers can't change it)", h.websiteUploadLimit(ctx, ev)>>20)
	if ev.KeepSites {
		line("Team sites after the event ends: kept, this event is exempt from cleanup (set by Simple Hack; organisers can't change it)")
	} else {
		line("Team sites after the event ends: removed %d days after it ends (fixed by Simple Hack; organisers can't change it)", int(h.limits.EventSitesKeep.Hours()/24))
	}
	return b.String()
}
