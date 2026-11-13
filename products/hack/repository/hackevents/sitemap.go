package hackevents

import (
	"context"

	"github.com/lib/pq"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// FilterSitemapSites keeps only team sites that an organiser has not taken
// down, preserving the platform's order and site metadata.
func FilterSitemapSites(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error) {
	if len(sites) == 0 {
		return sites, nil
	}
	ids := make([]string, len(sites))
	for i, site := range sites {
		ids[i] = site.ID
	}
	rows, err := queryContext(ctx, q, `
 SELECT s.id FROM sites s
 WHERE s.id = ANY($1)
   AND NOT EXISTS (SELECT 1 FROM events e JOIN event_teams t ON t.event_id=e.id
                   WHERE e.account_id=s.user_id AND t.slug=s.name AND t.site_taken_down_at IS NOT NULL)
   AND EXISTS (SELECT 1 FROM events e JOIN event_teams t ON t.event_id=e.id
               WHERE e.account_id=s.user_id AND t.slug=s.name)`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		allowed[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []repo.Site
	for _, site := range sites {
		if allowed[site.ID] {
			out = append(out, site)
		}
	}
	return out, nil
}
