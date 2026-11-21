package events

import (
	"context"
	"database/sql"
	"errors"
	"time"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// CreateOrganiserInvite replaces the reusable invitation and returns its code and expiry.
func (s *Service) CreateOrganiserInvite(ctx context.Context, eventID, userID string) (string, time.Time, error) {
	code, err := hackevents.RandomCode(32)
	if err != nil {
		return "", time.Time{}, err
	}
	expiry, err := eventrepo.SetOrganiserInvite(ctx, s.db, eventID, userID, hackevents.HashCode(code))
	return code, expiry, err
}

func (s *Service) RevokeOrganiserInvite(ctx context.Context, eventID string) error {
	return eventrepo.DeleteOrganiserInvite(ctx, s.db, eventID)
}

func (s *Service) OrganiserInvite(ctx context.Context, code string) (model.Event, error) {
	return eventrepo.OrganiserInviteEvent(ctx, s.db, hackevents.HashCode(hackevents.NormalizeCode(code)), false)
}

// AcceptOrganiserInvite joins through a reusable invitation, locking the event before the invite and member.
func (s *Service) AcceptOrganiserInvite(ctx context.Context, code, userID, name string) (model.Event, model.EventMember, error) {
	hash := hackevents.HashCode(hackevents.NormalizeCode(code))
	// Find the event before locking, then lock the event before the invite and inviter's member row, matching leave's lock order.
	unlocked, err := eventrepo.OrganiserInviteEvent(ctx, s.db, hash, false)
	if err != nil {
		return model.Event{}, model.EventMember{}, inviteRefusal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, model.EventMember{}, err
	}
	defer tx.Rollback()
	ev, err := lockInvitedEvent(ctx, tx, unlocked.ID, hash)
	if err != nil {
		return ev, model.EventMember{}, err
	}
	member, err := joinAsOrganiser(ctx, tx, ev.ID, userID, name)
	if err != nil {
		return ev, member, err
	}
	return ev, member, tx.Commit()
}

func inviteRefusal(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "invalid_code", Message: "invalid or expired invitation"}
	}
	return err
}

// lockInvitedEvent locks the event, then rechecks the invitation under lock (serialising accepts), and refuses an event that is taken down or ended.
func lockInvitedEvent(ctx context.Context, tx *sql.Tx, eventID, hash string) (model.Event, error) {
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return ev, inviteRefusal(err)
	}
	if _, err := eventrepo.OrganiserInviteEvent(ctx, tx, hash, true); err != nil {
		return ev, inviteRefusal(err)
	}
	if ev.TakenDown() {
		return ev, &Error{Code: "event_taken_down", Message: "this event has been taken down"}
	}
	if ev.Stage == "archived" {
		return ev, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	return ev, nil
}

// joinAsOrganiser adds a user who is not yet in the event as an organiser and refreshes the event's access.
func joinAsOrganiser(ctx context.Context, tx *sql.Tx, eventID, userID, name string) (model.EventMember, error) {
	if _, err := eventrepo.GetEventMember(ctx, tx, eventID, userID); err == nil {
		return model.EventMember{}, &Error{Code: "already_member", Message: "you already belong to this event; roles cannot be changed through an invitation"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.EventMember{}, err
	}
	if err := EnsureAccountName(ctx, tx, userID, name); err != nil {
		return model.EventMember{}, err
	}
	member, err := eventrepo.InsertEventMember(ctx, tx, eventID, userID, "organiser")
	if unique(err) {
		return member, &Error{Code: "already_member", Message: "you already belong to this event"}
	}
	if err != nil {
		return member, err
	}
	return member, SyncAccess(ctx, tx, eventID)
}

// RemoveOrganiser removes a co-organiser and revokes the shared invitation in a transaction.
func (s *Service) RemoveOrganiser(ctx context.Context, event model.Event, actorID, userID string) error {
	if event.CreatedBy.Valid && userID == event.CreatedBy.String {
		return &Error{Code: "primary_organiser", Message: "the event creator cannot be removed"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = teamrepo.RemoveCoOrganiser(ctx, tx, event.ID, actorID, userID); errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "event_not_found", Message: "event not found"}
	} else if err != nil {
		return err
	}
	if err = eventrepo.DeleteOrganiserInvite(ctx, tx, event.ID); err != nil {
		return err
	}
	if err = SyncAccess(ctx, tx, event.ID); err != nil {
		return err
	}
	return tx.Commit()
}

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
