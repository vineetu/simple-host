package db

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sites"
)

// Old names of renamed sites (site_name_aliases). A link to an old name
// redirects to the site's current address until a site of that name exists
// again: the caller looks the name up as a site first, and only a miss reaches
// ResolveSiteNameAlias.

// ResolveOldSiteName returns the current name of the site that used to be
// called name in the account. sql.ErrNoRows when the name was never a renamed
// site's, or that site is in Recently deleted.
func ResolveOldSiteName(ctx context.Context, q Querier, userID, name string) (siteID, current string, err error) {
	return sites.ResolveOldSiteName(ctx, q, userID, name)
}
