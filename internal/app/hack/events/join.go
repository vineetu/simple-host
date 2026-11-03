package events

import (
	"context"
	"database/sql"
	"errors"

	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
)

// JoinByCode joins an event as participant or judge.
func (s *Service) JoinByCode(ctx context.Context, code, userID, display string, judge bool, input map[string]string, validate func([]model.SignupQuestion, map[string]string) ([]model.SignupAnswer, error)) (model.Event, model.EventMember, bool, error) {
	var ev model.Event
	var err error
	if judge {
		ev, err = eventrepo.GetEventByJudgeCode(ctx, s.db, code)
	} else {
		ev, err = eventrepo.GetEventByJoinCode(ctx, s.db, code)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ev, model.EventMember{}, false, &Error{"invalid_code", "invalid code"}
	}
	if err != nil {
		return ev, model.EventMember{}, false, err
	}
	if ev.TakenDown() {
		return ev, model.EventMember{}, false, &Error{"event_taken_down", "this event has been taken down"}
	}
	role := "participant"
	if judge {
		role = "judge"
		if ev.Stage == "results" || ev.Stage == "archived" {
			return ev, model.EventMember{}, false, &Error{"judging_closed", "judging is closed for this event"}
		}
	} else if ev.Stage != "open" && ev.Stage != "building" {
		return ev, model.EventMember{}, false, &Error{"joining_closed", "joining is closed for this event"}
	}
	existing, err := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
	if err == nil {
		if existing.Role == role {
			return ev, existing, existing.Role == "organiser", nil
		}
		return ev, model.EventMember{}, false, &Error{"already_member", "you already have a different role in this event"}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ev, model.EventMember{}, false, err
	}
	var member model.EventMember
	if judge {
		// Lock-order fix (SIM-340 2.4): a leave holds the event row FOR
		// UPDATE before the leaver's member row, so joining must take the
		// event row first too (FOR SHARE, as the participant branch below
		// does), never insert the member row with the event row unlocked.
		tx, txerr := s.db.BeginTx(ctx, nil)
		if txerr != nil {
			return ev, model.EventMember{}, false, txerr
		}
		defer tx.Rollback()
		locked, txerr := eventrepo.GetEventForShare(ctx, tx, ev.ID)
		if errors.Is(txerr, sql.ErrNoRows) {
			return ev, model.EventMember{}, false, &Error{"invalid_code", "invalid code"}
		}
		if txerr != nil {
			return ev, model.EventMember{}, false, txerr
		}
		// Revalidate against the state as of the lock (Codex review,
		// SIM-340): the pre-lock ev above could be stale if a last
		// organiser's "end" or "delete" was waiting on this same row and
		// committed while this request queued behind it.
		if locked.TakenDown() {
			return ev, model.EventMember{}, false, &Error{"event_taken_down", "this event has been taken down"}
		}
		if locked.Stage == "results" || locked.Stage == "archived" {
			return ev, model.EventMember{}, false, &Error{"judging_closed", "judging is closed for this event"}
		}
		member, err = eventrepo.InsertEventMember(ctx, tx, ev.ID, userID, role, display)
		if err == nil {
			err = tx.Commit()
		}
	} else {
		tx, txerr := s.db.BeginTx(ctx, nil)
		if txerr != nil {
			return ev, model.EventMember{}, false, txerr
		}
		defer tx.Rollback()
		locked, txerr := eventrepo.GetEventForShare(ctx, tx, ev.ID)
		if errors.Is(txerr, sql.ErrNoRows) {
			return ev, model.EventMember{}, false, &Error{"invalid_code", "invalid code"}
		}
		if txerr != nil {
			return ev, model.EventMember{}, false, txerr
		}
		if locked.TakenDown() {
			return ev, model.EventMember{}, false, &Error{"event_taken_down", "this event has been taken down"}
		}
		if locked.Stage != "open" && locked.Stage != "building" {
			return ev, model.EventMember{}, false, &Error{"joining_closed", "joining is closed for this event"}
		}
		questions, required, qerr := eventrepo.RegistrationSettings(ctx, tx, ev.ID)
		if qerr != nil {
			return ev, model.EventMember{}, false, qerr
		}
		answers, validationErr := validate(questions, input)
		if validationErr != nil {
			return ev, model.EventMember{}, false, validationErr
		}
		member, err = eventrepo.JoinParticipantRegistration(ctx, tx, ev.ID, userID, display, answers, required)
		if err == nil && required {
			// A sign-up that waits for approval waits on the organisers.
			err = eventrepo.NotifyOrganisers(ctx, tx, ev.ID, eventrepo.NoticeApplicationNew, userID)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err != nil {
		if unique(err) {
			existing, err2 := eventrepo.GetEventMember(ctx, s.db, ev.ID, userID)
			if err2 == nil && existing.Role == role {
				return ev, existing, false, nil
			}
			return ev, model.EventMember{}, false, &Error{"already_member", "you already have a different role in this event"}
		}
		return ev, model.EventMember{}, false, err
	}
	return ev, member, false, nil
}
