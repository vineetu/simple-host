package teams

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/host/rules/sites"
	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

type Error = hackmodel.Error

func InternalError(err error) error { return &Error{Code: "internal", Message: err.Error()} }

// The event_closed wordings of the organiser team routes.
const (
	msgEnded    = "an ended event cannot be changed"
	msgArchived = "an archived event cannot be changed"
)

func errTeamsLocked() error {
	return &Error{Code: "teams_locked", Message: "teams cannot be changed at this stage"}
}

// errEventNotFound is the 404 that never confirms where someone belongs.
func ErrEventNotFound() error { return &Error{Code: "event_not_found", Message: "event not found"} }

func errDeadlinePassed() error {
	return &Error{Code: "submissions_closed", Message: "the submission deadline has passed"}
}

// participantStage refuses a participant team change outside the
// team-building stages.
func ParticipantStage(stage string) error {
	if !hackteams.ParticipantTeamStages(stage) {
		return errTeamsLocked()
	}
	return nil
}

// organiserStage refuses an organiser team write in an archived event;
// msg is the route's wording of event_closed.
func organiserStage(stage, msg string) error {
	if stage == "archived" {
		return &Error{Code: "event_closed", Message: msg}
	}
	return nil
}

func IsID(s string) bool { return sites.UUIDShape.MatchString(s) }

func normSlug(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func notFoundAs(err, want error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return want
	}
	return err
}

func CheckLine(s, field string, min, max int) (string, error) {
	v, e := eventapp.Line(s, field, min, max)
	if e != nil {
		return "", &Error{Code: e.Code, Message: e.Message}
	}
	return v, nil
}

// optionalLine is checkLine on a field that may be absent (nil), after
// stripping invisible characters.
func optionalLine(p *string, field string, min, max int) (*string, error) {
	if p == nil {
		return nil, nil
	}
	v, err := CheckLine(eventapp.StripInvisible(*p), field, min, max)
	if err != nil {
		return nil, err
	}
	return &v, nil
}
