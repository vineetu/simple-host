package lifecycle

import (
	"context"
	"strings"

	"github.com/vsriram/simple-host/products/hack/app/events"

	accounts "github.com/vsriram/simple-host/host/access"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// AdminListEvents is every event with organiser details, head counts and creator email.
func (h *Handler) AdminListEvents(ctx context.Context) ([]model.AdminEvent, error) {
	events, err := eventrepo.ListAdminEvents(ctx, h.DB)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(events))
	for i, a := range events {
		ids[i] = a.ID
	}
	counts, err := hackmembers.CountRoles(ctx, h.DB, ids)
	if err != nil {
		return nil, err
	}
	teams, err := teamrepo.CountTeamsByEvent(ctx, h.DB, ids)
	if err != nil {
		return nil, err
	}
	for i, a := range events {
		c := counts[a.ID]
		events[i].Participants, events[i].Judges, events[i].Teams = c.Participants, c.JudgeCount(a.OrganiserJudges), teams[a.ID]
	}
	return events, nil
}

func (h *Handler) AdminKeepSites(ctx context.Context, id string, keep bool) (model.Event, error) {
	return eventrepo.SetEventKeepSites(ctx, h.DB, id, keep)
}

// AdminModerate changes the event and its holding account atomically.
func (h *Handler) AdminModerate(ctx context.Context, ev model.Event, reason string, restore bool, syncMarkers func(context.Context, model.Event) bool) (model.Event, error) {
	tx, err := h.DB.BeginTx(ctx, nil)
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
		return updated, &events.Error{Code: "markers_failed", Message: "saved, but the event's team sites could not all be updated; retry"}
	}
	return updated, nil
}

func (h *Handler) AdminDelete(ctx context.Context, ev model.Event, removeFiles func(model.Event)) error {
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = events.DeleteEventAndAccount(ctx, tx, ev); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	removeFiles(ev)
	return nil
}

func (h *Handler) AdminEvent(ctx context.Context, slug string) (model.Event, error) {
	return eventrepo.GetEventBySlug(ctx, h.DB, strings.ToLower(strings.TrimSpace(slug)))
}
