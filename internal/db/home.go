package db

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sites"
)

// HomeSite loads the selected site, including offline and taken-down rows.
func HomeSite(ctx context.Context, q Querier, userID string) (Site, bool, error) {
	return sites.HomeSite(ctx, q, userID)
}

// SetHomeSite uses one statement so a concurrent delete cannot select a deleted site.
func SetHomeSite(ctx context.Context, q Querier, userID string, name *string) error {
	return sites.SetHomeSite(ctx, q, userID, name)
}
