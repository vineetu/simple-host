package hackhttp

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/products/hack/app/events"
)

// hackEventLocation loads the event's IANA zone, falling back to UTC (and
// reporting "UTC" as the fallback name) when it is empty or invalid.
func hackEventLocation(zone string) (*time.Location, string) {
	loc, err := time.LoadLocation(zone)
	if err != nil || loc == nil {
		return time.UTC, "UTC"
	}
	name := zone
	if name == "" {
		name = loc.String()
	}
	return loc, name
}

var hackNumericZoneRe = regexp.MustCompile(`^[+-]\d`)

// hackZoneAbbrFamiliar is the same fixed map ui.zoneAbbr (host/handler/
// static/hack/ui.js) and the vote page's own copy of it use -- kept in step by
// hand, since Go's tzdata and Intl don't always agree on which zones get a real
// short code.
var hackZoneAbbrFamiliar = map[string]string{
	"Asia/Jakarta": "WIB", "Asia/Pontianak": "WIB",
	"Asia/Makassar":  "WITA",
	"Asia/Jayapura":  "WIT",
	"Asia/Tokyo":     "JST",
	"Asia/Seoul":     "KST",
	"Asia/Singapore": "SGT",
	"Asia/Kolkata":   "IST", "Asia/Calcutta": "IST",
}

// hackZoneAbbr is the short zone name for a moment in time ("WIB", "GMT"), the
// way tech_notes asks for ("times in WIB").
func hackZoneAbbr(t time.Time, ianaFallback string) string {
	abbr, offsetSeconds := t.Zone()
	if abbr != "" && !hackNumericZoneRe.MatchString(abbr) {
		return abbr
	}
	if a, ok := hackZoneAbbrFamiliar[ianaFallback]; ok {
		return a
	}
	return hackGMTOffsetLabel(offsetSeconds)
}

// hackGMTOffsetLabel formats a UTC offset in seconds as "GMT", "GMT+9" or
// "GMT+5:30" -- the same shape ui.zoneAbbr's gmtOffsetLabel builds.
func hackGMTOffsetLabel(offsetSeconds int) string {
	if offsetSeconds == 0 {
		return "GMT"
	}
	sign := "+"
	mins := offsetSeconds / 60
	if mins < 0 {
		sign = "-"
		mins = -mins
	}
	h, m := mins/60, mins%60
	if m == 0 {
		return fmt.Sprintf("GMT%s%d", sign, h)
	}
	return fmt.Sprintf("GMT%s%d:%02d", sign, h, m)
}

func hackEventDateRange(starts, ends time.Time, loc *time.Location) string {
	var start, end time.Time
	if !starts.IsZero() {
		start = starts.In(loc)
	}
	if !ends.IsZero() {
		end = ends.In(loc)
	}
	if start.IsZero() && end.IsZero() {
		return ""
	}
	// events.Midnight is the same local-midnight check the fixed-row classifier
	// uses (hack_schedule.go hackFixedSchedule, via events.Midnight): hour, minute
	// AND second, so a stray 00:00:30 doesn't read as date-only here while the
	// classifier disagrees.
	startDateOnly := start.IsZero() || events.Midnight(start, loc)
	endDateOnly := end.IsZero() || events.Midnight(end, loc)
	if !startDateOnly || !endDateOnly {
		format := func(t time.Time, dateOnly bool) string {
			if t.IsZero() {
				return ""
			}
			if dateOnly {
				return t.Format("Mon 2 Jan 2006")
			}
			return t.Format("Mon 2 Jan 2006, 3:04 pm")
		}
		a, b := format(start, startDateOnly), format(end, endDateOnly)
		if a == "" {
			return b
		}
		if b == "" {
			return a
		}
		return a + " – " + b
	}
	if end.IsZero() {
		return start.Format("2 January 2006")
	}
	if start.IsZero() {
		return end.Format("2 January 2006")
	}
	return hackDateRange(start, end)
}

func hackDateRange(a, b time.Time) string {
	if a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day() {
		return a.Format("2 January 2006")
	}
	if a.Year() == b.Year() && a.Month() == b.Month() {
		return fmt.Sprintf("%d–%d %s %d", a.Day(), b.Day(), a.Month().String(), a.Year())
	}
	if a.Year() == b.Year() {
		return fmt.Sprintf("%d %s–%d %s %d", a.Day(), a.Month().String(), b.Day(), b.Month().String(), a.Year())
	}
	return a.Format("2 January 2006") + "–" + b.Format("2 January 2006")
}

