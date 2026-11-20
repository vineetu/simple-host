package notify

import (
	"context"

	accounts "github.com/vsriram/simple-host/host/access"
	repo "github.com/vsriram/simple-host/host/data/repo/notify"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// GetNotificationOn reads a subscription, including one whose site was
// created in the caller's transaction.
func (s *Service) GetNotificationOn(ctx context.Context, q sqlkit.Querier, id string) (model.Notification, error) {
	return repo.GetNotification(ctx, q, id)
}

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
