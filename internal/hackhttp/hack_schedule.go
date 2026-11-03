package hackhttp

import (
	"context"
	"sort"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/model"
)

// A schedule that acts (owner, 2026-11-02). The event's real times -- start
// and end, the entry deadline (a team's own extension in its own view), the
// voting window and the moment results were published -- show in the
// schedule as fixed rows. They are computed from the settings each time, never
// stored, so a fixed row can never disagree with the setting it comes from.
// The organiser changes them where they are set (set_in), not in Schedule.
//
// A row can also line up with a forward stage move (prompt). When that row's
// time comes, the organiser's next step says so ("Kickoff is now. Open
// sign-up?"). It only asks: nothing here ever moves a stage, because a late
// kickoff must not close entries early.

// hackScheduleMoves are the forward moves a schedule row may prompt: a stage
// next_step would move to, or publish_results. Ending the event is left out
// on purpose, since it is final.
var hackScheduleMoves = map[string]string{
	"open":            "Open sign-up",
	"building":        "Start building",
	"closed":          "Close entries",
	"judging":         "Start judging",
	"results":         "Finish judging",
	"publish_results": "Publish results",
}

// hackFixedRow is one derived, read-only schedule row. EndAt is nil for a
// moment (a deadline) and set for a window (voting). AllDay marks an event
// start or end saved as a date only (midnight in the event's zone): it shows
// the date with no time and never nudges anyone.
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

// hackFixedInputs is what the fixed rows are computed from. Voting is nil
// when the caller does not need the voting row (the next-step cue). Team is
// the caller's own team state, for a participant on a team.
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

// hackEventOver and hackMidnight moved to internal/rules/hackevents
// (EventOver, Midnight) in cleanup 3.2 slice 4: pure, no DB/HTTP/clock, and
// EventOver is also the directory's past-group decision now in
// internal/app/hack/events.

// hackFixedSchedule builds the fixed rows, sorted by time. Prompts are only
// filled in for organisers.
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
		// With no entry deadline, the event's end is when entries are
		// meant to close.
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

// hackScheduleCue is the next step's schedule cue: the most recent row
// (editable or fixed) that prompts this very move and whose time has come.
// nil when none. It never moves anything.
func hackScheduleCue(step map[string]any, free []hackScheduleItem, fixed []hackFixedRow, loc *time.Location, now time.Time) map[string]any {
	move := ""
	switch step["kind"] {
	case "stage":
		move, _ = step["stage"].(string)
	case "publish_results":
		move = "publish_results"
	}
	label, ok := hackScheduleMoves[move]
	if !ok || step["blocked"] != nil {
		return nil
	}
	type cand struct {
		key, title    string
		start, end    time.Time
		hasEnd, fixed bool
	}
	var best *cand
	consider := func(c cand) {
		if c.start.After(now) {
			return
		}
		if best == nil || c.start.After(best.start) {
			cc := c
			best = &cc
		}
	}
	for _, it := range free {
		if it.Prompt != move {
			continue
		}
		s, e1 := time.Parse(time.RFC3339, it.StartAt)
		e, e2 := time.Parse(time.RFC3339, it.EndAt)
		if e1 != nil || e2 != nil {
			continue
		}
		consider(cand{title: it.Title, start: s, end: e, hasEnd: true})
	}
	for _, r := range fixed {
		if r.Prompt != move || r.AllDay {
			continue
		}
		s, err := time.Parse(time.RFC3339, r.StartAt)
		if err != nil {
			continue
		}
		c := cand{key: r.Key, title: r.Title, start: s, fixed: true}
		if r.EndAt != nil {
			if e, err := time.Parse(time.RFC3339, *r.EndAt); err == nil {
				c.end, c.hasEnd = e, true
			}
		}
		consider(c)
	}
	if best == nil {
		return nil
	}
	isNow := now.Before(best.start.Add(hackCuePointWindow))
	if best.hasEnd {
		isNow = now.Before(best.end)
	}
	at := hackCueTime(best.start, now, loc)
	var phrase string
	switch best.key {
	case "entry_deadline":
		phrase = "Entries were due at " + at
		if isNow {
			phrase = "Entries are due now"
		}
	case "event_start":
		phrase = "The event started at " + at
		if isNow {
			phrase = "The event starts now"
		}
	case "event_end":
		phrase = "The event ended at " + at
		if isNow {
			phrase = "The event ends now"
		}
	default:
		phrase = best.title + " was at " + at
		if isNow {
			phrase = best.title + " is now"
		}
	}
	var endAt any
	if best.hasEnd {
		endAt = best.end.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"key":      best.key,
		"title":    best.title,
		"start_at": best.start.UTC().Format(time.RFC3339),
		"end_at":   endAt,
		"fixed":    best.fixed,
		"now":      isNow,
		"text":     phrase + ". " + label + "?",
	}
}

// scheduleCue loads what the organiser's cue needs. Errors leave it out:
// the next step itself never depends on the schedule.
func (h *HackHandler) scheduleCue(ctx context.Context, ev model.Event, step map[string]any) map[string]any {
	content, err := readHackContent(ctx, h.database, ev.ID)
	if err != nil {
		return nil
	}
	now := time.Now()
	fixed := hackFixedSchedule(hackFixedInputs{Event: ev, Organiser: true, Now: now})
	return hackScheduleCue(step, content.Schedule, fixed, hackEventLoc(ev), now)
}

// fixedScheduleFor is the fixed rows for one reader: the organiser (with
// prompts), a participant (with their team's own deadline) or the public.
func (h *HackHandler) fixedScheduleFor(ctx context.Context, ev model.Event, member *model.EventMember, organiser bool) ([]hackFixedRow, error) {
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
		if st, err := h.teamsSvc().TeamWriteStateForLookup(ctx, member.TeamID.String, false); err == nil {
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
