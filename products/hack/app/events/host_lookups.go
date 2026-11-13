package events

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/accounts"
	"github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// KeyResolver is injected by the composition root so Hack shares account authentication.
type KeyResolver func(context.Context, string, time.Duration) (model.User, error)

func (s *Service) SetKeyResolver(resolve KeyResolver) { s.resolveKey = resolve }
func (s *Service) UserByAPIKey(ctx context.Context, key string, idle time.Duration) (model.User, error) {
	return s.resolveKey(ctx, key, idle)
}
func (s *Service) UserByID(ctx context.Context, id string) (model.User, error) {
	return accounts.GetUserByID(ctx, s.db, id)
}
func (s *Service) AccountHandles(ctx context.Context, user *model.User) []string {
	handles := []string{user.Handle.String}
	if aliases, err := accounts.ListHandleAliases(ctx, s.db, user.ID); err == nil {
		handles = append(handles, aliases...)
	}
	return handles
}
func (s *Service) SiteByUser(ctx context.Context, accountID, name string) (model.Site, error) {
	return sites.GetSiteByUser(ctx, s.db, accountID, name)
}
func (s *Service) SiteVersions(ctx context.Context, siteID string) ([]model.Version, error) {
	return sites.ListVersionsBySite(ctx, s.db, siteID)
}
func (s *Service) SitePasscode(ctx context.Context, siteID string) (model.SitePasscodeRow, error) {
	return sites.GetSitePasscode(ctx, s.db, siteID)
}
func (s *Service) PendingContentMail(ctx context.Context) (int64, error) {
	return hackevents.PendingContentMail(ctx, s.db)
}

// HandleVerdict describes the shared handle namespace check for an event name.
type HandleVerdict struct {
	Status int
	Code   string
}
type HandleChecker func(context.Context, sqlkit.Querier, string) (HandleVerdict, error)

func (s *Service) SetHandleChecker(check HandleChecker) { s.checkHandle = check }
func (s *Service) JudgeHandle(ctx context.Context, name string) (HandleVerdict, error) {
	if s.checkHandle == nil {
		return HandleVerdict{}, nil
	}
	return s.checkHandle(ctx, s.db, name)
}
