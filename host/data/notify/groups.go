package notify

import (
	"context"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// MayActOnSite reports whether person may still keep a subscription on the
// site: they own it, or may act as its owner (accounts.ActsAsOwner: a member
// of the team that holds it, or an editor of the site).
func MayActOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error) {
	var accountID, name string
	if err := q.QueryRowContext(ctx, `SELECT user_id, name FROM sites WHERE id = $1`, siteID).Scan(&accountID, &name); err != nil {
		return false, err
	}
	return accounts.ActsAsOwner(ctx, q, person, accountID, name)
}

type notificationAccess struct{}

func (notificationAccess) ActsOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error) {
	return MayActOnSite(ctx, q, siteID, person)
}
