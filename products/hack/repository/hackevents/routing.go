package hackevents

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/model"
)

// LoadRoutingFacts joins the caller's read-only routing snapshot. No event
// gates, member lists, codes or credentials enter the copy.
func LoadRoutingFacts(ctx context.Context, tx *sql.Tx) ([]model.RouteGroup, []model.RouteGroupSite, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,slug,account_id FROM events ORDER BY id`)
	if err != nil {
		return nil, nil, err
	}
	var groups []model.RouteGroup
	for rows.Next() {
		var g model.RouteGroup
		if err = rows.Scan(&g.ID, &g.Slug, &g.AccountID); err != nil {
			rows.Close()
			return nil, nil, err
		}
		groups = append(groups, g)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT event_id,slug FROM event_teams ORDER BY event_id,slug`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var sites []model.RouteGroupSite
	for rows.Next() {
		var s model.RouteGroupSite
		if err = rows.Scan(&s.GroupID, &s.Name); err != nil {
			return nil, nil, err
		}
		sites = append(sites, s)
	}
	return groups, sites, rows.Err()
}
