package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// OrphanTeamSites lists live sites of events' accounts ("" accountID: every
// event's) whose team no longer exists (the team was removed or emptied):
// the sweep takes them to Recently deleted.
func OrphanTeamSites(ctx context.Context, database *sql.DB, accountID string) ([]model.Site, error) {
	var events []Event
	if accountID == "" {
		all, err := hackevents.AllEvents(ctx, database)
		if err != nil {
			return nil, err
		}
		events = all
	} else {
		ev, err := hackevents.GetEventByAccount(ctx, database, accountID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			events = []Event{ev}
		}
	}
	ids := make([]string, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	teams, err := teamSlugsByEvent(ctx, database, ids)
	if err != nil {
		return nil, err
	}
	var out []model.Site
	for _, e := range events {
		list, err := sites.AccountSites(ctx, database, e.AccountID)
		if err != nil {
			return nil, err
		}
		keep := map[string]bool{e.ID: true}
		for _, t := range teams[e.ID] {
			keep[t] = true
		}
		for _, s := range list {
			if !keep[s.Name] {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

// teamSlugsByEvent is the team site names of these events, by event id.
func teamSlugsByEvent(ctx context.Context, q Querier, eventIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(eventIDs) == 0 {
		return out, nil
	}
	rows, err := queryContext(ctx, q, `SELECT event_id, slug FROM event_teams WHERE event_id = ANY($1::uuid[])`, pq.Array(eventIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, slug string
		if err := rows.Scan(&id, &slug); err != nil {
			return nil, err
		}
		out[id] = append(out[id], slug)
	}
	return out, rows.Err()
}
