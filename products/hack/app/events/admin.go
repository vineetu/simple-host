package events

import (
	"context"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// AdminListEvents is every event with organiser details, head counts and creator email.
func (s *Service) AdminListEvents(ctx context.Context) ([]model.AdminEvent, error) {
	events, err := eventrepo.ListAdminEvents(ctx, s.db)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(events))
	for i, a := range events {
		ids[i] = a.ID
	}
	counts, err := hackmembers.CountRoles(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	teams, err := teamrepo.CountTeamsByEvent(ctx, s.db, ids)
	if err != nil {
		return nil, err
	}
	for i, a := range events {
		c := counts[a.ID]
		events[i].Participants, events[i].Judges, events[i].Teams = c.Participants, c.JudgeCount(a.OrganiserJudges), teams[a.ID]
	}
	return events, nil
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
	if err = deleteEventAndAccount(ctx, tx, ev); err != nil {
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
