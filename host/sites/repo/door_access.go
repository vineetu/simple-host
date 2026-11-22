package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"

	accountsrepo "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
)

// A site's gate as Access decides on it: its passcode, who may view it,
// whether it is offline or taken down. The columns are on sites, so they are
// read and written here; Access's routes call these.

type SitePasscodeRow = model.SitePasscodeRow

// GetSitePasscode loads the passcode state of a live (not deleted) site.
// sql.ErrNoRows for an unknown or deleted site.
func GetSitePasscode(ctx context.Context, q Querier, siteID string) (SitePasscodeRow, error) {
	var r SitePasscodeRow
	err := q.QueryRowContext(ctx, `
		SELECT id, user_id, name, passcode_enc, passcode_set_at, passcode_generation, offline_at IS NOT NULL, access = 'specific'
		  FROM sites WHERE id::text = $1 AND deleted_at IS NULL`, siteID).Scan(
		&r.SiteID, &r.UserID, &r.Name, &r.Enc, &r.SetAt, &r.Generation, &r.Offline, &r.NamedViewers)
	return r, err
}

// SetSitePasscode stores a new sealed passcode and ends every unlock (the
// generation goes up). Returns the new generation. sql.ErrNoRows for an
// unknown or deleted site.
func SetSitePasscode(ctx context.Context, q Querier, siteID string, enc []byte, now time.Time) (int, error) {
	var gen int
	err := q.QueryRowContext(ctx, `
		UPDATE sites SET passcode_enc = $2, passcode_set_at = $3, passcode_generation = passcode_generation + 1
		 WHERE id = $1 AND deleted_at IS NULL
		RETURNING passcode_generation`, siteID, enc, now).Scan(&gen)
	return gen, err
}

