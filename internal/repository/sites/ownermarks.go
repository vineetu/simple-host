package sites

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// SiteOwner returns the site's account for owner visit classification.
func SiteOwner(ctx context.Context, q sqlkit.Querier, siteID string) (string, error) {
	var owner string
	err := q.QueryRowContext(ctx, `SELECT user_id FROM sites WHERE id = $1`, siteID).Scan(&owner)
	return owner, err
}
