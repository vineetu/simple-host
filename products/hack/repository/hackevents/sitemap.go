package hackevents

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/repository/sqlkit"
)

// FilterSitemapSites keeps only team sites that an organiser has not taken
// down, preserving the platform's order and site metadata.
func FilterSitemapSites(ctx context.Context, q sqlkit.Querier, sites []model.Site) ([]model.Site, error) {
	if len(sites) == 0 {
		return sites, nil
	}
	accounts, names := make([]string, len(sites)), make([]string, len(sites))
	for i, site := range sites {
		accounts[i], names[i] = site.UserID, site.Name
	}
	rows, err := queryContext(ctx, q, `
 SELECT x.a || '/' || x.n FROM unnest($1::text[], $2::text[]) x(a, n)
 WHERE EXISTS (SELECT 1 FROM events e JOIN event_teams t ON t.event_id = e.id
               WHERE e.account_id::text = x.a AND t.slug = x.n AND t.site_taken_down_at IS NULL)`, pq.Array(accounts), pq.Array(names))
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
		if allowed[site.UserID+"/"+site.Name] {
			out = append(out, site)
		}
	}
	return out, nil
}
