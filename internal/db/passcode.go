package db

import (
	"context"
	"database/sql"
	"time"
)

// SitePasscodeRow is a site's passcode state (handler/passcode.go). Enc is the
// sealed passcode (nil = none); Generation goes up on every change and on
// "sign everyone out".
type SitePasscodeRow struct {
	SiteID     string
	UserID     string
	Name       string
	Enc        []byte
	SetAt      sql.NullTime
	Generation int
	Offline    bool
	// NamedViewers: sites.access is 'specific' (viewers.go): only the owner
	// and the named viewers may open the site.
	NamedViewers bool
}

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
