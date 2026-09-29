package db

import (
	"context"
	"database/sql"
	"errors"
)

// ErrAccountSuspended is returned by GetUserByAPIKey (with the user filled in)
// when the key's account has been suspended by the operator. Callers that
// authenticate must refuse the request; callers that only compare the error to
// sql.ErrNoRows fail closed (500), never open.
var ErrAccountSuspended = errors.New("account suspended")

// SetSiteSuspended takes a site down (reason non-empty) or restores it
// (reason empty). Nothing else about the site changes, so restoring puts it
// back exactly as it was. Returns sql.ErrNoRows for an unknown site.
func SetSiteSuspended(ctx context.Context, q Querier, siteID, reason string) error {
	var res sql.Result
	var err error
	if reason != "" {
		res, err = q.ExecContext(ctx,
			`UPDATE sites SET suspended_at = COALESCE(suspended_at, now()), suspended_reason = $2 WHERE id::text = $1`,
			siteID, reason)
	} else {
		res, err = q.ExecContext(ctx,
			`UPDATE sites SET suspended_at = NULL, suspended_reason = NULL WHERE id::text = $1`, siteID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetUserSuspended suspends an account (reason non-empty) or re-enables it
// (reason empty). Keys and connector grants are kept; the checks that read
// users.suspended_at refuse them while it is set. Suspending also ends every
// site sign-in of the account (EndVisitorSessions): nothing re-enables them.
func SetUserSuspended(ctx context.Context, q Querier, userID, reason string) error {
	var res sql.Result
	var err error
	if reason != "" {
		res, err = q.ExecContext(ctx,
			`UPDATE users SET suspended_at = COALESCE(suspended_at, now()), suspended_reason = $2 WHERE id::text = $1`,
			userID, reason)
	} else {
		res, err = q.ExecContext(ctx,
			`UPDATE users SET suspended_at = NULL, suspended_reason = NULL WHERE id::text = $1`, userID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if reason != "" {
		return EndVisitorSessions(ctx, q, userID)
	}
	return nil
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

// UserSuspended reports whether an account is suspended. An unknown id is
// not suspended (callers resolve the account elsewhere).
func UserSuspended(ctx context.Context, q Querier, userID string) (bool, error) {
	var s bool
	err := q.QueryRowContext(ctx,
		`SELECT suspended_at IS NOT NULL FROM users WHERE id::text = $1`, userID).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return s, err
}

// GetSiteByID loads one site by id with its owner's handle and suspension
// state (admin actions address sites by id). It also finds a site in Recently
// deleted, with Deleted set.
func GetSiteByID(ctx context.Context, q Querier, siteID string) (Site, error) {
	var site Site
	err := q.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.name, s.active_version, COALESCE(s.site_url, ''), s.created_at, s.updated_at,
		       s.custom_domain, s.domain_status, s.visibility, u.username, COALESCE(u.handle, ''),
		       s.suspended_at IS NOT NULL, COALESCE(s.suspended_reason, ''),
		       u.suspended_at IS NOT NULL, COALESCE(u.suspended_reason, ''),
		       s.deleted_at IS NOT NULL, s.offline_at IS NOT NULL, s.keep_versions
		  FROM sites s JOIN users u ON u.id = s.user_id
		 WHERE s.id::text = $1`, siteID).Scan(
		&site.ID, &site.UserID, &site.Name, &site.ActiveVersion, &site.SiteURL, &site.CreatedAt, &site.UpdatedAt,
		&site.CustomDomain, &site.DomainStatus, &site.Visibility, &site.OwnerUsername, &site.OwnerHandle,
		&site.SiteSuspended, &site.SiteSuspendedReason, &site.OwnerSuspended, &site.OwnerSuspendedReason,
		&site.Deleted, &site.Offline, &site.KeepVersions,
	)
	return site, err
}
