package sites

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"time"
)

type Querier = sqlkit.Querier
type Site = model.Site
type Version = model.Version
type SiteSummary = model.SiteSummary
type ExpiredSite = model.ExpiredSite

// CreateSite inserts a new site. expiresAt is nil for permanent sites, or a
// timestamp for ephemeral "preview" sites that the background sweep deletes.
// legacy_data marks the sites made before the kinds (the column's default,
// true, covers them); a new site is false, so a data name nobody declared on
// it follows SAVED_DATA_DEFAULT_KIND (Shared unless the install chose
// declare_first).
func CreateSite(ctx context.Context, q Querier, userID, name, siteURL string, expiresAt *time.Time) (Site, error) {
	const query = `
		INSERT INTO sites (user_id, name, site_url, expires_at, legacy_data)
		VALUES ($1, $2, $3, $4, false)
		RETURNING id, user_id, name, active_version, site_url, created_at, updated_at, visibility
	`

	var site Site

	err := q.QueryRowContext(ctx, query, userID, name, siteURL, expiresAt).Scan(
		&site.ID,
		&site.UserID,
		&site.Name,
		&site.ActiveVersion,
		&site.SiteURL,
		&site.CreatedAt,
		&site.UpdatedAt,
		&site.Visibility,
	)
	if err != nil {
		return Site{}, err
	}

	return site, nil
}

