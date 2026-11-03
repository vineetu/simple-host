package events

import (
	model "github.com/vsriram/simple-host/internal/model"

	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"

	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/db" // Host (4.3)
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// CreateOrganiserInvite creates an invitation and returns its code and expiry.
func (s *Service) CreateOrganiserInvite(ctx context.Context, eventID, userID string) (string, time.Time, error) {
	code, err := hackevents.RandomCode(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiry, err := eventrepo.SetOrganiserInvite(ctx, s.db, eventID, userID, db.HashAPIKey(code))
	return code, expiry, err
}

// RevokeOrganiserInvite removes an invitation.
func (s *Service) RevokeOrganiserInvite(ctx context.Context, eventID string) error {
	return eventrepo.DeleteOrganiserInvite(ctx, s.db, eventID)
}

// OrganiserInvite finds an invitation's event.
func (s *Service) OrganiserInvite(ctx context.Context, code string) (model.Event, error) {
	return eventrepo.OrganiserInviteEvent(ctx, s.db, db.HashAPIKey(hackevents.NormalizeCode(code)), false)
}

// AcceptOrganiserInvite accepts an invitation, locking the event before the invite and member.
func (s *Service) AcceptOrganiserInvite(ctx context.Context, code, userID, name string) (model.Event, model.EventMember, error) {
	var ev model.Event
	var member model.EventMember
	invalid := func() error { return &Error{"invalid_code", "invalid or expired invitation"} }
	hash := db.HashAPIKey(hackevents.NormalizeCode(code))
	// Find the event before locking, then lock the event before the invite
	// and inviter's member row, matching leave's lock order (SIM-340 2.4).
	unlocked, err := eventrepo.OrganiserInviteEvent(ctx, s.db, hash, false)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, member, invalid()
	}
	if err != nil {
		return ev, member, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ev, member, err
	}
	defer tx.Rollback()
	ev, err = eventrepo.GetEventForUpdate(ctx, tx, unlocked.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, member, invalid()
	}
	if err != nil {
		return ev, member, err
	}
	// Recheck the invite under lock before checking whether the event ended:
	// ending an event deletes invitations, so invalid_code takes precedence.
	if _, err = eventrepo.OrganiserInviteEvent(ctx, tx, hash, true); errors.Is(err, sql.ErrNoRows) {
		return ev, member, invalid()
	} else if err != nil {
		return ev, member, err
	}
	if ev.TakenDown() {
		return ev, member, &Error{"event_taken_down", "this event has been taken down"}
	}
	if ev.Stage == "archived" {
		return ev, member, &Error{"event_closed", "an ended event cannot be changed"}
	}
	if _, err = eventrepo.GetEventMember(ctx, tx, ev.ID, userID); err == nil {
		return ev, member, &Error{"already_member", "you already belong to this event; roles cannot be changed through an invitation"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ev, member, err
	}
	member, err = eventrepo.InsertEventMember(ctx, tx, ev.ID, userID, "organiser", name)
	if unique(err) {
		return ev, member, &Error{"already_member", "you already belong to this event"}
	}
	if err != nil {
		return ev, member, err
	}
	if err = eventrepo.DeleteOrganiserInvite(ctx, tx, ev.ID); err != nil {
		return ev, member, err
	}
	if err = tx.Commit(); err != nil {
		return ev, member, err
	}
	return ev, member, nil
}

// RemoveOrganiser removes a co-organiser in a transaction.
func (s *Service) RemoveOrganiser(ctx context.Context, event model.Event, actorID, userID string) error {
	if event.CreatedBy.Valid && userID == event.CreatedBy.String {
		return &Error{"primary_organiser", "the event creator cannot be removed"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = eventrepo.RemoveCoOrganiser(ctx, tx, event.ID, actorID, userID, eventLeaveOperations()); errors.Is(err, sql.ErrNoRows) {
		return &Error{"event_not_found", "event not found"}
	} else if err != nil {
		return err
	}
	return tx.Commit()
}

// RegenerateCode replaces the event's join or judge code, retrying collisions.
func (s *Service) RegenerateCode(ctx context.Context, eventID, kind string, length int) (model.Event, error) {
	var updated model.Event
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		code, genErr := hackevents.RandomCode(length)
		if genErr != nil {
			return updated, genErr
		}
		if kind == "join" {
			updated, err = eventrepo.SetEventJoinCode(ctx, s.db, eventID, code)
		} else {
			updated, err = eventrepo.SetEventJudgeCode(ctx, s.db, eventID, code)
		}
		if err == nil {
			break
		}
		if unique(err) && attempt < 7 {
			continue
		}
		return updated, err
	}
	return updated, nil
}
