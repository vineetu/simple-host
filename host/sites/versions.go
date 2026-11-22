package sites

import (
	"context"
	"log"

	"github.com/vsriram/simple-host/host/sites/repo"
)

// PruneInput holds the values for PruneVersions.
type PruneInput struct {
	SiteID, UserID, SiteName string
	ActiveVersion, SiteKeep  int
}

// PruneVersions removes database rows before their version directories.
func (s *Service) PruneVersions(ctx context.Context, in PruneInput) []int {
	siteID, userID, siteName, activeVersion, siteKeep := in.SiteID, in.UserID, in.SiteName, in.ActiveVersion, in.SiteKeep
	keep, err := s.Retention(ctx, userID, siteKeep)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	versions, err := repo.ListVersionsBySite(ctx, s.DB, siteID)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	keepFrom := s.RetainedFloor(versions, keep, activeVersion, siteKeep)
	if keepFrom < 2 {
		return nil
	}
	if keepFrom = s.PinnedFloor(ctx, userID, siteName, keepFrom); keepFrom < 2 {
		return nil
	}
	removed, err := repo.PruneVersions(ctx, s.DB, siteID, keepFrom, activeVersion)
	if err != nil {
		log.Printf("prune versions for %s: %v", siteName, err)
		return nil
	}
	for _, n := range removed {
		if err := s.Disk.DeleteVersion(userID, siteName, n); err != nil {
			log.Printf("prune versions for %s: v%d row removed but files remain: %v", siteName, n, err)
		}
	}
	return removed
}

// Retention is how many versions the account keeps for a site whose own
// setting is siteKeep (host/access decides it).
func (s *Service) Retention(ctx context.Context, userID string, siteKeep int) (int, error) {
	keep, _, err := s.Accounts.SiteRetention(ctx, userID, siteKeep, s.Deps.DefaultRetention, s.Deps.Limits)
	return keep, err
}

// EffectiveKeepVersions selects the site retention or the instance default.
func EffectiveKeepVersions(siteKeep, instanceKeep int) int {
	if siteKeep > 0 {
		return siteKeep
	}
	return instanceKeep
}

// MaxReadPaths is the most files one read of version texts takes (GET
// …/files?read=); update_site's edits read at most this many files at once.
const MaxReadPaths = 50
