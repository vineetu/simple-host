package notifications

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/notifications"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// GroupSites answers for held sites. Nil on a platform-only install.
type GroupSites interface {
	SiteFor(ctx context.Context, q sqlkit.Querier, member, name, group string, scope ...string) (string, error)
	Allowed(ctx context.Context, q sqlkit.Querier, siteID, member string, creating bool) (bool, error)
	ContextExists(ctx context.Context, q sqlkit.Querier, member, name, group, scope string) (bool, error)
	// LockMember runs before the site lock in the caller's transaction.
	LockMember(ctx context.Context, tx *sql.Tx, siteID, member string) error
	// GroupLabel returns sql.ErrNoRows when the account holds no group.
	GroupLabel(ctx context.Context, q sqlkit.Querier, accountID string) (string, error)
	// Labels returns labels only for groups' own websites, closing rows before returning.
	Labels(ctx context.Context, q sqlkit.Querier, siteIDs []string) (map[string]string, error)
}

func (s *Service) WithGroupSites(groups GroupSites) *Service {
	s.groups = groups
	return s
}

// GetNotificationOn reads and enriches with the same handle, including an
// uncommitted site created in the caller's transaction.
func (s *Service) GetNotificationOn(ctx context.Context, q sqlkit.Querier, id string) (model.Notification, error) {
	n, err := repo.GetNotification(ctx, q, id)
	if err != nil {
		return n, err
	}
	rows, err := s.labelNotifications(ctx, q, []model.Notification{n})
	if err != nil {
		return model.Notification{}, err
	}
	return rows[0], nil
}

func (s *Service) labelNotifications(ctx context.Context, q sqlkit.Querier, rows []model.Notification) ([]model.Notification, error) {
	if s.groups == nil || len(rows) == 0 {
		return rows, nil
	}
	ids := make([]string, len(rows))
	for i := range rows {
		ids[i] = rows[i].SiteID
	}
	labels, err := s.groups.Labels(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Event = labels[rows[i].SiteID]
	}
	return rows, nil
}
