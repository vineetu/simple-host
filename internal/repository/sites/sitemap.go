package sites

import (
	"context"
	"database/sql"
)

// ListSitemapSites reads all eligible rows in one query, independent of profile
// listing. The caller checks live files and the site's own indexing preferences.
func ListSitemapSites(ctx context.Context, q *sql.DB, teamsOnly bool) ([]Site, error) {
	rows, err := q.QueryContext(ctx, `
 SELECT s.id, s.user_id, s.name, COALESCE(u.handle, ''),
        COALESCE(s.site_url, ''), s.custom_domain, s.domain_verified_at,
        COALESCE(s.previous_domain, ''),
        (SELECT max(v.created_at) FROM versions v WHERE v.site_id=s.id AND v.status='active')
 FROM sites s JOIN users u ON u.id=s.user_id
 WHERE s.deleted_at IS NULL AND s.offline_at IS NULL
   AND s.suspended_at IS NULL AND u.suspended_at IS NULL
   AND s.passcode_enc IS NULL AND s.access='anyone' AND s.expires_at IS NULL
   AND NOT EXISTS (SELECT 1 FROM events e JOIN event_teams t ON t.event_id=e.id
                   WHERE e.account_id=s.user_id AND t.slug=s.name AND t.site_taken_down_at IS NOT NULL)
   AND (NOT $1 OR EXISTS (SELECT 1 FROM events e JOIN event_teams t ON t.event_id=e.id
                         WHERE e.account_id=s.user_id AND t.slug=s.name))
 ORDER BY s.id`, teamsOnly)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var s Site
		if err := rows.Scan(&s.ID, &s.UserID, &s.Name, &s.OwnerHandle, &s.SiteURL,
			&s.CustomDomain, &s.DomainVerifiedAt, &s.PreviousDomain, &s.LastDeployedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
