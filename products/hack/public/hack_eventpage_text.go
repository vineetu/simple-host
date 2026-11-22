package public

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/products/hack/app/events"
)

// hackEventLocation loads the event's IANA zone, falling back to UTC when it is empty or invalid.
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

// hackZoneAbbrFamiliar is the same fixed map as ui.zoneAbbr (host/handler/static/hack/ui.js) and the vote page's copy; keep in step by hand, since Go's tzdata and Intl disagree on which zones get a short code.
var hackZoneAbbrFamiliar = map[string]string{
	"Asia/Jakarta": "WIB", "Asia/Pontianak": "WIB",
	"Asia/Makassar":  "WITA",
	"Asia/Jayapura":  "WIT",
	"Asia/Tokyo":     "JST",
	"Asia/Seoul":     "KST",
	"Asia/Singapore": "SGT",
	"Asia/Kolkata":   "IST", "Asia/Calcutta": "IST",
}

// hackZoneAbbr is the short zone name for a moment in time ("WIB", "GMT").
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

// hackGMTOffsetLabel formats a UTC offset in seconds as "GMT", "GMT+9" or "GMT+5:30".
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
	// events.Midnight is the same local-midnight check the fixed-row classifier uses: hour, minute AND second, so a stray 00:00:30 doesn't read as date-only here while the classifier disagrees.
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

// hackScheduleViews sorts rows by start time, marks Now/Next, and shows the end date too when an item spans more than one day.
func hackScheduleViews(items []events.ScheduleItem, fixed []core.FixedRow, loc *time.Location, zoneFallback string) []hackScheduleView {
	rows := core.TimedRows(items, fixed, loc)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].SortAt.Before(rows[j].SortAt) })
	ongoing, next := hackScheduleMarks(rows, time.Now())
	var out []hackScheduleView
	for i, r := range rows {
		status := ""
		if i == ongoing {
			status = "Now"
		} else if i == next {
			status = "Next"
		}
		out = append(out, hackScheduleView{Title: r.Title, Description: r.Desc, When: hackScheduleWhen(r, loc, zoneFallback), Status: status, Fixed: r.Fixed})
	}
	return out
}

// hackScheduleMarks is the index of the first row under way now and of the
// soonest row still to come (-1 when none). Date-only rows are never either.
func hackScheduleMarks(rows []core.TimedRow, now time.Time) (ongoing, next int) {
	ongoing, next = -1, -1
	for i, r := range rows {
		if r.AllDay {
			continue
		}
		if ongoing < 0 && r.HasEnd && !now.Before(r.Start) && now.Before(r.End) {
			ongoing = i
		}
		if now.Before(r.Start) && (next < 0 || r.Start.Before(rows[next].Start)) {
			next = i
		}
	}
	return ongoing, next
}

// hackScheduleWhen is a row's time as the page prints it: a date for a
// date-only row, one time for a moment, otherwise the span (with both dates
// when it crosses midnight).
func hackScheduleWhen(r core.TimedRow, loc *time.Location, zoneFallback string) string {
	ls := r.Start.In(loc)
	switch {
	case r.AllDay:
		return ls.Format("Mon 2 Jan")
	case !r.HasEnd:
		return ls.Format("Mon 2 Jan, 3:04 pm") + " " + hackZoneAbbr(ls, zoneFallback)
	}
	le := r.End.In(loc)
	abbr := hackZoneAbbr(le, zoneFallback)
	if ls.Year() == le.Year() && ls.YearDay() == le.YearDay() {
		return ls.Format("Mon 2 Jan, 3:04 pm") + "–" + le.Format("3:04 pm") + " " + abbr
	}
	return ls.Format("Mon 2 Jan 3:04 pm") + " – " + le.Format("Mon 2 Jan 3:04 pm") + " " + abbr
}

// hackCountdownText is the server-rendered, non-live value the countdown script enhances in place.
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

// trimScoreZeros trims trailing zeros and a bare trailing "." from a formatted decimal; it never rounds.
func trimScoreZeros(s string) string {
	if s == "" || !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// hackInitials is the first letter of up to the first two words of name.
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
