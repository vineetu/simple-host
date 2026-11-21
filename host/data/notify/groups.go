package notify

import (
	"context"

	accounts "github.com/vsriram/simple-host/host/access"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// MayActOnSite reports whether person may still keep a subscription on the
// site: they own it, or may act as its owner (accounts.ActsAsOwner: a member
// of the team that holds it, or an editor of the site).
func MayActOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error) {
	site, err := sitesrepo.GetSiteByID(ctx, q, siteID)
	if err != nil {
		return false, err
	}
	return accounts.ActsAsOwner(ctx, q, person, site.UserID, site.Name)
}

type notificationAccess struct{}

func (notificationAccess) ActsOnSite(ctx context.Context, q sqlkit.Querier, siteID, person string) (bool, error) {
	return MayActOnSite(ctx, q, siteID, person)
}
