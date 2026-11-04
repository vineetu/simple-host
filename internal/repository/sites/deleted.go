package sites

import (
	"context"
	"database/sql"
	"time"
)

const purgeDue = `COALESCE(purge_at, deleted_at + ($1 * interval '1 second')) < now()`
const notTakenDown = `suspended_at IS NULL
	AND NOT EXISTS (SELECT 1 FROM users u WHERE u.id = sites.user_id AND u.suspended_at IS NOT NULL)`

// MarkSiteDeleted moves a live site to Recently deleted, recording when it
// will be removed for good. sql.ErrNoRows when there is no live site with
// that id. An emailed idle-cleanup link ("Keep
// it") ends with it; the cleanup's own removal sets a restore link after.
func MarkSiteDeleted(ctx context.Context, q Querier, siteID string, retention time.Duration) error {
	res, err := q.ExecContext(ctx, `WITH cleared AS (UPDATE users SET home_site_id = NULL WHERE home_site_id = $1) UPDATE sites SET deleted_at = now(), purge_at = now() + ($2 * interval '1 second'), idle_token_hash = NULL
		 WHERE id = $1 AND deleted_at IS NULL`, siteID, int64(retention.Seconds()))
	return oneRow(res, err)
}

// RestoreDeletedSite brings a site back from Recently deleted. sql.ErrNoRows
// when the site is not (or no longer) in Recently deleted. Every restore
// (owner app, API, MCP, the emailed link) counts as the owner keeping it: the
// idle warning, removal and link end and the idle clock restarts.
func RestoreDeletedSite(ctx context.Context, q Querier, siteID string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET deleted_at = NULL, purge_at = NULL, updated_at = now(),
		       idle_kept_at = now(), idle_warned_at = NULL, idle_removed_at = NULL, idle_token_hash = NULL
		 WHERE id = $1 AND deleted_at IS NOT NULL`, siteID)
	return oneRow(res, err)
}

func oneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// LockPurgeableSite locks a deleted site only when its retention window has elapsed.
func LockPurgeableSite(ctx context.Context, tx *sql.Tx, siteID string, retention time.Duration) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE id = $2 AND deleted_at IS NOT NULL
		AND `+purgeDue+` AND `+notTakenDown+` FOR UPDATE`, int64(retention.Seconds()), siteID).Scan(&id)
}
