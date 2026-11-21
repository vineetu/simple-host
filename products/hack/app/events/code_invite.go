package events

import (
	"context"
	"database/sql"
	"errors"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// CodeInvite is what a join or judge code opens, for its preview page.
type CodeInvite struct {
	Event    model.Event
	Role     string
	Joinable bool
	// Questions and ApprovalRequired are the sign-up settings; judges have none.
	Questions        []model.SignupQuestion
	ApprovalRequired bool
}

// InviteByCode resolves a join code (judge false) or a judge code.
func (s *Service) InviteByCode(ctx context.Context, raw string, judge bool) (CodeInvite, error) {
	code := NormalizeCode(raw)
	if code == "" {
		return CodeInvite{}, errInvalidCode
	}
	lookup := eventrepo.GetEventByJoinCode
	if judge {
		lookup = eventrepo.GetEventByJudgeCode
	}
	ev, err := lookup(ctx, s.db, code)
	if errors.Is(err, sql.ErrNoRows) {
		return CodeInvite{}, errInvalidCode
	}
	if err != nil {
		return CodeInvite{}, err
	}
	if ev.TakenDown() {
		return CodeInvite{}, &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	if judge {
		return CodeInvite{Event: ev, Role: "judge", Joinable: ev.Stage != "results" && ev.Stage != "archived", Questions: []model.SignupQuestion{}}, nil
	}
	inv := CodeInvite{Event: ev, Role: "participant", Joinable: ev.Stage == "open" || ev.Stage == "building"}
	inv.Questions, inv.ApprovalRequired, err = eventrepo.RegistrationSettings(ctx, s.db, ev.ID)
	return inv, err
}
