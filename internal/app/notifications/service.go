package notifications

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	repo "github.com/vsriram/simple-host/internal/repository/notifications"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

type Service struct {
	database *sql.DB
	sites    SiteReader
	groups   GroupSites
}
type SiteReader interface {
	GetSiteByID(context.Context, string) (model.Site, error)
}

func New(database *sql.DB, sites SiteReader) *Service {
	return &Service{database: database, sites: sites}
}
func (s *Service) ClaimNotificationDelivery(ctx context.Context, lease time.Duration, maxAttempts int, historyDays int, window time.Duration) (model.NotificationDelivery, error) {
	return repo.ClaimNotificationDelivery(ctx, s.database, lease, maxAttempts, historyDays, window)
}
func (s *Service) ClaimNotificationEmail(ctx context.Context, id string) (bool, error) {
	return repo.ClaimNotificationEmail(ctx, s.database, id)
}
func (s *Service) DeferNotificationDelivery(ctx context.Context, d model.NotificationDelivery) error {
	return repo.DeferNotificationDelivery(ctx, s.database, d)
}
func (s *Service) DeleteNotification(ctx context.Context, id string) (bool, error) {
	return repo.DeleteNotification(ctx, s.database, id)
}
func (s *Service) EmitSiteEvent(ctx context.Context, site string, id string, name string, storage string, data []byte) error {
	return repo.EmitSiteEvent(ctx, s.database, site, id, name, storage, data)
}
func (s *Service) EmitSiteEventOnce(ctx context.Context, site string, id string, name string, data []byte, visitor string, within time.Duration) (bool, error) {
	return repo.EmitSiteEventOnce(ctx, s.database, site, id, name, data, visitor, within)
}
func (s *Service) EmitSiteEventFor(ctx context.Context, site string, id string, name string, storage string, data []byte, pushActor, pushTarget string) error {
	return repo.EmitSiteEventFor(ctx, s.database, site, id, name, storage, data, pushActor, pushTarget)
}
func (s *Service) ListPushDevices(ctx context.Context, site string) ([]model.Notification, error) {
	rows, err := repo.ListPushDevices(ctx, s.database, site)
	if err != nil {
		return nil, err
	}
	return s.labelNotifications(ctx, s.database, rows)
}
func (s *Service) ListVisitorPushDevices(ctx context.Context, site, visitorID string) ([]model.Notification, error) {
	rows, err := repo.ListVisitorPushDevices(ctx, s.database, site, visitorID)
	if err != nil {
		return nil, err
	}
	return s.labelNotifications(ctx, s.database, rows)
}
func (s *Service) UpsertPushSubscription(ctx context.Context, n model.Notification, newID string, max int) (string, bool, error) {
	return repo.UpsertPushSubscription(ctx, s.database, n, newID, max)
}
func (s *Service) DeleteNotificationForOwner(ctx context.Context, site, id, visitorID string) (bool, error) {
	return repo.DeleteNotificationForOwner(ctx, s.database, site, id, visitorID)
}
func (s *Service) DeletePushSubscriptionsForVisitor(ctx context.Context, site, visitorID string) (int64, error) {
	return repo.DeletePushSubscriptionsForVisitor(ctx, s.database, site, visitorID)
}
func (s *Service) DeletePushSubscriptionsForPerson(ctx context.Context, site, personID string) (int64, error) {
	return repo.DeletePushSubscriptionsForPerson(ctx, s.database, site, personID)
}
func (s *Service) DeletePushSubscriptionsForFamily(ctx context.Context, family string) (int64, error) {
	return repo.DeletePushSubscriptionsForFamily(ctx, s.database, family)
}
func (s *Service) DeletePushSubscriptionsForSite(ctx context.Context, site string) (int64, error) {
	return repo.DeletePushSubscriptionsForSite(ctx, s.database, site)
}
func (s *Service) SetMutedResources(ctx context.Context, id string, resources []string) error {
	return repo.SetMutedResources(ctx, s.database, id, resources)
}
func (s *Service) GetPushRule(ctx context.Context, site, resource string) (model.PushRule, error) {
	return repo.GetPushRule(ctx, s.database, site, resource)
}
func (s *Service) ListPushRules(ctx context.Context, site string) ([]model.PushRule, error) {
	return repo.ListPushRules(ctx, s.database, site)
}
func (s *Service) SetPushRule(ctx context.Context, rule model.PushRule) error {
	return repo.SetPushRule(ctx, s.database, rule)
}
func (s *Service) FinishNotificationDelivery(ctx context.Context, d model.NotificationDelivery, state string, category string, status int, duration int, next time.Time, window time.Duration, minAttempts int, failPercent int, failFor time.Duration) (bool, error) {
	return repo.FinishNotificationDelivery(ctx, s.database, d, state, category, status, duration, next, window, minAttempts, failPercent, failFor)
}
func (s *Service) GetNotification(ctx context.Context, id string) (model.Notification, error) {
	return s.GetNotificationOn(ctx, s.database, id)
}
func (s *Service) HasNotifications(ctx context.Context, site string) (bool, error) {
	return repo.HasNotifications(ctx, s.database, site)
}
func (s *Service) HasNotificationsFor(ctx context.Context, site string, name string) (bool, error) {
	return repo.HasNotificationsFor(ctx, s.database, site, name)
}
func (s *Service) ListNotifications(ctx context.Context, owner string, site string) ([]model.Notification, error) {
	rows, err := repo.ListNotifications(ctx, s.database, owner, site)
	if err != nil {
		return nil, err
	}
	return s.labelNotifications(ctx, s.database, rows)
}
func (s *Service) NotificationCursor(ctx context.Context, id string, site string) (int64, error) {
	return repo.NotificationCursor(ctx, s.database, id, site)
}
func (s *Service) NotificationHistory(ctx context.Context, id string, days int) ([]json.RawMessage, error) {
	return repo.NotificationHistory(ctx, s.database, id, days)
}
func (s *Service) NotificationLeaseHeld(ctx context.Context, d model.NotificationDelivery) (bool, error) {
	return repo.NotificationLeaseHeld(ctx, s.database, d)
}
func (s *Service) PauseNotification(ctx context.Context, id string, paused bool) error {
	return repo.PauseNotification(ctx, s.database, id, paused)
}
func (s *Service) PendingNotificationDeliveries(ctx context.Context) (int64, error) {
	return repo.PendingNotificationDeliveries(ctx, s.database)
}
func (s *Service) RecentSiteEvent(ctx context.Context, site string, name string, visitor string, within time.Duration) (bool, error) {
	return repo.RecentSiteEvent(ctx, s.database, site, name, visitor, within)
}
func (s *Service) RecentlyVerifiedNotification(ctx context.Context, owner string, url string) (bool, error) {
	return repo.RecentlyVerifiedNotification(ctx, s.database, owner, url)
}
func (s *Service) RotateNotification(ctx context.Context, id string, secret string, secrets model.NotificationSecrets) (*time.Time, error) {
	return repo.RotateNotification(ctx, s.database, id, secret, secrets)
}
func (s *Service) SaveNotification(ctx context.Context, n model.Notification, max int, cursor *int64, historyDays int, secrets model.NotificationSecrets) (bool, bool, error) {
	return repo.SaveNotification(ctx, s.database, n, max, cursor, historyDays, secrets, notificationAccess{s.groups})
}
func (s *Service) SealNotificationSecrets(ctx context.Context, seal func(string, string) (string, error)) (int64, error) {
	return repo.SealNotificationSecrets(ctx, s.database, seal)
}
func (s *Service) SweepNotifications(ctx context.Context, days int) error {
	return repo.SweepNotifications(ctx, s.database, days)
}
func (s *Service) SweepNotificationsCount(ctx context.Context, days int) (int64, error) {
	return repo.SweepNotificationsCount(ctx, s.database, days)
}
func (s *Service) GroupNotificationSite(ctx context.Context, member, name, event string, teamEvent ...string) (model.Site, error) {
	if s.groups == nil {
		return model.Site{}, sql.ErrNoRows
	}
	id, err := s.groups.SiteFor(ctx, s.database, member, name, event, teamEvent...)
	if err != nil {
		return model.Site{}, err
	}
	return s.sites.GetSiteByID(ctx, id)
}
func (s *Service) GroupNotificationAllowed(ctx context.Context, site string, member string, creating bool) (bool, error) {
	if s.groups == nil {
		return false, nil
	}
	return s.groups.Allowed(ctx, s.database, site, member, creating)
}
func (s *Service) GroupNotificationContextExists(ctx context.Context, member string, name string, event string, scope string) (bool, error) {
	if s.groups == nil {
		return false, nil
	}
	return s.groups.ContextExists(ctx, s.database, member, name, event, scope)
}
func (s *Service) EventAccountFlag(ctx context.Context, userID string) (bool, error) {
	return accounts.EventAccountFlag(ctx, s.database, userID)
}

func (s *Service) VerifiedAt(ctx context.Context, owner, url string) (time.Time, error) {
	return repo.VerifiedAt(ctx, s.database, owner, url)
}
func (s *Service) GroupLabel(ctx context.Context, account string) (string, error) {
	if s.groups == nil {
		return "", sql.ErrNoRows
	}
	return s.groups.GroupLabel(ctx, s.database, account)
}

func (a notificationAccess) LockNotificationMember(ctx context.Context, tx *sql.Tx, site, member string) error {
	if a.groups == nil {
		return model.ErrNotificationAccess
	}
	return a.groups.LockMember(ctx, tx, site, member)
}

type notificationAccess struct{ groups GroupSites }

func (a notificationAccess) GroupNotificationAllowed(ctx context.Context, q sqlkit.Querier, site, member string, creating bool) (bool, error) {
	if a.groups == nil {
		return false, nil
	}
	return a.groups.Allowed(ctx, q, site, member, creating)
}
