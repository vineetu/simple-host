package db

import (
	"context"
)

// Old names of renamed sites (site_name_aliases). A link to an old name
// redirects to the site's current address until a site of that name exists
// again: the caller looks the name up as a site first, and only a miss reaches
// ResolveSiteNameAlias.

// ResolveOldSiteName returns the current name of the site that used to be
// called name in the account. sql.ErrNoRows when the name was never a renamed
// site's, or that site is in Recently deleted.
func ResolveOldSiteName(ctx context.Context, q Querier, userID, name string) (siteID, current string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT s.id, s.name
		  FROM site_name_aliases a JOIN sites s ON s.id = a.site_id
		 WHERE a.user_id::text = $1 AND a.name = $2
		   AND s.user_id = a.user_id AND s.deleted_at IS NULL`, userID, name).Scan(&siteID, &current)
	return siteID, current, err
}
