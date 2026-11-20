package sites

import (
	"context"
	"database/sql"
)

// What a product reads of an account's sites (app/sites/product.go).

// LiveSiteNames is the account's sites that visitors can see: not deleted,
// not taken down, and the account itself not suspended.
// withDeleted counts sites in Recently deleted too.
func LiveSiteNames(ctx context.Context, q Querier, accountID string, withDeleted bool) ([]string, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT s.name FROM sites s JOIN users u ON u.id = s.user_id
		 WHERE s.user_id = $1 AND ($2 OR s.deleted_at IS NULL) AND s.suspended_at IS NULL AND u.suspended_at IS NULL
		 ORDER BY s.name`, accountID, withDeleted)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SiteExists reports whether the account has a site of that name that is
// not in Recently deleted.
func SiteExists(ctx context.Context, q Querier, accountID, name string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL)`, accountID, name).Scan(&ok)
	return ok, err
}

// SiteEverNamed reports whether the account has or had a site of that name,
// Recently deleted included.
func SiteEverNamed(ctx context.Context, q Querier, accountID, name string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sites WHERE user_id = $1 AND lower(name) = lower($2))`, accountID, name).Scan(&ok)
	return ok, err
}

// LiveActiveVersion is the live version of the account's site of that name
// (not valid when there is no such site).
func LiveActiveVersion(ctx context.Context, q Querier, accountID, name string) (sql.NullInt64, error) {
	var v sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT active_version FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL`, accountID, name).Scan(&v)
	if err == sql.ErrNoRows {
		return v, nil
	}
	return v, err
}

// StoredVersions is every stored (not uploading, not discarded) version of
// the account's sites not in Recently deleted, as "<site id>/<n>".
func StoredVersions(ctx context.Context, q Querier, accountID string) (map[string]bool, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT v.site_id::text || '/' || v.version_number FROM versions v JOIN sites s ON s.id = v.site_id
		 WHERE s.user_id = $1 AND s.deleted_at IS NULL AND v.status <> 'uploading' AND v.status <> 'discarded'`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// AccountSiteCounts counts the account's site records (Recently deleted
// included) and their kept versions.
func AccountSiteCounts(ctx context.Context, q Querier, accountID string) (sites, versions int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT count(*),
		(SELECT count(*) FROM versions v JOIN sites s ON s.id = v.site_id WHERE s.user_id = $1 AND v.status <> 'discarded')
		FROM sites WHERE user_id = $1`, accountID).Scan(&sites, &versions)
	return sites, versions, err
}

// LockAccountSites locks every site row of the account, as a deploy takes
// its own: an account about to be deleted with its sites.
func LockAccountSites(ctx context.Context, tx *sql.Tx, accountID string) error {
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM sites WHERE user_id = $1 FOR UPDATE`, accountID)
	return err
}

// DeleteAccountRow deletes an account whose deletion its product decided
// (a Simple Hack event's account): its sites and memberships go with it.
func DeleteAccountRow(ctx context.Context, tx *sql.Tx, accountID string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, accountID)
	return err
}
