package events

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/host/sqlkit"
	model "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// CreateOrganiserInvite replaces the reusable invitation and returns its code and expiry.
func (s *Service) CreateOrganiserInvite(ctx context.Context, eventID, userID string) (string, time.Time, error) {
	code, err := hackevents.RandomCode(32)
	if err != nil {
		return "", time.Time{}, err
	}
	var expiry time.Time
	err = s.asOrganiser(ctx, eventID, userID, func(tx *sql.Tx) error {
		expiry, err = eventrepo.SetOrganiserInvite(ctx, tx, eventID, userID, hackevents.HashCode(code))
		return err
	})
	return code, expiry, err
}

// asOrganiser runs write in one transaction holding userID's organiser
// membership FOR SHARE, so a removal cannot land between the check and the
// write; sql.ErrNoRows when they do not organise the event.
func (s *Service) asOrganiser(ctx context.Context, eventID, userID string, write func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := hackmembers.Member(ctx, tx, eventID, userID, hackmembers.ForShare)
	if err == nil && !m.Organiser() {
		err = sql.ErrNoRows
	}
	if err == nil {
		err = write(tx)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) RevokeOrganiserInvite(ctx context.Context, eventID string) error {
	return eventrepo.DeleteOrganiserInvite(ctx, s.db, eventID)
}

func (s *Service) OrganiserInvite(ctx context.Context, code string) (model.Event, error) {
	return organiserInviteEvent(ctx, s.db, hackevents.HashCode(hackevents.NormalizeCode(code)), false)
}

// organiserInviteEvent is the event of an unexpired invitation whose issuer
// still organises it (sql.ErrNoRows otherwise); lock locks the invitation,
// then the issuer's member row.
func organiserInviteEvent(ctx context.Context, q sqlkit.Querier, hash string, lock bool) (model.Event, error) {
	ev, issuer, err := eventrepo.OrganiserInviteEvent(ctx, q, hash, lock)
	if err != nil {
		return model.Event{}, err
	}
	l := hackmembers.NoLock
	if lock {
		l = hackmembers.ForUpdate
	}
	m, err := hackmembers.Member(ctx, q, ev.ID, issuer, l)
	if err == nil && !m.Organiser() {
		err = sql.ErrNoRows
	}
	if err != nil {
		return model.Event{}, err
	}
	return ev, nil
}

// AcceptOrganiserInvite joins through a reusable invitation, locking the event before the invite and member.
func (s *Service) AcceptOrganiserInvite(ctx context.Context, code, userID, name string) (model.Event, model.EventMember, error) {
	hash := hackevents.HashCode(hackevents.NormalizeCode(code))
	// Find the event before locking, then lock the event before the invite and inviter's member row, matching leave's lock order.
	unlocked, err := organiserInviteEvent(ctx, s.db, hash, false)
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
	if _, err := organiserInviteEvent(ctx, tx, hash, true); err != nil {
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
	if _, err := hackmembers.Member(ctx, tx, eventID, userID, hackmembers.NoLock); err == nil {
		return model.EventMember{}, &Error{Code: "already_member", Message: "you already belong to this event; roles cannot be changed through an invitation"}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return model.EventMember{}, err
	}
	if err := EnsureAccountName(ctx, tx, userID, name); err != nil {
		return model.EventMember{}, err
	}
	member, err := hackmembers.Join(ctx, tx, eventID, userID, "organiser")
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
	if err = entryrepo.DropLeaver(ctx, tx, event.ID, userID, true); err != nil {
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
