package sites

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"github.com/vsriram/simple-host/host/model"
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
	keep, err := s.retention(ctx, userID, siteKeep)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, siteID)
	if err != nil {
		log.Printf("prune versions: %v", err)
		return nil
	}
	keepFrom := s.retainedFloor(versions, keep, activeVersion, siteKeep)
	if keepFrom < 2 {
		return nil
	}
	if keepFrom = s.pinnedFloor(ctx, userID, siteName, keepFrom); keepFrom < 2 {
		return nil
	}
	removed, err := repo.PruneVersions(ctx, s.database, siteID, keepFrom, activeVersion)
	if err != nil {
		log.Printf("prune versions for %s: %v", siteName, err)
		return nil
	}
	for _, n := range removed {
		if err := s.disk.DeleteVersion(userID, siteName, n); err != nil {
			log.Printf("prune versions for %s: v%d row removed but files remain: %v", siteName, n, err)
		}
	}
	return removed
}

// CheckRetentionAllowed authorizes the retention setting before decoding its body.
func (s *Service) CheckRetentionAllowed(ctx context.Context, user *model.User) error {
	if !s.deps.Limits.CanSetKeepVersions(s.accounts.SiteAccountHandles(ctx, user)...) {
		return &Error{Code: "keep_versions_fixed", Message: fmt.Sprintf("Simple Host keeps your %d latest versions", s.deps.DefaultRetention)}
	}
	return nil
}

// KeepVersionsInput holds the values for SetKeepVersions.
type KeepVersionsInput struct {
	UserID, Name string
	Keep         int
}

// SetKeepVersions updates a site's retention and applies it while holding its lock.
func (s *Service) SetKeepVersions(ctx context.Context, in KeepVersionsInput) (model.Site, int, []int, error) {
	userID, name, keep := in.UserID, in.Name, in.Keep
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &Error{Code: "site_not_found"}
		}
		return site, 0, nil, err
	}
	if site.Suspended() {
		return site, 0, nil, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := repo.SetSiteKeepVersions(ctx, s.database, site.ID, keep); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &Error{Code: "not_found", Message: "site not found"}
		}
		return site, 0, nil, err
	}
	effective, err := s.retention(ctx, userID, keep)
	if err != nil {
		return site, 0, nil, err
	}
	removed := s.PruneVersions(ctx, PruneInput{site.ID, site.UserID, site.Name, site.ActiveVersion, keep})
	if removed == nil {
		removed = []int{}
	}
	return site, effective, removed, nil
}

// retention is how many versions the account keeps for a site whose own
// setting is siteKeep (host/access decides it).
func (s *Service) retention(ctx context.Context, userID string, siteKeep int) (int, error) {
	keep, _, err := s.accounts.SiteRetention(ctx, userID, siteKeep, s.deps.DefaultRetention, s.deps.Limits)
	return keep, err
}

// EffectiveKeepVersions selects the site retention or the instance default.
func EffectiveKeepVersions(siteKeep, instanceKeep int) int {
	if siteKeep > 0 {
		return siteKeep
	}
	return instanceKeep
}

// DiscardInput holds the values for DiscardVersion.
type DiscardInput struct {
	UserID, Name, ExpectedSiteID string
	CheckSiteID                  bool
	Version                      int
}

// DiscardVersion tombstones a never-published version and deletes its files.
func (s *Service) DiscardVersion(ctx context.Context, in DiscardInput) (model.Site, error) {
	userID, name, expectedSiteID, checkSiteID, n := in.UserID, in.Name, in.ExpectedSiteID, in.CheckSiteID, in.Version
	unlockAccount := s.lockAccount(userID)
	defer unlockAccount()
	unlock := s.lockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.database, userID, name)
	if err == nil && checkSiteID && site.ID != expectedSiteID {
		err = sql.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, &Error{Code: "site_not_found"}
		}
		return site, err
	}
	if site.Suspended() {
		return site, &Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, site.ID)
	if err != nil {
		return site, err
	}
	var found *model.Version
	for i := range versions {
		if versions[i].VersionNumber == n {
			found = &versions[i]
			break
		}
	}
	if found == nil || found.Status == "uploading" {
		return site, &Error{Code: "version_not_found"}
	}
	if n == site.ActiveVersion {
		return site, &Error{Code: "version_live", Message: fmt.Sprintf("version %d is live; only a version that was never live can be discarded", n)}
	}
	if found.Status == "active" {
		return site, &Error{Code: "version_published", Message: fmt.Sprintf("version %d has been live; kept versions are removed by retention, not discarded", n)}
	}
	gone, err := repo.DiscardReadyVersion(ctx, s.database, site.ID, n)
	if err != nil {
		return site, err
	}
	if !gone {
		return site, &Error{Code: "version_not_found"}
	}
	if err := s.disk.DeleteVersion(site.UserID, site.Name, n); err != nil {
		log.Printf("discard version for %s: v%d discarded but files remain: %v", site.Name, n, err)
	}
	return site, nil
}

// VersionListInput names the site whose versions are listed. ExpectedID,
// when CheckID is set, is the site an operator acting as the owner named;
// another site of that name is not found. Pending lists only versions
// stored with publish=false that never went live (the ones DiscardVersion
// removes).
type VersionListInput struct {
	UserID, Name, ExpectedID string
	CheckID, Pending         bool
}

// VersionRow is one version in the list; Active is the one visitors see.
type VersionRow struct {
	model.Version
	Active bool
}

// ListVersions lists a site's versions. Refusal: site_not_found.
func (s *Service) ListVersions(ctx context.Context, in VersionListInput) ([]VersionRow, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, in.UserID, in.Name)
	if err == nil && in.CheckID && site.ID != in.ExpectedID {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return nil, err
	}
	versions, err := repo.ListVersionsBySite(ctx, s.database, site.ID)
	if err != nil {
		return nil, err
	}
	rows := make([]VersionRow, 0, len(versions))
	for _, v := range versions {
		active := v.VersionNumber == site.ActiveVersion
		if in.Pending && (v.Status != "ready" || active) {
			continue
		}
		rows = append(rows, VersionRow{Version: v, Active: active})
	}
	return rows, nil
}
