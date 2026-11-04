package db

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sites"
)

// ErrAccountSuspended is returned by GetUserByAPIKey (with the user filled in)
// when the key's account has been suspended by the operator. Callers that
// authenticate must refuse the request; callers that only compare the error to
// sql.ErrNoRows fail closed (500), never open.
var ErrAccountSuspended = model.ErrAccountSuspended

// SiteSuspension reports whether a site is taken down, by its own flag or
// through its owner's account, and the reason to show.
func SiteSuspension(ctx context.Context, q Querier, siteID string) (bool, string, error) {
	return sites.SiteSuspension(ctx, q, siteID)
}
