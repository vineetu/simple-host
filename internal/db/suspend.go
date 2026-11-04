package db

import (
	"context"
	"errors"
)

// ErrAccountSuspended is returned by GetUserByAPIKey (with the user filled in)
// when the key's account has been suspended by the operator. Callers that
// authenticate must refuse the request; callers that only compare the error to
// sql.ErrNoRows fail closed (500), never open.
var ErrAccountSuspended = errors.New("account suspended")

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
