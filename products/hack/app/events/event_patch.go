package events

import (
	"database/sql"
	"slices"
	"strings"
	"time"

	authrules "github.com/vsriram/simple-host/host/access/rules"
	model "github.com/vsriram/simple-host/products/hack/model"
)

// EventPatch is one change to an event. A nil field is absent and keeps its
// stored value; an empty string clears a field that can be empty.
type EventPatch struct {
	AccentColor, Theme, Title, Tagline, About, Rules, Prizes, CocText, TimeZone, StartsAt, EndsAt *string
	TeamFinding, ApplicationsOpen                                                                 *bool
	TeamSizeMax                                                                                   *int
	OrganiserName, Organisation, ContactEmail, Purpose                                            *string
	ExpectedParticipants                                                                          *int
	SubmissionDeadline                                                                            *string
	EntryRequired, OverallPrizes                                                                  *[]string
	GalleryOpen, SubmissionsPublic                                                                *bool
	JudgingEndsAt                                                                                 *string
}

// Crayons are the accent colours an organiser can choose, by stored name.
var Crayons = []struct{ Name, Color string }{
	{"red", "#d15d48"}, {"teal", "#267b79"}, {"indigo", "#4a49a8"},
	{"orange", "#e9a45f"}, {"yellow", "#f2c94c"}, {"grass", "#86ab74"}, {"blush", "#ee8f92"},
}

// Themes are the public event page's whole-page styles (SIM-353). ""
// (Classic) is always valid and not listed. The CSS for each lives in
// products/hack/webassets/static/hack/themes.css.
var Themes = []string{"poster", "confetti", "boardroom", "garden", "terminal"}

// The event field validators, shared by create and patch.

func EventTitle(s string) (string, error)         { return lineValue(s, "title", 1, 120) }
func EventOrganiserName(s string) (string, error) { return lineValue(s, "organiser_name", 1, 100) }
func EventPurpose(s string) (string, error)       { return textValue(s, "purpose", 1, 1000) }

// hackOrganisationHintPaste is the organisation field's own guidance text,
// seen saved as its value when a client copied the hint: cleared, not stored.
const hackOrganisationHintPaste = "write the name of your company, college, or group (for example: futuretech or your organization's name)"

func EventOrganisation(s string) (string, error) {
	if strings.ToLower(strings.TrimSpace(s)) == hackOrganisationHintPaste {
		s = ""
	}
	return lineValue(s, "organisation", 0, 120)
}

func EventContactEmail(s string) (string, error) {
	addr, ok := authrules.NormalEmail(s)
	if !ok {
		return "", &Error{Code: "invalid_contact_email", Message: "contact_email must be a valid email address"}
	}
	return addr, nil
}

func EventExpectedParticipants(n int) error {
	if n < 1 || n > 100000 {
		return &Error{Code: "invalid_expected_participants", Message: "expected_participants must be between 1 and 100000"}
	}
	return nil
}

// EventTimeZone is the trimmed IANA zone, UTC when empty.
func EventTimeZone(s string) (string, *time.Location, error) {
	tz := strings.TrimSpace(s)
	if tz == "" {
		tz = "UTC"
	}
	invalid := &Error{Code: "invalid_time_zone", Message: "time_zone is not a valid IANA name"}
	if tz == "Local" {
		return "", nil, invalid
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return "", nil, invalid
	}
	return tz, loc, nil
}

// EventEnds refuses an end before the start. A date-only end (local
// midnight) means the whole of that day (EventOver), so a same-day timed
// start is not "after" it.
func EventEnds(starts, ends sql.NullTime, zone string) error {
	if ends.Valid && starts.Valid && EventOver(ends.Time, zone).Before(starts.Time) {
		return &Error{Code: "invalid_ends_at", Message: "ends_at must not be before starts_at"}
	}
	return nil
}

