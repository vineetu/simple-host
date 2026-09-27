package db

import (
	"context"
	"database/sql"
	"time"
)

// Recently deleted (completeness plan, 2026-09-27).
//
// Deleting a site sets sites.deleted_at instead of removing the row, so its
// versions, saved state, collections, private-list settings and claimed names
// stay attached and its name stays held. Every lookup that serves, lists or
// resolves a site filters on deleted_at IS NULL; the functions here are the
// only ones that see deleted rows. PurgeDeletedSite removes one for good once
// DeletedSiteRetention has passed.

// DeletedSiteRetention is how long a deleted site can be restored.
const DeletedSiteRetention = 7 * 24 * time.Hour

// DeletedSite is one site in an account's Recently deleted list.
type DeletedSite struct {
	ID           string
	UserID       string
	Name         string
	DeletedAt    time.Time
	CustomDomain sql.NullString
}

// PurgeAt is when the site is removed for good.
func (d DeletedSite) PurgeAt() time.Time { return d.DeletedAt.Add(DeletedSiteRetention) }

// MarkSiteDeleted moves a live site to Recently deleted. sql.ErrNoRows when
// there is no live site with that id.
func MarkSiteDeleted(ctx context.Context, q Querier, siteID string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL`, siteID)
	return oneRow(res, err)
}

// RestoreDeletedSite brings a site back from Recently deleted. sql.ErrNoRows
// when the site is not (or no longer) in Recently deleted.
func RestoreDeletedSite(ctx context.Context, q Querier, siteID string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET deleted_at = NULL, updated_at = now() WHERE id = $1 AND deleted_at IS NOT NULL`, siteID)
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

const deletedSiteCols = `id, user_id, name, deleted_at, custom_domain`

func scanDeletedSite(sc interface{ Scan(...any) error }) (DeletedSite, error) {
	var d DeletedSite
	err := sc.Scan(&d.ID, &d.UserID, &d.Name, &d.DeletedAt, &d.CustomDomain)
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

// ListPurgeableSites returns deleted sites whose restore window has passed.
func ListPurgeableSites(ctx context.Context, database *sql.DB) ([]DeletedSite, error) {
	return queryDeletedSites(ctx, database,
		`SELECT `+deletedSiteCols+` FROM sites WHERE deleted_at IS NOT NULL AND deleted_at < now() - ($1 * interval '1 second') ORDER BY deleted_at LIMIT 500`,
		int64(DeletedSiteRetention.Seconds()))
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
// when nothing was removed.
func PurgeDeletedSite(ctx context.Context, database *sql.DB, siteID string) error {
	tx, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE id = $1 AND deleted_at IS NOT NULL
		AND deleted_at < now() - ($2 * interval '1 second') FOR UPDATE`, siteID, int64(DeletedSiteRetention.Seconds())).Scan(&id)
	if err != nil {
		return err
	}
	if err := DeleteSite(ctx, tx, siteID); err != nil {
		return err
	}
	return tx.Commit()
}
