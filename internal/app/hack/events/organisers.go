package events

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// CreateOrganiserInvite creates an invitation and returns its code and expiry.
func (s *Service) CreateOrganiserInvite(ctx context.Context, eventID, userID string) (string, time.Time, error) {
	code, err := hackevents.RandomCode(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiry, err := db.SetOrganiserInvite(ctx, s.db, eventID, userID, db.HashAPIKey(code))
	return code, expiry, err
}

// RevokeOrganiserInvite removes an invitation.
func (s *Service) RevokeOrganiserInvite(ctx context.Context, eventID string) error {
	return db.DeleteOrganiserInvite(ctx, s.db, eventID)
}

// OrganiserInvite finds an invitation's event.
func (s *Service) OrganiserInvite(ctx context.Context, code string) (db.Event, error) {
	return db.OrganiserInviteEvent(ctx, s.db, db.HashAPIKey(hackevents.NormalizeCode(code)), false)
}

// AcceptOrganiserInvite accepts an invitation, locking the event before the invite and member.
func (s *Service) AcceptOrganiserInvite(ctx context.Context, code, userID, name string) (db.Event, db.EventMember, error) {
	var ev db.Event
	var member db.EventMember
	invalid := func() error { return &Error{"invalid_code", "invalid or expired invitation"} }
	hash := db.HashAPIKey(hackevents.NormalizeCode(code))
	// Find the event before locking, then lock the event before the invite
	// and inviter's member row, matching leave's lock order (SIM-340 2.4).
	unlocked, err := db.OrganiserInviteEvent(ctx, s.db, hash, false)
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
	ev, err = db.GetEventForUpdate(ctx, tx, unlocked.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ev, member, invalid()
	}
	if err != nil {
		return ev, member, err
	}
	// Recheck the invite under lock before checking whether the event ended:
	// ending an event deletes invitations, so invalid_code takes precedence.
	if _, err = db.OrganiserInviteEvent(ctx, tx, hash, true); errors.Is(err, sql.ErrNoRows) {
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
	if _, err = db.GetEventMember(ctx, tx, ev.ID, userID); err == nil {
		return ev, member, &Error{"already_member", "you already belong to this event; roles cannot be changed through an invitation"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ev, member, err
	}
	member, err = db.InsertEventMember(ctx, tx, ev.ID, userID, "organiser", name)
	if unique(err) {
		return ev, member, &Error{"already_member", "you already belong to this event"}
	}
	if err != nil {
		return ev, member, err
	}
	if err = db.DeleteOrganiserInvite(ctx, tx, ev.ID); err != nil {
		return ev, member, err
	}
	if err = tx.Commit(); err != nil {
		return ev, member, err
	}
	return ev, member, nil
}

// RemoveOrganiser removes a co-organiser in a transaction.
func (s *Service) RemoveOrganiser(ctx context.Context, event db.Event, actorID, userID string) error {
	if event.CreatedBy.Valid && userID == event.CreatedBy.String {
		return &Error{"primary_organiser", "the event creator cannot be removed"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = db.RemoveCoOrganiser(ctx, tx, event.ID, actorID, userID); errors.Is(err, sql.ErrNoRows) {
		return &Error{"event_not_found", "event not found"}
	} else if err != nil {
		return err
	}
	return tx.Commit()
}

// RegenerateCode replaces the event's join or judge code, retrying collisions.
func (s *Service) RegenerateCode(ctx context.Context, eventID, kind string, length int) (db.Event, error) {
	var updated db.Event
	var err error
	for attempt := 0; attempt < 8; attempt++ {
		code, genErr := hackevents.RandomCode(length)
		if genErr != nil {
			return updated, genErr
		}
		if kind == "join" {
			updated, err = db.SetEventJoinCode(ctx, s.db, eventID, code)
		} else {
			updated, err = db.SetEventJudgeCode(ctx, s.db, eventID, code)
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