// ListExpiredSites returns sites whose expires_at has passed. Sites with a NULL
// expires_at (the default) are permanent and never returned.
func ListExpiredSites(ctx context.Context, db *sql.DB) ([]ExpiredSite, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, user_id, name FROM sites WHERE expires_at IS NOT NULL AND expires_at < now() AND deleted_at IS NULL ORDER BY expires_at LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ExpiredSite
	for rows.Next() {
		var e ExpiredSite
		if err := rows.Scan(&e.ID, &e.UserID, &e.Name); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func RenameSite(ctx context.Context, q Querier, siteID, name, siteURL string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET name = $2, site_url = $3, updated_at = now() WHERE id = $1`, siteID, name, siteURL)
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

func DeleteSite(ctx context.Context, db Querier, siteID string) error {
	if _, err := db.ExecContext(ctx, `DELETE FROM versions WHERE site_id = $1`, siteID); err != nil {
		return err
	}

	result, err := db.ExecContext(ctx, `DELETE FROM sites WHERE id = $1`, siteID)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetSiteVisibility updates a site's showcase visibility ('public' | 'unlisted').
func SetSiteVisibility(ctx context.Context, db *sql.DB, siteID, visibility string) error {
	const query = `UPDATE sites SET visibility = $2, updated_at = now() WHERE id = $1`
	res, err := db.ExecContext(ctx, query, siteID, visibility)
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

// SetSiteKeepVersions stores how many deploys of a site are kept (0 = the
// instance setting). sql.ErrNoRows for an unknown or deleted site.
func SetSiteKeepVersions(ctx context.Context, q Querier, siteID string, keep int) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET keep_versions = $2 WHERE id = $1 AND deleted_at IS NULL`, siteID, keep)
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

// SiteActiveVersion is the site's live version number (read under the
// caller's lock when q is its transaction).
func SiteActiveVersion(ctx context.Context, db Querier, siteID string) (int, error) {
	var v int
	err := db.QueryRowContext(ctx, `SELECT active_version FROM sites WHERE id = $1`, siteID).Scan(&v)
	return v, err
}

// UpdateSiteSummary stores extracted content without changing the deploy timestamp.
func UpdateSiteSummary(ctx context.Context, q Querier, siteID string, s SiteSummary) error {
	_, err := q.ExecContext(ctx, `UPDATE sites SET summary_title = $2, summary_description = $3, summary_headings = $4, summary_text = $5, updated_at = updated_at WHERE id = $1`, siteID, s.Title, s.Description, s.Headings, s.Text)
	return err
}

// UpdateSiteSummaryIfUnset is UpdateSiteSummary, but only writes a row that is
// still unprocessed (summary_title IS NULL). Used only by the one-time
// site-summary-backfill CLI: a deploy may race it and already have stored the
// current version's summary between the backfill's SELECT and this write,
// and that newer summary must win, not the backfill's stale read.
func UpdateSiteSummaryIfUnset(ctx context.Context, q Querier, siteID string, s SiteSummary) error {
	_, err := q.ExecContext(ctx, `UPDATE sites SET summary_title = $2, summary_description = $3, summary_headings = $4, summary_text = $5, updated_at = updated_at WHERE id = $1 AND summary_title IS NULL`, siteID, s.Title, s.Description, s.Headings, s.Text)
	return err
}

func UpdateSiteActiveVersion(ctx context.Context, db Querier, siteID string, version int) error {
	const query = `
		UPDATE sites
		SET active_version = $2, updated_at = now()
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, siteID, version)
	return err
}

// CreateVersion opens a version row ('uploading'). author is who made it
// (?author= on the deploy, already cleaned by the handler) or "".
func CreateVersion(ctx context.Context, db Querier, siteID string, versionNumber int, diskPath, archiveSHA256, author string) (Version, error) {
	const query = `
		INSERT INTO versions (site_id, version_number, disk_path, status, archive_sha256, author)
		VALUES ($1, $2, $3, 'uploading', $4, $5)
		RETURNING id, site_id, version_number, disk_path, status, author, created_at
	`

	var version Version
	err := db.QueryRowContext(ctx, query, siteID, versionNumber, diskPath, archiveSHA256, author).Scan(
		&version.ID,
		&version.SiteID,
		&version.VersionNumber,
		&version.DiskPath,
		&version.Status,
		&version.Author,
		&version.CreatedAt,
	)
	version.ArchiveSHA256 = archiveSHA256
	return version, err
}

// ListVersionsBySite is a site's versions, newest first. Discarded versions
// (DiscardReadyVersion) are left out: the row stays only so the number is
// never handed out again.
func ListVersionsBySite(ctx context.Context, db *sql.DB, siteID string) ([]Version, error) {
	const query = `
		SELECT id, site_id, version_number, disk_path, status, author, created_at
		FROM versions
		WHERE site_id = $1 AND status <> 'discarded'
		ORDER BY version_number DESC
	`

	rows, err := db.QueryContext(ctx, query, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.SiteID, &v.VersionNumber, &v.DiskPath, &v.Status, &v.Author, &v.CreatedAt); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

func ActivateVersion(ctx context.Context, db Querier, versionID string) error {
	const query = `
		UPDATE versions
		SET status = 'active'
		WHERE id = $1
	`

	_, err := db.ExecContext(ctx, query, versionID)
	return err
}

// MarkVersionReady finishes a version deployed with publish=false: complete
// and kept, but never made live ('ready' rather than 'active', so the site's
// last-deployed time does not move). Making it live (ActivateVersionNumber)
// turns it 'active'.
func MarkVersionReady(ctx context.Context, db Querier, versionID string) error {
	_, err := db.ExecContext(ctx, `UPDATE versions SET status = 'ready' WHERE id = $1`, versionID)
	return err
}

// ActivateVersionNumber marks a site's version 'active' when it is made live
// (a no-op for one that already is).
func ActivateVersionNumber(ctx context.Context, db Querier, siteID string, version int) error {
	_, err := db.ExecContext(ctx, `UPDATE versions SET status = 'active' WHERE site_id = $1 AND version_number = $2 AND status = 'ready'`, siteID, version)
	return err
}

// VersionKept reports whether a site still keeps a complete version n (live
// now, live before, or stored with publish=false).
func VersionKept(ctx context.Context, db Querier, siteID string, version int) (bool, error) {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM versions WHERE site_id = $1 AND version_number = $2 AND status IN ('active', 'ready'))`, siteID, version).Scan(&ok)
	return ok, err
}

// LockSiteForUpdate takes a row-level lock on the sites row so concurrent
// uploads to the same site serialize their version allocation. Must be called
// inside a transaction; the lock releases on commit/rollback. sql.ErrNoRows
// when the site is gone or in Recently deleted (also when a delete committed
// while this waited for the lock).
func LockSiteForUpdate(ctx context.Context, db Querier, siteID string) error {
	const query = `SELECT id FROM sites WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`
	var id string
	return db.QueryRowContext(ctx, query, siteID).Scan(&id)
}

func GetMaxVersionNumber(ctx context.Context, db Querier, siteID string) (int, error) {
	const query = `
		SELECT COALESCE(MAX(version_number), 0)
		FROM versions
		WHERE site_id = $1
	`

	var maxVersion int
	err := db.QueryRowContext(ctx, query, siteID).Scan(&maxVersion)
	return maxVersion, err
}

// DiscardReadyVersion discards one version a deploy stored with publish=false
// and nobody has made live since (status 'ready'). It reports whether a row
// changed. The row stays as a tombstone (status 'discarded') so the number is
// never reused: GetMaxVersionNumber still counts it, while every list and
// VersionKept leave it out, so a preview link for it renders the expired page
// and a retried discard answers not found. The live version is read again
// inside the UPDATE: a rollback that made this version live between the
// handler's check and this statement must not lose it. The caller removes the
// folder afterwards (rows first, disk second, as PruneVersions explains).
func DiscardReadyVersion(ctx context.Context, q Querier, siteID string, n int) (bool, error) {
	var gone int
	err := q.QueryRowContext(ctx,
		`UPDATE versions SET status = 'discarded'
		  WHERE site_id = $1 AND version_number = $2 AND status = 'ready'
		    AND version_number <> (SELECT active_version FROM sites WHERE id = $1)
		  RETURNING version_number`, siteID, n).Scan(&gone)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// PruneVersions deletes a site's version rows below keepFrom, never touching
// the active one, and returns the version numbers removed so the caller can
// delete the matching directories. Discarded tombstones below the floor go
// too but are not returned: their folders are already gone, and no list ever
// showed them.
//
// Rows go first and disk second. The other order leaves rows pointing at
// directories that are already gone, so a rollback to one of them would serve
// nothing; this way a failure leaves directories with no rows, which wastes
// space until the next deploy retries and is otherwise harmless.
//
// The live version is read again inside the DELETE as well as passed in: a
// rollback that lands between the caller reading the site and this statement
// must not lose the version it just made live.
func PruneVersions(ctx context.Context, db *sql.DB, siteID string, keepFrom, activeVersion int) ([]int, error) {
	rows, err := db.QueryContext(ctx,
		`WITH gone AS (
		   DELETE FROM versions
		    WHERE site_id = $1 AND version_number < $2 AND version_number <> $3
		      AND version_number <> (SELECT active_version FROM sites WHERE id = $1)
		    RETURNING version_number, status)
		 SELECT version_number FROM gone WHERE status <> 'discarded'`, siteID, keepFrom, activeVersion)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var removed []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		removed = append(removed, n)
	}
	return removed, rows.Err()
}

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

// LockUserHandle locks the owner row and reads its handle.
func LockUserHandle(ctx context.Context, tx *sql.Tx, userID string, handle *sql.NullString) error {
	return tx.QueryRowContext(ctx, "SELECT handle FROM users WHERE id = $1 FOR UPDATE", userID).Scan(handle)
}
