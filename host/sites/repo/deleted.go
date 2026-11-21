package repo

import (
	"context"
	"database/sql"
	"time"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// purgeDue is true for a deleted row whose promised date has passed; $1 is
// DeletedSiteRetention in seconds, for rows deleted before purge_at existed.
const purgeDue = `COALESCE(purge_at, deleted_at + ($1 * interval '1 second')) < now()`

// MarkSiteDeleted moves a live site to Recently deleted, recording when it
// will be removed for good. sql.ErrNoRows when there is no live site with
// that id.
//
// Web-push subscriptions (owner and visitor devices) are deleted
// in this same statement's transaction, not left for the eventual physical
// delete's cascading FK. A site in Recently deleted serves nothing and
// should not keep pushing to anyone; its restore path (RestoreDeletedSite)
// does not restore them, the same as it never resurrects any other write
// that happened after deletion.
func MarkSiteDeleted(ctx context.Context, q Querier, siteID string, retention time.Duration) error {
	if err := accountsrepo.ClearHomeSite(ctx, q, siteID); err != nil {
		return err
	}
	res, err := q.ExecContext(ctx, `UPDATE sites SET deleted_at = now(), purge_at = now() + ($2 * interval '1 second')
		 WHERE id = $1 AND deleted_at IS NULL`, siteID, int64(retention.Seconds()))
	if err := oneRow(res, err); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `DELETE FROM site_notifications WHERE site_id=$1 AND kind='web-push'`, siteID)
	return err
}

// RestoreDeletedSite brings a site back from Recently deleted. sql.ErrNoRows
// when the site is not (or no longer) in Recently deleted. Every restore
// (owner app, API, MCP) counts as the owner keeping it: the idle clock restarts.
func RestoreDeletedSite(ctx context.Context, q Querier, siteID string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET deleted_at = NULL, purge_at = NULL, updated_at = now(),
		       idle_kept_at = now(), idle_removed_at = NULL
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

// LockPurgeableSite locks a deleted site only when its retention window has
// elapsed and nothing holds it: the owner's account row is locked first
// (a suspension waits for this transaction, or this one sees it), then the
// site row with its own take-down in the same statement. sql.ErrNoRows when
// it may not be purged.
func LockPurgeableSite(ctx context.Context, tx *sql.Tx, siteID string, retention time.Duration) error {
	if err := lockOwnerNotSuspended(ctx, tx, siteID); err != nil {
		return err
	}
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE id = $2 AND deleted_at IS NOT NULL
		AND `+purgeDue+` AND suspended_at IS NULL FOR UPDATE`, int64(retention.Seconds()), siteID).Scan(&id)
}

// lockOwnerNotSuspended locks the site owner's account row inside tx and is
// sql.ErrNoRows when the site has no owner or the owner is suspended (the
// operator's hold covers every site of theirs).
func lockOwnerNotSuspended(ctx context.Context, tx *sql.Tx, siteID string) error {
	var owner sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM sites WHERE id = $1`, siteID).Scan(&owner); err != nil {
		return err
	}
	if !owner.Valid {
		return sql.ErrNoRows
	}
	u, err := accountsrepo.LockUser(ctx, tx, owner.String)
	if err != nil {
		return err
	}
	if u.Suspended {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteExpiredSite removes an expired preview's row for good, in one
// transaction that locks its owner's account and then deletes the site only
// while it is still expired, live and not taken down. sql.ErrNoRows when it
// may not be removed (the expiry sweep then keeps its files).
func DeleteExpiredSite(ctx context.Context, database *sql.DB, siteID string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := lockOwnerNotSuspended(ctx, tx, siteID); err != nil {
		return err
	}
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE id = $1 AND expires_at IS NOT NULL AND expires_at < now()
		AND deleted_at IS NULL AND suspended_at IS NULL FOR UPDATE`, siteID).Scan(&id); err != nil {
		return err
	}
	if err := DeleteSite(ctx, tx, siteID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeletedSite is one site in an account's Recently deleted list.
type DeletedSite = model.DeletedSite

// PurgeAt is when the site is removed for good: the date promised when it was
// deleted, so a later DELETED_RETENTION_DAYS change applies to new deletions
// only. retention is the caller's instance limit, used only for a row
// deleted before the purge_at column existed.

const deletedSiteCols = `id, user_id, name, deleted_at, custom_domain, purge_at`

func scanDeletedSite(sc interface{ Scan(...any) error }) (DeletedSite, error) {
	var d DeletedSite
	err := sc.Scan(&d.ID, &d.UserID, &d.Name, &d.DeletedAt, &d.CustomDomain, &d.PurgeAtStored)
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
func PurgeDeletedSite(ctx context.Context, database *sql.DB, siteID string, retention time.Duration, platformDomains ...string) ([]string, error) {
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
	domains, err := RetireSiteNames(ctx, tx, siteID, platformDomains...)
	if err != nil {
		return nil, err
	}
	if err := DeleteSite(ctx, tx, siteID); err != nil {
		return nil, err
	}
	return domains, tx.Commit()
}
