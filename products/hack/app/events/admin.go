package events

import (
	"context"
	"github.com/vsriram/simple-host/host/sites"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func (s *Service) AdminListEvents(ctx context.Context) ([]model.AdminEvent, error) {
	return eventrepo.ListAdminEvents(ctx, s.db)
}

func (s *Service) AdminKeepSites(ctx context.Context, id string, keep bool) (model.Event, error) {
	return eventrepo.SetEventKeepSites(ctx, s.db, id, keep)
}

// AdminModerate changes the event and its holding account atomically.
func (s *Service) AdminModerate(ctx context.Context, ev model.Event, reason string, restore bool, syncMarkers func(context.Context, model.Event) bool) (model.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Event{}, err
	}
	defer tx.Rollback()
	var updated model.Event
	if restore {
		updated, err = eventrepo.RestoreEvent(ctx, tx, ev.ID)
	} else {
		updated, err = eventrepo.TakeDownEvent(ctx, tx, ev.ID, reason)
	}
	if err != nil {
		return model.Event{}, err
	}
	if err = accounts.SuspendAccount(ctx, tx, ev.AccountID, reason); err != nil {
		return model.Event{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.Event{}, err
	}
	if !syncMarkers(ctx, ev) {
		return updated, &Error{Code: "markers_failed", Message: "saved, but the event's team sites could not all be updated; retry"}
	}
	return updated, nil
}

func (s *Service) AdminDelete(ctx context.Context, ev model.Event, removeFiles func(model.Event)) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = eventrepo.DeleteEventAndAccount(ctx, tx, ev, sites.LockAccountSites, sites.DeleteProductAccount); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	removeFiles(ev)
	return nil
}

func (s *Service) AdminEvent(ctx context.Context, slug string) (model.Event, error) {
	return eventrepo.GetEventBySlug(ctx, s.db, strings.ToLower(strings.TrimSpace(slug)))
}
