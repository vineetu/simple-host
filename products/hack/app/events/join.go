package events

import (
	"context"
	"database/sql"
	"errors"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// signupValidator checks registration answers against the event's questions.
type signupValidator = func([]model.SignupQuestion, map[string]string) ([]model.SignupAnswer, error)

var (
	errInvalidCode   = &Error{Code: "invalid_code", Message: "invalid code"}
	errAlreadyMember = &Error{Code: "already_member", Message: "you already have a different role in this event"}
)

// JoinByCode joins an event as participant or judge.
func (s *Service) JoinByCode(ctx context.Context, code, userID, display string, judge bool, input map[string]string, validate signupValidator) (model.Event, model.EventMember, bool, error) {
	ev, err := s.eventByCode(ctx, code, judge)
	if err == nil {
		err = joinRefusal(ev, judge)
	}
	if err != nil {
		return ev, model.EventMember{}, false, err
	}
	existing, err := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		member, err := s.addMember(ctx, ev.ID, userID, display, judge, input, validate)
		return ev, member, false, err
	case err != nil:
		return ev, model.EventMember{}, false, err
	case existing.Role != joinRole(judge):
		return ev, model.EventMember{}, false, errAlreadyMember
	}
	member, err := s.rejoin(ctx, ev.ID, userID, display)
	return ev, member, member.Role == "organiser", err
}

// eventByCode finds the event a join or judge code belongs to.
func (s *Service) eventByCode(ctx context.Context, code string, judge bool) (model.Event, error) {
	lookup := eventrepo.GetEventByJoinCode
	if judge {
		lookup = eventrepo.GetEventByJudgeCode
	}
	ev, err := lookup(ctx, s.db, code)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, errInvalidCode
	}
	return ev, err
}

// joinRole is the membership role a join creates.
func joinRole(judge bool) string {
	if judge {
		return "judge"
	}
	return "participant"
}

// joinRefusal says why ev cannot take a new member in this role, or nil.
func joinRefusal(ev model.Event, judge bool) error {
	switch {
	case ev.TakenDown():
		return &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	case judge && (ev.Stage == "results" || ev.Stage == "archived"):
		return &Error{Code: "judging_closed", Message: "judging is closed for this event"}
	case !judge && ev.Stage != "open" && ev.Stage != "building":
		return &Error{Code: "joining_closed", Message: "joining is closed for this event"}
	}
	return nil
}

// rejoin fills a returning member's name and returns their membership as it stands.
func (s *Service) rejoin(ctx context.Context, eventID, userID, display string) (model.EventMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventMember{}, err
	}
	defer tx.Rollback()
	if err := EnsureAccountName(ctx, tx, userID, display); err != nil {
		return model.EventMember{}, err
	}
	member, err := eventrepo.GetEventMember(ctx, tx, eventID, userID)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return model.EventMember{}, err
	}
	return member, nil
}

// addMember inserts a new membership.
func (s *Service) addMember(ctx context.Context, eventID, userID, display string, judge bool, input map[string]string, validate signupValidator) (model.EventMember, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventMember{}, err
	}
	defer tx.Rollback()
	locked, err := eventrepo.GetEventForShare(ctx, tx, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.EventMember{}, errInvalidCode
	}
	if err == nil {
		err = joinRefusal(locked, judge)
	}
	if err != nil {
		return model.EventMember{}, err
	}
	var answers []model.SignupAnswer
	required := false
	if !judge {
		if answers, required, err = registrationAnswers(ctx, tx, eventID, input, validate); err != nil {
			return model.EventMember{}, err
		}
	}
	if err := EnsureAccountName(ctx, tx, userID, display); err != nil {
		return model.EventMember{}, err
	}
	member, err := insertJoin(ctx, tx, eventID, userID, judge, answers, required)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return s.settleJoinRace(ctx, eventID, userID, joinRole(judge), err)
	}
	return member, nil
}

// registrationAnswers validates a participant's sign-up answers and reports whether the event requires approval.
func registrationAnswers(ctx context.Context, tx *sql.Tx, eventID string, input map[string]string, validate signupValidator) ([]model.SignupAnswer, bool, error) {
	questions, required, err := eventrepo.RegistrationSettings(ctx, tx, eventID)
	if err != nil {
		return nil, false, err
	}
	answers, err := validate(questions, input)
	return answers, required, err
}

// insertJoin writes the membership row; a sign-up that waits for approval also notifies the organisers.
func insertJoin(ctx context.Context, tx *sql.Tx, eventID, userID string, judge bool, answers []model.SignupAnswer, required bool) (model.EventMember, error) {
	if judge {
		return eventrepo.InsertEventMember(ctx, tx, eventID, userID, "judge")
	}
	member, err := eventrepo.JoinParticipantRegistration(ctx, tx, eventID, userID, answers, required)
	if err == nil && required {
		err = eventrepo.NotifyOrganisers(ctx, tx, eventID, eventrepo.NoticeApplicationNew, userID)
	}
	return member, err
}

// settleJoinRace resolves a failed insert.
func (s *Service) settleJoinRace(ctx context.Context, eventID, userID, role string, err error) (model.EventMember, error) {
	if !unique(err) {
		return model.EventMember{}, err
	}
	existing, err := eventrepo.GetEventMember(ctx, s.db, eventID, userID)
	if err == nil && existing.Role == role {
		return existing, nil
	}
	return model.EventMember{}, errAlreadyMember
}
