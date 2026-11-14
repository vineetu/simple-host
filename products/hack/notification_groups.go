package hack

import (
	"context"
	"database/sql"
	notifications "github.com/vsriram/simple-host/internal/app/notifications"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// NotificationGroups supplies the event membership and website queries.
type NotificationGroups struct{}

var _ notifications.GroupSites = NotificationGroups{}

func (NotificationGroups) SiteFor(ctx context.Context, q sqlkit.Querier, member, name, group string, scope ...string) (string, error) {
	return hackevents.HackNotificationSiteID(ctx, q, member, name, group, scope...)
}
func (NotificationGroups) Allowed(ctx context.Context, q sqlkit.Querier, site, member string, creating bool) (bool, error) {
	return hackevents.HackNotificationAllowed(ctx, q, site, member, creating)
}
func (NotificationGroups) ContextExists(ctx context.Context, q sqlkit.Querier, member, name, group, scope string) (bool, error) {
	return hackevents.HackNotificationContextExists(ctx, q, member, name, group, scope)
}
func (NotificationGroups) LockMember(ctx context.Context, tx *sql.Tx, site, member string) error {
	return hackevents.LockNotificationMember(ctx, tx, site, member)
}
func (NotificationGroups) GroupLabel(ctx context.Context, q sqlkit.Querier, account string) (string, error) {
	return hackevents.SiteEventSlug(ctx, q, account)
}
func (NotificationGroups) Labels(ctx context.Context, q sqlkit.Querier, sites []string) (map[string]string, error) {
	return hackevents.NotificationLabels(ctx, q, sites)
}
