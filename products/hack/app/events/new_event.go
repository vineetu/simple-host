package events

import (
	"database/sql"
	"strings"
)

// NewEventInput is an organiser's new-event form as sent.
type NewEventInput struct {
	Slug                 string
	Title                string
	OrganiserName        string
	Organisation         string
	ContactEmail         string
	Purpose              string
	ExpectedParticipants *int
	StartsAt             string
	EndsAt               string
	TimeZone             string
}

// NewEvent is a checked new-event form.
type NewEvent struct {
	Title, OrganiserName, Organisation, ContactEmail, Purpose, TimeZone string
	ExpectedParticipants                                                int
	StartsAt, EndsAt                                                    sql.NullTime
}

// CheckNewEvent checks every field of the form, in the form's order, and answers the first refusal.
func CheckNewEvent(in NewEventInput) (NewEvent, error) {
	var out NewEvent
	if in.ExpectedParticipants != nil {
		out.ExpectedParticipants = *in.ExpectedParticipants
	}
	err := firstFailure(
		func() (err error) { out.Title, err = EventTitle(in.Title); return },
		func() (err error) { out.OrganiserName, err = EventOrganiserName(in.OrganiserName); return },
		func() (err error) { out.Organisation, err = EventOrganisation(in.Organisation); return },
		func() (err error) { out.ContactEmail, err = EventContactEmail(in.ContactEmail); return },
		func() (err error) { out.Purpose, err = EventPurpose(in.Purpose); return },
		func() error { return EventExpectedParticipants(out.ExpectedParticipants) },
		func() error { return out.checkTimes(in) },
	)
	return out, err
}

// checkTimes checks the zone, the start and the optional end.
func (out *NewEvent) checkTimes(in NewEventInput) error {
	tz, loc, err := EventTimeZone(in.TimeZone)
	if err != nil {
		return err
	}
	out.TimeZone = tz
	if out.StartsAt, err = ParseEventTime(in.StartsAt, loc, "starts_at"); err != nil {
		return err
	}
	if strings.TrimSpace(in.EndsAt) == "" {
		return nil
	}
	if out.EndsAt, err = ParseEventTime(in.EndsAt, loc, "ends_at"); err != nil {
		return err
	}
	return EventEnds(out.StartsAt, out.EndsAt, tz)
}

// firstFailure runs steps in order and stops at the first error.
func firstFailure(steps ...func() error) error {
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}
