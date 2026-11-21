package hackhttp

import (
	"context"
	"sort"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

// The event's real times (start, end, entry deadline, voting window, results published) show in the schedule as fixed rows. They are computed from the settings each time, never stored, so a row can never disagree with its setting; the organiser changes them where set (set_in), not in Schedule.
// A row can also line up with a forward stage move (prompt). When its time comes the organiser's next step says so ("Kickoff is now. Open sign-up?"). It only asks: nothing here moves a stage, because a late kickoff must not close entries early.

type hackFixedRow struct {
	Key         string  `json:"key"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	StartAt     string  `json:"start_at"`
	EndAt       *string `json:"end_at"`
	AllDay      bool    `json:"all_day"`
	Fixed       bool    `json:"fixed"`
	SetIn       string  `json:"set_in"`
	Prompt      string  `json:"prompt,omitempty"`
}

type hackFixedInputs struct {
	Event     model.Event
	Voting    *model.VotingSettings
	Results   entries.EventResultsRow
	Team      *model.TeamWriteState
	Organiser bool
	Now       time.Time
}

func hackEventLoc(ev model.Event) *time.Location {
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil || ev.TimeZone == "" {
		return time.UTC
	}
	return loc
}

func hackFixedSchedule(in hackFixedInputs) []hackFixedRow {
	ev := in.Event
	loc := hackEventLoc(ev)
	stamp := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	rows := []hackFixedRow{}
	add := func(r hackFixedRow) {
		r.Fixed = true
		if !in.Organiser {
			r.Prompt = ""
		}
		rows = append(rows, r)
	}
	if ev.StartsAt.Valid {
		r := hackFixedRow{Key: "event_start", Title: "Event starts", StartAt: stamp(ev.StartsAt.Time), SetIn: "basics"}
		r.AllDay = events.Midnight(ev.StartsAt.Time, loc)
		if !r.AllDay {
			r.Prompt = "building"
		}
		add(r)
	}
	if ev.EndsAt.Valid {
		r := hackFixedRow{Key: "event_end", Title: "Event ends", StartAt: stamp(ev.EndsAt.Time), SetIn: "basics"}
		r.AllDay = events.Midnight(ev.EndsAt.Time, loc)
		// With no entry deadline, the event's end is when entries are meant to close.
		if !r.AllDay && !ev.SubmissionDeadline.Valid {
			r.Prompt = "closed"
		}
		add(r)
	}
	if in.Team != nil && in.Team.HasOverride && in.Team.Deadline.Valid {
		add(hackFixedRow{Key: "team_deadline", Title: "Entries due for your team", Description: "Your team was given more time.",
			StartAt: stamp(in.Team.Deadline.Time), SetIn: "entries"})
	} else if ev.SubmissionDeadline.Valid {
		add(hackFixedRow{Key: "entry_deadline", Title: "Entries due", StartAt: stamp(ev.SubmissionDeadline.Time), SetIn: "entries", Prompt: "closed"})
	}
	if ev.JudgingEndsAt.Valid {
		// judging_end is a plain organiser-set time, not inferred from any schedule row.
		add(hackFixedRow{Key: "judging_end", Title: "Judging closes", StartAt: stamp(ev.JudgingEndsAt.Time), SetIn: "judging", Prompt: "results"})
	}
	if in.Voting != nil && votingStatus(ev, *in.Voting, in.Now) != "disabled" {
		end := stamp(in.Voting.ClosesAt.Time)
		add(hackFixedRow{Key: "voting", Title: "People's choice voting", Description: "Vote for your favourite projects on the event page.",
			StartAt: stamp(in.Voting.OpensAt.Time), EndAt: &end, SetIn: "results"})
	}
	if in.Results.Published && in.Results.PublishedAt.Valid {
		add(hackFixedRow{Key: "results_published", Title: "Results published", StartAt: stamp(in.Results.PublishedAt.Time), SetIn: "results"})
	}
	sort.SliceStable(rows, func(i, j int) bool { return hackFixedSortTime(rows[i], loc).Before(hackFixedSortTime(rows[j], loc)) })
	return rows
}

// hackFixedSortTime places a date-only event end after everything else on
// its day: the event runs to the end of that date.
func hackFixedSortTime(r hackFixedRow, loc *time.Location) time.Time {
	t, _ := time.Parse(time.RFC3339, r.StartAt)
	if r.AllDay && r.Key == "event_end" {
		l := t.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc).Add(-time.Second)
	}
	return t
}

// hackCuePointWindow is how long a moment (a deadline, the event's start)
// reads as "now" in the next step before it reads as past.
const hackCuePointWindow = 15 * time.Minute

// hackCueTime is a time in the event's zone, 12-hour like every other time
// in the product: just the time on the same day, else the day too.
func hackCueTime(t, now time.Time, loc *time.Location) string {
	lt, ln := t.In(loc), now.In(loc)
	if lt.Year() == ln.Year() && lt.YearDay() == ln.YearDay() {
		return lt.Format("3:04 pm")
	}
	return lt.Format("Mon 2 Jan, 3:04 pm")
}

// hackTimedRow is a schedule row with parsed times; sortAt is where it sorts.
type hackTimedRow struct {
	key, title, desc, prompt string
	start, end, sortAt       time.Time
	hasEnd, allDay, fixed    bool
}

func hackTimedRows(free []hackScheduleItem, fixed []hackFixedRow, loc *time.Location) []hackTimedRow {
	var rows []hackTimedRow
	for _, it := range free {
		start, e1 := time.Parse(time.RFC3339, it.StartAt)
		end, e2 := time.Parse(time.RFC3339, it.EndAt)
		if e1 != nil || e2 != nil {
			continue
		}
		rows = append(rows, hackTimedRow{title: it.Title, desc: it.Description, prompt: it.Prompt,
			start: start, end: end, sortAt: start, hasEnd: true})
	}
	for _, f := range fixed {
		start, err := time.Parse(time.RFC3339, f.StartAt)
		if err != nil {
			continue
		}
		r := hackTimedRow{key: f.Key, title: f.Title, desc: f.Description, prompt: f.Prompt,
			start: start, sortAt: hackFixedSortTime(f, loc), allDay: f.AllDay, fixed: true}
		if f.EndAt != nil {
			if end, err := time.Parse(time.RFC3339, *f.EndAt); err == nil {
				r.end, r.hasEnd = end, true
			}
		}
		rows = append(rows, r)
	}
	return rows
}

// hackCuePhrases is what the cue says about a fixed row, before and while it
// is "now"; any other row uses its own title.
var hackCuePhrases = map[string][2]string{
	"entry_deadline": {"Entries were due at ", "Entries are due now"},
	"event_start":    {"The event started at ", "The event starts now"},
	"event_end":      {"The event ended at ", "The event ends now"},
}

// hackStepMove is the schedule move a next step proposes: a stage, or
// publish_results; "" for anything else.
func hackStepMove(step map[string]any) string {
	if step["kind"] == "publish_results" {
		return "publish_results"
	}
	if step["kind"] != "stage" {
		return ""
	}
	move, _ := step["stage"].(string)
	return move
}

// hackDueRow is the latest-starting row that prompts move and whose time has
// come; the first such row wins a tie.
func hackDueRow(rows []hackTimedRow, move string, now time.Time) (hackTimedRow, bool) {
	var best hackTimedRow
	found := false
	for _, r := range rows {
		if r.prompt != move || r.allDay || r.start.After(now) {
			continue
		}
		if !found || r.start.After(best.start) {
			best, found = r, true
		}
	}
	return best, found
}

func hackCueText(r hackTimedRow, isNow bool, at, label string) string {
	past, present := r.title+" was at ", r.title+" is now"
	if p, ok := hackCuePhrases[r.key]; ok {
		past, present = p[0], p[1]
	}
	if isNow {
		return present + ". " + label + "?"
	}
	return past + at + ". " + label + "?"
}

// hackScheduleCue is the next step's schedule cue: the most recent row
// (editable or fixed) that prompts this very move and whose time has come.
func hackScheduleCue(step map[string]any, free []hackScheduleItem, fixed []hackFixedRow, loc *time.Location, now time.Time) map[string]any {
	move := hackStepMove(step)
	label, ok := events.ScheduleMoves[move]
	if !ok || step["blocked"] != nil {
		return nil
	}
	best, ok := hackDueRow(hackTimedRows(free, fixed, loc), move, now)
	if !ok {
		return nil
	}
	isNow := now.Before(best.start.Add(hackCuePointWindow))
	var endAt any
	if best.hasEnd {
		isNow = now.Before(best.end)
		endAt = best.end.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"key":      best.key,
		"title":    best.title,
		"start_at": best.start.UTC().Format(time.RFC3339),
		"end_at":   endAt,
		"fixed":    best.fixed,
		"now":      isNow,
		"text":     hackCueText(best, isNow, hackCueTime(best.start, now, loc), label),
	}
}

func (h *views) scheduleCue(ctx context.Context, ev model.Event, step map[string]any) map[string]any {
	content, err := h.events.EventContent(ctx, ev.ID)
	if err != nil {
		return nil
	}
	now := time.Now()
	fixed := hackFixedSchedule(hackFixedInputs{Event: ev, Organiser: true, Now: now})
	return hackScheduleCue(step, content.Schedule, fixed, hackEventLoc(ev), now)
}

// fixedScheduleFor is the fixed rows for one reader: the organiser (with
// prompts), a participant (with their team's own deadline) or the public.
func (h *publicEventHTTP) fixedScheduleFor(ctx context.Context, ev model.Event, member *model.EventMember, organiser bool) ([]hackFixedRow, error) {
	in := hackFixedInputs{Event: ev, Organiser: organiser, Now: time.Now()}
	voting, err := h.entries.VotingSettings(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	in.Voting = &voting
	if in.Results, err = h.entries.EventResults(ctx, ev.ID); err != nil {
		return nil, err
	}
	if member != nil && member.Role == "participant" && member.TeamID.Valid {
		if st, err := h.teams.TeamWriteStateForLookup(ctx, member.TeamID.String, false); err == nil {
			in.Team = &st
		}
	}
	return hackFixedSchedule(in), nil
}

// hackPublicSchedule drops the organiser's prompts from editable rows for
// everyone else.
func hackPublicSchedule(items []hackScheduleItem) []hackScheduleItem {
	out := make([]hackScheduleItem, len(items))
	for i, it := range items {
		it.Prompt = ""
		out[i] = it
	}
	return out
}
