package hackteams

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
)

// OrphanTeamSites lists live sites of events' accounts ("" accountID: every
// event's) whose team no longer exists (the team was removed or emptied):
// the sweep takes them to Recently deleted. accountSites is the platform's
// list of an account's sites.
func OrphanTeamSites(ctx context.Context, q Querier, accountID string, accountSites func(context.Context, string) ([]model.Site, error)) ([]model.Site, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT e.id, e.account_id, ARRAY(SELECT t.slug FROM event_teams t WHERE t.event_id = e.id)
		  FROM events e WHERE ($1 = '' OR e.account_id::text = $1)`, accountID)
	if err != nil {
		return nil, err
	}
	type event struct {
		id, account string
		teams       []string
	}
	var events []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.id, &e.account, pq.Array(&e.teams)); err != nil {
			rows.Close()
			return nil, err
		}
		events = append(events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []model.Site
	for _, e := range events {
		list, err := accountSites(ctx, e.account)
		if err != nil {
			return nil, err
		}
		teams := map[string]bool{e.id: true}
		for _, t := range e.teams {
			teams[t] = true
		}
		for _, s := range list {
			if !teams[s.Name] {
				out = append(out, s)
			}
		}
	}
	return out, nil
}