// ClearSitePasscode removes the passcode (and ends every unlock, so putting
// one back later never revives an old cookie). sql.ErrNoRows for an unknown
// or deleted site.
func ClearSitePasscode(ctx context.Context, q Querier, siteID string) error {
	res, err := q.ExecContext(ctx, `
		UPDATE sites SET passcode_enc = NULL, passcode_set_at = NULL, passcode_generation = passcode_generation + 1
		 WHERE id = $1 AND deleted_at IS NULL`, siteID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// BumpSitePasscodeGeneration ends every unlock of a site that has a
// passcode ("sign everyone out"). sql.ErrNoRows when the site is unknown,
// deleted or has no passcode.
func BumpSitePasscodeGeneration(ctx context.Context, q Querier, siteID string) (int, error) {
	var gen int
	err := q.QueryRowContext(ctx, `
		UPDATE sites SET passcode_generation = passcode_generation + 1
		 WHERE id = $1 AND deleted_at IS NULL AND passcode_enc IS NOT NULL
		RETURNING passcode_generation`, siteID).Scan(&gen)
	return gen, err
}

// GetSitePasscodeByName is GetSitePasscode for a site named by its owner and
// name (how the file-serving path knows a site).
func GetSitePasscodeByName(ctx context.Context, q Querier, userID, name string) (SitePasscodeRow, error) {
	var r SitePasscodeRow
	err := q.QueryRowContext(ctx, `
		SELECT id, user_id, name, passcode_enc, passcode_set_at, passcode_generation, offline_at IS NOT NULL, access = 'specific'
		  FROM sites WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL`, userID, name).Scan(
		&r.SiteID, &r.UserID, &r.Name, &r.Enc, &r.SetAt, &r.Generation, &r.Offline, &r.NamedViewers)
	return r, err
}

// SetSiteAccess sets sites.access ('anyone' or 'specific') of a live site.
// sql.ErrNoRows for an unknown or deleted site.
func SetSiteAccess(ctx context.Context, q Querier, siteID, access string) error {
	res, err := q.ExecContext(ctx, `UPDATE sites SET access = $2 WHERE id = $1 AND deleted_at IS NULL`, siteID, access)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetSiteIDByName resolves a bare site name for the legacy edge. When multiple
// users own the same name, it returns the ORIGINAL (oldest) owner so the
// legacy subdomain / bare-name route never flips to a later same-named site.
// Returns sql.ErrNoRows if no site with that name exists.
func GetSiteIDByName(ctx context.Context, db *sql.DB, name string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM sites WHERE name = $1 AND deleted_at IS NULL ORDER BY created_at ASC, id ASC LIMIT 1`,
		name,
	).Scan(&id)
	return id, err
}

// SiteOffline reports whether a site is offline by its owner's choice.
func SiteOffline(ctx context.Context, q Querier, siteID string) (bool, error) {
	var off bool
	err := q.QueryRowContext(ctx, `SELECT offline_at IS NOT NULL FROM sites WHERE id::text = $1`, siteID).Scan(&off)
	return off, err
}

// ListAccountAddresses returns every live site's own address (claimed name
// or custom domain), and the earlier one it still serves at while a new one
// is pending.
func ListAccountAddresses(ctx context.Context, database *sql.DB, userID string) ([]model.AccountAddress, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT name, lower(COALESCE(custom_domain, '')), COALESCE(domain_status, ''), lower(COALESCE(previous_domain, ''))
		  FROM sites WHERE user_id = $1 AND deleted_at IS NULL
		   AND (custom_domain IS NOT NULL OR previous_domain IS NOT NULL)
		 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AccountAddress
	for rows.Next() {
		var a model.AccountAddress
		if err := rows.Scan(&a.Site, &a.Domain, &a.Status, &a.Previous); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListRetiredNames returns the account's retired <name>.<SITE_DOMAIN> names.
func ListRetiredNames(ctx context.Context, database *sql.DB, userID string) ([]model.RetiredAccountName, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT l.hostname, COALESCE(s.name, '')
		  FROM legacy_hostnames l LEFT JOIN sites s ON s.id = l.site_id AND s.deleted_at IS NULL
		 WHERE l.user_id = $1 ORDER BY l.hostname`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RetiredAccountName
	for rows.Next() {
		var r model.RetiredAccountName
		if err := rows.Scan(&r.Name, &r.Site); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func AccountHasSites(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var has bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM sites WHERE user_id=$1)", id).Scan(&has)
	return has, err
}

// LongestSiteName is the account's longest site name, live or Recently
// deleted ("" with none).
func LongestSiteName(ctx context.Context, tx *sql.Tx, id string) (string, error) {
	var name string
	err := tx.QueryRowContext(ctx, "SELECT COALESCE((SELECT name FROM sites WHERE user_id=$1 ORDER BY length(name) DESC LIMIT 1), '')", id).Scan(&name)
	return name, err
}

// AccountSiteKeys lists the account id and name of every site of the
// account and of the teams it belongs to, live and Recently deleted, so the
// caller can take each site's lock before erasing (a team the account alone
// belongs to goes with it).
func AccountSiteKeys(ctx context.Context, database *sql.DB, userID string) ([][2]string, error) {
	accounts, err := accountsrepo.TeamIDsOf(ctx, database, userID)
	if err != nil {
		return nil, err
	}
	rows, err := database.QueryContext(ctx, `SELECT user_id::text, name FROM sites WHERE user_id = ANY($1::uuid[])`,
		pq.Array(append(accounts, userID)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var k [2]string
		if err := rows.Scan(&k[0], &k[1]); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// SiteActivationFor adds the sites side of each account's Activation (out,
// from accounts' ActivationFor): sites published and when first, person
// visits, and a deploy as the latest activity.
func SiteActivationFor(ctx context.Context, q Querier, userIDs []string, out map[string]*model.Activation) error {
	if len(userIDs) == 0 {
		return nil
	}
	queries := []struct {
		sql  string
		fill func(*sql.Rows) (string, func(*model.Activation), error)
	}{
		{`SELECT s.user_id, COUNT(*), MIN(s.created_at)
		  FROM sites s
		 WHERE s.user_id = ANY($1::uuid[])
		   AND EXISTS (SELECT 1 FROM versions v WHERE v.site_id = s.id AND v.status IN ('active', 'ready'))
		 GROUP BY s.user_id`, func(rows *sql.Rows) (string, func(*model.Activation), error) {
			var id string
			var n int
			var first sql.NullTime
			err := rows.Scan(&id, &n, &first)
			return id, func(a *model.Activation) { a.SitesPublished, a.FirstPublish = n, first }, err
		}},
		{`SELECT s.user_id, COALESCE(SUM(h.views), 0)
		  FROM site_view_hourly h JOIN sites s ON s.id = h.site_id
		 WHERE s.user_id = ANY($1::uuid[]) AND h.class = 'person'
		 GROUP BY s.user_id`, func(rows *sql.Rows) (string, func(*model.Activation), error) {
			var id string
			var n int64
			err := rows.Scan(&id, &n)
			return id, func(a *model.Activation) { a.Visits = n }, err
		}},
		{`SELECT s.user_id, MAX(v.created_at) FROM versions v JOIN sites s ON s.id = v.site_id
		 WHERE s.user_id = ANY($1::uuid[]) AND v.created_at IS NOT NULL GROUP BY s.user_id`, func(rows *sql.Rows) (string, func(*model.Activation), error) {
			var id string
			var at sql.NullTime
			err := rows.Scan(&id, &at)
			return id, func(a *model.Activation) {
				if at.Valid && (!a.LastActive.Valid || at.Time.After(a.LastActive.Time)) {
					a.LastActive = at
				}
			}, err
		}},
	}
	for _, aq := range queries {
		rows, err := q.QueryContext(ctx, aq.sql, pq.Array(userIDs))
		if err != nil {
			return err
		}
		for rows.Next() {
			id, fill, err := aq.fill(rows)
			if err != nil {
				rows.Close()
				return err
			}
			if a := out[id]; a != nil {
				fill(a)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}
