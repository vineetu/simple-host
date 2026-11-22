package sites

import (
	"context"
	"log"

	"github.com/vsriram/simple-host/host/sites/repo"
)

// IsSiteOwner reports whether userID is the account that owns the site: a
// visitor sign-in by the owner marks their address as the owner's own
// (owner marks). A failed lookup is logged and reads as not the owner.
func (s *Service) IsSiteOwner(ctx context.Context, siteID, userID string) bool {
	owner, err := repo.SiteOwner(ctx, s.DB, siteID)
	if err != nil {
		log.Printf("owner marks: site owner site=%s: %v", siteID, err)
		return false
	}
	return owner == userID
}
