package sites

import (
	"context"
	"database/sql"
	"time"
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

// SiteCreatedAt is a live (non-deleted) site's created_at and name.
func SiteCreatedAt(ctx context.Context, q Querier, id string) (time.Time, string, error) {
	var at time.Time
	var name string
	err := q.QueryRowContext(ctx,
		`SELECT created_at, name FROM sites WHERE id::text = $1 AND deleted_at IS NULL`,
		id,
	).Scan(&at, &name)
	return at, name, err
}

// SiteOffline reports whether a site is offline by its owner's choice.
func SiteOffline(ctx context.Context, q Querier, siteID string) (bool, error) {
	var off bool
	err := q.QueryRowContext(ctx, `SELECT offline_at IS NOT NULL FROM sites WHERE id::text = $1`, siteID).Scan(&off)
	return off, err
}

// GetSiteWriteGate returns the owning user_id and the admin override flag
// used by visitorWriteOK.
func GetSiteWriteGate(ctx context.Context, q Querier, siteID string) (userID string, allowAnonymous bool, err error) {
	err = q.QueryRowContext(ctx,
		`SELECT user_id::text, allow_anonymous_writes FROM sites WHERE id = $1`,
		siteID,
	).Scan(&userID, &allowAnonymous)
	return
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
