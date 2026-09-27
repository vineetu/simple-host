package db

import (
	"context"
)

// Old names of renamed sites (site_name_aliases). A link to an old name
// redirects to the site's current address until a site of that name exists
// again: the caller looks the name up as a site first, and only a miss reaches
// ResolveSiteNameAlias.

// KeepOldSiteName records oldName as an old name of siteID in the account (a
// rename), and drops newName from the account's old names: the name is a
// site's own again. Run in the rename's transaction.
func KeepOldSiteName(ctx context.Context, q Querier, userID, siteID, oldName, newName string) error {
	if _, err := q.ExecContext(ctx, `
		INSERT INTO site_name_aliases (user_id, name, site_id) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, name) DO UPDATE SET site_id = EXCLUDED.site_id, created_at = now()`,
		userID, oldName, siteID); err != nil {
		return err
	}
	return DropOldSiteName(ctx, q, userID, newName)
}

// DropOldSiteName forgets name as an old name in the account: a site of that
// name was created or brought back, and it wins.
func DropOldSiteName(ctx context.Context, q Querier, userID, name string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM site_name_aliases WHERE user_id::text = $1 AND name = $2`, userID, name)
	return err
}

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
