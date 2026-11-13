package accounts

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	events "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func (s *Service) HackSiteOwnerOnSite(ctx context.Context, siteID, userID string) (bool, error) {
	return events.HackSiteOwnerOnSite(ctx, s.database, siteID, userID)
}
func (s *Service) GetEventByAccount(ctx context.Context, accountID string) (model.Event, error) {
	return events.GetEventByAccount(ctx, s.database, accountID)
}
