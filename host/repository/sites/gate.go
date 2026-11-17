package sites

import (
	"context"
	"database/sql"
)

// LockSiteTakedown locks the site row used for an organiser takedown.
func LockSiteTakedown(ctx context.Context, tx *sql.Tx, accountID, teamSlug string) (siteID, reason string, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(suspended_reason, '') FROM sites
		 WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL FOR UPDATE`, accountID, teamSlug).Scan(&siteID, &reason)
	return
}
