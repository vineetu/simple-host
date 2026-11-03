package events

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"strings"
)

// AdminListEvents lists events for the platform administrator.
func (s *Service) AdminListEvents(ctx context.Context) ([]db.AdminEvent, error) {
	return eventrepo.ListAdminEvents(ctx, s.db)
}

// AdminKeepSites changes the archive retention override.
func (s *Service) AdminKeepSites(ctx context.Context, id string, keep bool) (db.Event, error) {
	return eventrepo.SetEventKeepSites(ctx, s.db, id, keep)
}

// AdminModerate changes the event and its holding account atomically.
func (s *Service) AdminModerate(ctx context.Context, ev db.Event, reason string, restore bool, syncMarkers func(context.Context, db.Event) bool) (db.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.Event{}, err
	}
	defer tx.Rollback()
	var updated db.Event
	if restore {
		updated, err = eventrepo.RestoreEvent(ctx, tx, ev.ID)
	} else {
		updated, err = eventrepo.TakeDownEvent(ctx, tx, ev.ID, reason)
	}
	if err != nil {
		return db.Event{}, err
	}
	if err = db.SetUserSuspended(ctx, tx, ev.AccountID, reason); err != nil {
		return db.Event{}, err
	}
	if err = tx.Commit(); err != nil {
		return db.Event{}, err
	}
	if !syncMarkers(ctx, ev) {
		return updated, &Error{Code: "markers_failed", Message: "saved, but the event's team sites could not all be updated; retry"}
	}
	return updated, nil
}

// AdminDelete deletes an event and its holding account atomically.
func (s *Service) AdminDelete(ctx context.Context, ev db.Event, removeFiles func(db.Event)) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = eventrepo.DeleteEventAndAccount(ctx, tx, ev); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	removeFiles(ev)
	return nil
}

// AdminEvent loads the event selected by a platform administrator.
func (s *Service) AdminEvent(ctx context.Context, slug string) (db.Event, error) {
	return eventrepo.GetEventBySlug(ctx, s.db, strings.ToLower(strings.TrimSpace(slug)))
}
