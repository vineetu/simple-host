package hackevents

import (
	"database/sql"
	"time"
)

// Midnight reports whether t, read in loc, is exactly local midnight: an
// event start/end saved as a date only (no time of day) always lands here.
// Was hackhttp's hack_schedule.go hackMidnight; moved in cleanup 3.2 slice 4
// since it is pure (no DB/HTTP/clock).
func Midnight(t time.Time, loc *time.Location) bool {
	l := t.In(loc)
	return l.Hour() == 0 && l.Minute() == 0 && l.Second() == 0
}

// EventOver is the instant an event's end has passed. Basics saves the end
// as a date only, stored as that day's midnight in the event's zone; a
// date-only end means the whole of that day, so it is over at the next
// midnight (full-test fix, 2026-11-02). An end with a time of day is over at
// that time. Was hackhttp's hack_schedule.go hackEventOver; moved in cleanup
// 3.2 slice 4.
func EventOver(end time.Time, zone string) time.Time {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" {
		loc = time.UTC
	}
	if Midnight(end, loc) {
		l := end.In(loc)
		return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc)
	}
	return end
}

// DirectoryGroup is GET /v1/hack/directory's now/upcoming/past choice for
// one event: "past" once it is archived or its end (via EventOver) has
// passed, "upcoming" when its start is still ahead, else "now". Was
// hackhttp's getDirectory (hack_voting.go) inline group logic; moved in
// cleanup 3.2 slice 4 as the directory's other pure decision, alongside
// EventOver and ApplicationsOpenNow.
func DirectoryGroup(stage string, endsAt sql.NullTime, zone string, startsAt sql.NullTime, now time.Time) string {
	if stage == "archived" || (endsAt.Valid && !now.Before(EventOver(endsAt.Time, zone))) {
		return "past"
	}
	if startsAt.Valid && now.Before(startsAt.Time) {
		return "upcoming"
	}
	return "now"
}
