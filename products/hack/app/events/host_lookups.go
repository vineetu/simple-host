package events

import (
	"context"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func (s *Service) UserByID(ctx context.Context, id string) (model.User, error) {
	return accounts.UserByID(ctx, s.db, id)
}
func (s *Service) SiteByUser(ctx context.Context, accountID, name string) (model.Site, error) {
	return sites.SiteByName(ctx, s.db, accountID, name)
}
func (s *Service) SiteVersions(ctx context.Context, siteID string) ([]model.Version, error) {
	return sites.SiteVersions(ctx, s.db, siteID)
}
func (s *Service) SitePasscode(ctx context.Context, siteID string) (model.SitePasscodeRow, error) {
	return sites.SitePasscode(ctx, s.db, siteID)
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