// hackScheduleViews sorts rows by start time (Grok re-test cosmetic #4: rows
// used to list in whatever order the organiser entered them, so "Demos" could
// print above "Kickoff" and the Next badge landed on the wrong row), marks
// Now/Next, and shows the end date too when an item spans more than one day
// (tech_notes/fixes: "Multi-day schedule items showed only the end time").
func hackScheduleViews(items []hackScheduleItem, fixed []hackFixedRow, loc *time.Location, zoneFallback string) []hackScheduleView {
	rows := hackTimedRows(items, fixed, loc)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].sortAt.Before(rows[j].sortAt) })
	ongoing, next := hackScheduleMarks(rows, time.Now())
	var out []hackScheduleView
	for i, r := range rows {
		status := ""
		if i == ongoing {
			status = "Now"
		} else if i == next {
			status = "Next"
		}
		out = append(out, hackScheduleView{Title: r.title, Description: r.desc, When: hackScheduleWhen(r, loc, zoneFallback), Status: status, Fixed: r.fixed})
	}
	return out
}

// hackScheduleMarks is the index of the first row under way now and of the
// soonest row still to come (-1 when none). Date-only rows are never either.
func hackScheduleMarks(rows []hackTimedRow, now time.Time) (ongoing, next int) {
	ongoing, next = -1, -1
	for i, r := range rows {
		if r.allDay {
			continue
		}
		if ongoing < 0 && r.hasEnd && !now.Before(r.start) && now.Before(r.end) {
			ongoing = i
		}
		if now.Before(r.start) && (next < 0 || r.start.Before(rows[next].start)) {
			next = i
		}
	}
	return ongoing, next
}

// hackScheduleWhen is a row's time as the page prints it: a date for a
// date-only row, one time for a moment, otherwise the span (with both dates
// when it crosses midnight).
func hackScheduleWhen(r hackTimedRow, loc *time.Location, zoneFallback string) string {
	ls := r.start.In(loc)
	switch {
	case r.allDay:
		return ls.Format("Mon 2 Jan")
	case !r.hasEnd:
		return ls.Format("Mon 2 Jan, 3:04 pm") + " " + hackZoneAbbr(ls, zoneFallback)
	}
	le := r.end.In(loc)
	abbr := hackZoneAbbr(le, zoneFallback)
	if ls.Year() == le.Year() && ls.YearDay() == le.YearDay() {
		return ls.Format("Mon 2 Jan, 3:04 pm") + "–" + le.Format("3:04 pm") + " " + abbr
	}
	return ls.Format("Mon 2 Jan 3:04 pm") + " – " + le.Format("Mon 2 Jan 3:04 pm") + " " + abbr
}

// hackCountdownText is the server-rendered, non-live value the countdown
// script enhances in place (tech_notes: "the date alone without it").
func hackCountdownText(now, deadline time.Time) string {
	if !now.Before(deadline) {
		return "Entries are closed."
	}
	left := deadline.Sub(now)
	days := int(left.Hours()) / 24
	hours := int(left.Hours()) % 24
	minutes := int(left.Minutes()) % 60
	switch {
	case days > 0:
		return hackPlural(days, "day") + " " + hackPlural(hours, "hour") + " left"
	case hours > 0:
		return hackPlural(hours, "hour") + " " + hackPlural(minutes, "minute") + " left"
	case minutes > 0:
		return hackPlural(minutes, "minute") + " left"
	default:
		return "less than a minute left"
	}
}

func hackPlural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// hackScoreText formats a score for display: full precision (two decimals,
// matching the data everywhere else stores it), with trailing zeros trimmed so
// "100.00" reads "100" and "42.50" reads "42.5" (Grok SIM-346 Part B
// hack-event.html item 4) -- but, after review #8, never rounded to fewer
// decimals than it actually has: "81.25" must stay "81.25", not become "81.3"
// or "81.2".
func hackScoreText(score float64) string {
	return trimScoreZeros(strconv.FormatFloat(score, 'f', 2, 64))
}

// trimScoreZeros trims trailing zeros (then a bare trailing ".") off an
// already-formatted decimal string, for display -- it never rounds, so it
// can't drop a real digit the way reformatting at fewer decimal places can.
func trimScoreZeros(s string) string {
	if s == "" || !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// hackInitials is one or two upper-case letters for an avatar with no image:
// the first letter of up to the first two words, or just the first letter of
// a single word.
func hackInitials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var out []rune
	for _, f := range strings.Fields(name) {
		r := []rune(f)
		if len(r) == 0 {
			continue
		}
		out = append(out, []rune(strings.ToUpper(string(r[0])))...)
		if len(out) >= 2 {
			break
		}
	}
	if len(out) == 0 {
		r := []rune(name)
		return strings.ToUpper(string(r[0]))
	}
	return string(out)
}