// ParseEventTime reads a required event time: YYYY-MM-DD, a wall-clock
// YYYY-MM-DDTHH:MM[:SS] in loc, or RFC 3339.
func ParseEventTime(s string, loc *time.Location, field string) (sql.NullTime, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullTime{}, &Error{Code: "invalid_" + field, Message: field + " is required"}
	}
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		t, err := time.ParseInLocation("2006-01-02", s, loc)
		if err != nil {
			return sql.NullTime{}, &Error{Code: "invalid_" + field, Message: field + " must be RFC 3339 or YYYY-MM-DD"}
		}
		return sql.NullTime{Time: t, Valid: true}, nil
	}
	// A wall-clock time in the event's zone, as a datetime-local field sends it.
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if len(s) == len(layout) {
			if t, err := time.ParseInLocation(layout, s, loc); err == nil {
				// A wall-clock time the clocks skip (a daylight-saving gap)
				// does not exist in that zone: say so rather than move it.
				if t.In(loc).Format(layout) != s {
					return sql.NullTime{}, &Error{Code: "invalid_" + field, Message: field + " is a time the clocks skip in " + loc.String() + "; pick another"}
				}
				return sql.NullTime{Time: t, Valid: true}, nil
			}
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		return sql.NullTime{}, &Error{Code: "invalid_" + field, Message: field + " must be RFC 3339, YYYY-MM-DDTHH:MM in the event's time zone, or YYYY-MM-DD"}
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// EventOverallPrizes is at most three single-line prizes, trailing blanks dropped.
func EventOverallPrizes(in []string) ([]string, error) {
	invalid := &Error{Code: "invalid_overall_prizes", Message: "overall_prizes takes at most three single-line prizes of up to 60 characters"}
	if len(in) > 3 {
		return nil, invalid
	}
	out := make([]string, 0, len(in))
	for _, prize := range in {
		prize = strings.TrimSpace(prize)
		if strings.ContainsAny(prize, "\r\n") || len([]rune(prize)) > 60 {
			return nil, invalid
		}
		out = append(out, prize)
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out, nil
}

// eventEntryRequired is the required entry fields in canonical order, never nil.
func eventEntryRequired(in []string) ([]string, error) {
	want := map[string]bool{}
	for _, f := range in {
		f = strings.TrimSpace(f)
		if !slices.Contains(EntryFields, f) {
			return nil, &Error{Code: "invalid_entry_field", Message: "entry_required takes any of: " + strings.Join(EntryFields, ", ")}
		}
		want[f] = true
	}
	out := []string{}
	for _, k := range EntryFields {
		if want[k] {
			out = append(out, k)
		}
	}
	return out, nil
}

func lineValue(s, field string, min, max int) (string, error) {
	value, e := Line(s, field, min, max)
	if e != nil {
		return "", &Error{Code: e.Code, Message: e.Message}
	}
	return value, nil
}

func textValue(s, field string, min, max int) (string, error) {
	value, e := Text(s, field, min, max)
	if e != nil {
		return "", &Error{Code: e.Code, Message: e.Message}
	}
	return value, nil
}

// set validates a present field into dst; an absent one leaves dst alone.
func set[In, Out any](dst *Out, in *In, valid func(In) (Out, error)) error {
	if in == nil {
		return nil
	}
	v, err := valid(*in)
	if err == nil {
		*dst = v
	}
	return err
}

func accept[T any](v T) (T, error) { return v, nil }

// first is the first refusal of fields checked in wire order: every argument
// is evaluated, left to right, and a refused patch is never stored.
func first(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// apply validates p against the locked row ev and writes it in, field by
// field in wire order; the first refusal wins. It reports whether the
// submission deadline and the required entry fields were sent.
func (p EventPatch) apply(ev *model.Event) (deadlineChanged, entryRequiredChanged bool, err error) {
	line := func(field string, min, max int) func(string) (string, error) {
		return func(s string) (string, error) { return lineValue(s, field, min, max) }
	}
	text := func(field string, min, max int) func(string) (string, error) {
		return func(s string) (string, error) { return textValue(s, field, min, max) }
	}
	oneOf := func(names []string, refusal *Error) func(string) (string, error) {
		return func(s string) (string, error) {
			if s != "" && !slices.Contains(names, s) {
				return "", refusal
			}
			return s, nil
		}
	}
	crayons := []string{}
	for _, c := range Crayons {
		crayons = append(crayons, c.Name)
	}
	oldZone := ev.TimeZone
	if err := first(
		set(&ev.AccentColor, p.AccentColor, oneOf(crayons, &Error{Code: "invalid_accent_color", Message: "choose a crayon colour or an empty string for automatic"})),
		set(&ev.Theme, p.Theme, oneOf(Themes, &Error{Code: "invalid_theme", Message: "choose poster, confetti, boardroom, garden, terminal, or an empty string for the classic look"})),
		set(&ev.Title, p.Title, EventTitle),
		set(&ev.Tagline, p.Tagline, line("tagline", 0, 160)),
		set(&ev.About, p.About, text("about", 0, 10000)),
		set(&ev.Rules, p.Rules, text("rules", 0, 10000)),
		set(&ev.OverallPrizes, p.OverallPrizes, EventOverallPrizes),
		set(&ev.Prizes, p.Prizes, text("prizes", 0, 5000)),
		set(&ev.CocText, p.CocText, text("coc_text", 0, 10000)),
		set(&ev.TimeZone, p.TimeZone, func(s string) (string, error) { tz, _, err := EventTimeZone(s); return tz, err }),
	); err != nil {
		return false, false, err
	}
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil {
		return false, false, &Error{Code: "invalid_time_zone", Message: "time_zone is not a valid IANA name"}
	}
	// A new zone without new dates keeps the same calendar dates: each
	// stored instant is moved to the same wall-clock time in the new zone.
	if old, oerr := time.LoadLocation(oldZone); ev.TimeZone != oldZone && oerr == nil {
		reanchor := func(t sql.NullTime) sql.NullTime {
			if !t.Valid {
				return t
			}
			o := t.Time.In(old)
			return sql.NullTime{Time: time.Date(o.Year(), o.Month(), o.Day(), o.Hour(), o.Minute(), o.Second(), 0, loc), Valid: true}
		}
		if p.StartsAt == nil {
			ev.StartsAt = reanchor(ev.StartsAt)
		}
		if p.EndsAt == nil {
			ev.EndsAt = reanchor(ev.EndsAt)
		}
	}
	// A clearable time: "" clears it. The deadline is an instant, so a new
	// zone never moves it (a deadline that passed must stay passed).
	clearable := func(field string) func(string) (sql.NullTime, error) {
		return func(s string) (sql.NullTime, error) {
			if strings.TrimSpace(s) == "" {
				return sql.NullTime{}, nil
			}
			return ParseEventTime(s, loc, field)
		}
	}
	err = first(
		set(&ev.StartsAt, p.StartsAt, func(s string) (sql.NullTime, error) { return ParseEventTime(s, loc, "starts_at") }),
		set(&ev.EndsAt, p.EndsAt, clearable("ends_at")),
		func() error { return EventEnds(ev.StartsAt, ev.EndsAt, ev.TimeZone) }(),
		set(&ev.TeamFinding, p.TeamFinding, accept[bool]),
		set(&ev.ApplicationsOpen, p.ApplicationsOpen, func(open bool) (bool, error) {
			if ev.Stage == "archived" {
				return false, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
			}
			return open, nil
		}),
		set(&ev.TeamSizeMax, p.TeamSizeMax, func(n int) (int, error) {
			if n < 1 || n > 50 {
				return 0, &Error{Code: "invalid_team_size", Message: "team_size_max must be between 1 and 50"}
			}
			return n, nil
		}),
		set(&ev.OrganiserName, p.OrganiserName, EventOrganiserName),
		set(&ev.Organisation, p.Organisation, EventOrganisation),
		set(&ev.ContactEmail, p.ContactEmail, EventContactEmail),
		set(&ev.Purpose, p.Purpose, EventPurpose),
		set(&ev.ExpectedParticipants, p.ExpectedParticipants, func(n int) (sql.NullInt64, error) {
			return sql.NullInt64{Int64: int64(n), Valid: true}, EventExpectedParticipants(n)
		}),
		set(&ev.SubmissionDeadline, p.SubmissionDeadline, clearable("submission_deadline")),
		set(&ev.EntryRequired, p.EntryRequired, eventEntryRequired),
		set(&ev.SubmissionsPublic, p.SubmissionsPublic, accept[bool]),
		set(&ev.GalleryOpen, p.GalleryOpen, accept[bool]),
		set(&ev.JudgingEndsAt, p.JudgingEndsAt, clearable("judging_ends_at")),
	)
	return p.SubmissionDeadline != nil, p.EntryRequired != nil, err
}
