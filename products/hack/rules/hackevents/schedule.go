package hackevents

import (
	"database/sql"
	"time"
)

// Midnight reports whether t, read in loc, is exactly local midnight: an event start/end saved as a date only (no time of day) always lands here.
func Midnight(t time.Time, loc *time.Location) bool {
	l := t.In(loc)
	return l.Hour() == 0 && l.Minute() == 0 && l.Second() == 0
}

// EventOver is the instant an event's end has passed.
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

// DirectoryGroup is GET /v1/hack/directory's now/upcoming/past choice for one event.
func DirectoryGroup(stage string, endsAt sql.NullTime, zone string, startsAt sql.NullTime, now time.Time) string {
	if stage == "archived" || (endsAt.Valid && !now.Before(EventOver(endsAt.Time, zone))) {
		return "past"
	}
	if startsAt.Valid && now.Before(startsAt.Time) {
		return "upcoming"
	}
	return "now"
}
