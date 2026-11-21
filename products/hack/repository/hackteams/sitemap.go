package hackteams

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// FilterSitemapSites keeps only team sites that an organiser has not taken
// down, preserving the platform's order and site metadata (the platform's
// sitemap hook).
func FilterSitemapSites(ctx context.Context, q sqlkit.Querier, sites []model.Site) ([]model.Site, error) {
	if len(sites) == 0 {
		return sites, nil
	}
	accounts := make([]string, len(sites))
	for i, site := range sites {
		accounts[i] = site.UserID
	}
	events, err := hackevents.EventIDsByAccounts(ctx, q, accounts)
	if err != nil {
		return nil, err
	}
	eventIDs, names := make([]string, 0, len(sites)), make([]string, 0, len(sites))
	for _, site := range sites {
		if id, ok := events[site.UserID]; ok {
			eventIDs, names = append(eventIDs, id), append(names, site.Name)
		}
	}
	rows, err := queryContext(ctx, q, `
 SELECT x.e::text || '/' || x.n FROM unnest($1::uuid[], $2::text[]) x(e, n)
 WHERE EXISTS (SELECT 1 FROM event_teams t WHERE t.event_id = x.e AND t.slug = x.n AND t.site_taken_down_at IS NULL)`, pq.Array(eventIDs), pq.Array(names))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		allowed[key] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []model.Site
	for _, site := range sites {
		if allowed[events[site.UserID]+"/"+site.Name] {
			out = append(out, site)
		}
	}
	return out, nil
}
