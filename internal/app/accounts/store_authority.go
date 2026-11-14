package accounts

import (
	"context"

	events "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

func (s *Service) HackSiteOwnerOnSite(ctx context.Context, siteID, userID string) (bool, error) {
	return events.HackSiteOwnerOnSite(ctx, s.database, siteID, userID)
}
