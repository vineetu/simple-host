package sites

import (
	"context"
	"database/sql"
	"time"
)

// purgeDue is true for a deleted row whose promised date has passed; $1 is
// DeletedSiteRetention in seconds, for rows deleted before purge_at existed.
const purgeDue = `COALESCE(purge_at, deleted_at + ($1 * interval '1 second')) < now()`

// notTakenDown keeps a site the operator took down (or whose owner's account
// is suspended) out of the purge: its files are the evidence the take-down
// rests on, and they stay until the operator lifts it or removes the account.
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

// DeletedSite is one site in an account's Recently deleted list.
type DeletedSite struct {
	ID           string
	UserID       string
	Name         string
	DeletedAt    time.Time
	CustomDomain sql.NullString
	// purgeAt is the date stored when it was deleted (NULL for sites deleted
	// before the column existed).
	purgeAt sql.NullTime
}

// PurgeAt is when the site is removed for good: the date promised when it was
// deleted, so a later DELETED_RETENTION_DAYS change applies to new deletions
// only. retention is the caller's instance limit, used only for a row
// deleted before the purge_at column existed.
func (d DeletedSite) PurgeAt(retention time.Duration) time.Time {
	if d.purgeAt.Valid {
		return d.purgeAt.Time
	}
	return d.DeletedAt.Add(retention)
}

const deletedSiteCols = `id, user_id, name, deleted_at, custom_domain, purge_at`

func scanDeletedSite(sc interface{ Scan(...any) error }) (DeletedSite, error) {
	var d DeletedSite
	err := sc.Scan(&d.ID, &d.UserID, &d.Name, &d.DeletedAt, &d.CustomDomain, &d.purgeAt)
	return d, err
}

// GetDeletedSiteByUser returns the account's site of that name when it is in
// Recently deleted, or sql.ErrNoRows.
func GetDeletedSiteByUser(ctx context.Context, q Querier, userID, name string) (DeletedSite, error) {
	return scanDeletedSite(q.QueryRowContext(ctx,
		`SELECT `+deletedSiteCols+` FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NOT NULL`, userID, name))
}

// ListDeletedSitesByUser lists the account's Recently deleted sites, most
// recently deleted first.
func ListDeletedSitesByUser(ctx context.Context, database *sql.DB, userID string) ([]DeletedSite, error) {
	return queryDeletedSites(ctx, database,
		`SELECT `+deletedSiteCols+` FROM sites WHERE user_id = $1 AND deleted_at IS NOT NULL ORDER BY deleted_at DESC, name`, userID)
}

// ListPurgeableSites returns deleted sites whose restore date has passed,
// except taken-down ones.
func ListPurgeableSites(ctx context.Context, database *sql.DB, retention time.Duration) ([]DeletedSite, error) {
	return queryDeletedSites(ctx, database,
		`SELECT `+deletedSiteCols+` FROM sites WHERE deleted_at IS NOT NULL AND `+purgeDue+` AND `+notTakenDown+` ORDER BY deleted_at LIMIT 500`,
		int64(retention.Seconds()))
}

func queryDeletedSites(ctx context.Context, database *sql.DB, query string, args ...any) ([]DeletedSite, error) {
	rows, err := database.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeletedSite
	for rows.Next() {
		d, err := scanDeletedSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// PurgeDeletedSite removes a deleted site's row for good (its versions, data
// and claimed names cascade with it), but only while it is still deleted and
// past the window: a restore that won the race keeps the site. sql.ErrNoRows
// when nothing was removed. Returns every own address the site had, for the
// caller to unbind on disk and release the certificate of.
func PurgeDeletedSite(ctx context.Context, database *sql.DB, siteID string, retention time.Duration) ([]string, error) {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := LockPurgeableSite(ctx, tx, siteID, retention); err != nil {
		return nil, err
	}
	// A claimed <name>.<SITE_DOMAIN> outlives the site as a retired name
	// ("this site was removed"), so it never passes to a stranger's site.
	domains, err := RetireSiteNames(ctx, tx, siteID)
	if err != nil {
		return nil, err
	}
	if err := DeleteSite(ctx, tx, siteID); err != nil {
		return nil, err
	}
	return domains, tx.Commit()
}

// WakeImageScans makes a restored site's waiting image checks due now.
func WakeImageScans(ctx context.Context, q Querier, siteID string) error {
	_, err := q.ExecContext(ctx, `UPDATE image_scan_queue SET next_attempt_at = now() WHERE site_id = $1 AND next_attempt_at <> 'infinity'`, siteID)
	return err
}
