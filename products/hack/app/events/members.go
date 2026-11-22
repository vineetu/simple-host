package events

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// countPeople is the event's approved participants, its teams and its
// judges (joined judges and the organisers it lists as judges).
func countPeople(ctx context.Context, q sqlkit.Querier, ev model.Event) (participants, teams, judges int, err error) {
	if participants, err = hackmembers.Count(ctx, q, hackmembers.Filter{EventID: ev.ID, Role: "participant", Status: "approved"}); err != nil {
		return
	}
	if teams, err = teamrepo.CountEventTeams(ctx, q, ev.ID); err != nil {
		return
	}
	ids, err := hackmembers.JudgeIDs(ctx, q, ev.ID, ev.OrganiserJudges)
	return participants, teams, len(ids), err
}

// deleteEventAndAccount deletes the event (cascading members and teams) and
// its notices, then the holding account, which frees the handle and slug.
func DeleteEventAndAccount(ctx context.Context, tx *sql.Tx, ev model.Event) error {
	// Lock order: the event row (as PATCH, stage and extension changes take
	// it), the holding account's sites (an update deploy holds its site row,
	// then its team row), then the team rows.
	if err := eventrepo.LockEventRow(ctx, tx, ev.ID); err != nil {
		return err
	}
	if err := sites.LockAccountSites(ctx, tx, ev.AccountID); err != nil {
		return err
	}
	if err := teamrepo.LockEventTeams(ctx, tx, ev.ID); err != nil {
		return err
	}
	// The name of an event anyone but its organisers was in stays taken.
	joined, err := hackmembers.AnyoneButOrganisers(ctx, tx, ev.ID)
	if err != nil {
		return err
	}
	if joined {
		if err := eventrepo.ReserveEventName(ctx, tx, ev.ID); err != nil {
			return err
		}
	}
	// event_notices has no foreign key to events.
	if err := hackmembers.DeleteEventNotices(ctx, tx, ev.ID); err != nil {
		return err
	}
	if err := eventrepo.DeleteEvent(ctx, tx, ev.ID); err != nil {
		return err
	}
	return accounts.DeleteAccountRow(ctx, tx, ev.AccountID)
}

// memberships is every event the person is in, newest first, and their
// membership in each (with their display name).
func memberships(ctx context.Context, q sqlkit.Querier, userID string) ([]model.Event, []model.EventMember, error) {
	members, err := hackmembers.Members(ctx, q, hackmembers.Filter{UserID: userID})
	if err != nil || len(members) == 0 {
		return nil, nil, err
	}
	u, err := accounts.UserByID(ctx, q, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	ids := make([]string, len(members))
	byEvent := map[string]model.EventMember{}
	for i, m := range members {
		m.DisplayName = u.DisplayName.String
		ids[i], byEvent[m.EventID] = m.EventID, m
	}
	byID, err := eventrepo.EventsByIDs(ctx, q, ids)
	if err != nil {
		return nil, nil, err
	}
	var events []model.Event
	for _, ev := range byID {
		events = append(events, ev)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.After(events[j].CreatedAt) })
	out := make([]model.EventMember, len(events))
	for i, ev := range events {
		out[i] = byEvent[ev.ID]
	}
	return events, out, nil
}
