package repo

import (
	"context"
	"database/sql"
)

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

// SiteSuspension reports whether a site is taken down, by its own flag or
// through its owner's account, and the reason to show.
func SiteSuspension(ctx context.Context, q Querier, siteID string) (bool, string, error) {
	var siteSusp, userSusp bool
	var siteReason, userReason string
	err := q.QueryRowContext(ctx, `
		SELECT s.suspended_at IS NOT NULL, COALESCE(s.suspended_reason, ''),
		       u.suspended_at IS NOT NULL, COALESCE(u.suspended_reason, '')
		  FROM sites s JOIN users u ON u.id = s.user_id
		 WHERE s.id::text = $1`, siteID).Scan(&siteSusp, &siteReason, &userSusp, &userReason)
	if err != nil {
		return false, "", err
	}
	if siteSusp {
		return true, siteReason, nil
	}
	if userSusp {
		return true, userReason, nil
	}
	return false, "", nil
}
