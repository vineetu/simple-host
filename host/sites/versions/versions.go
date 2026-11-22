package versions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// CheckRetentionAllowed authorizes the retention setting before decoding its body.
func (s Service) CheckRetentionAllowed(ctx context.Context, user *model.User) error {
	if !s.Deps.Limits.CanSetKeepVersions(s.Accounts.SiteAccountHandles(ctx, user)...) {
		return &sites.Error{Code: "keep_versions_fixed", Message: fmt.Sprintf("Simple Host keeps your %d latest versions", s.Deps.DefaultRetention)}
	}
	return nil
}

// KeepVersionsInput holds the values for SetKeepVersions.
type KeepVersionsInput struct {
	UserID, Name string
	Keep         int
}

// SetKeepVersions updates a site's retention and applies it while holding its lock.
func (s Service) SetKeepVersions(ctx context.Context, in KeepVersionsInput) (model.Site, int, []int, error) {
	userID, name, keep := in.UserID, in.Name, in.Keep
	unlock := s.LockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &sites.Error{Code: "site_not_found"}
		}
		return site, 0, nil, err
	}
	if site.Suspended() {
		return site, 0, nil, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	if err := repo.SetSiteKeepVersions(ctx, s.DB, site.ID, keep); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, 0, nil, &sites.Error{Code: "not_found", Message: "site not found"}
		}
		return site, 0, nil, err
	}
	effective, err := s.Retention(ctx, userID, keep)
	if err != nil {
		return site, 0, nil, err
	}
	removed := s.PruneVersions(ctx, sites.PruneInput{SiteID: site.ID, UserID: site.UserID, SiteName: site.Name, ActiveVersion: site.ActiveVersion, SiteKeep: keep})
	if removed == nil {
		removed = []int{}
	}
	return site, effective, removed, nil
}

// DiscardInput holds the values for DiscardVersion.
type DiscardInput struct {
	UserID, Name, ExpectedSiteID string
	CheckSiteID                  bool
	Version                      int
}

// DiscardVersion tombstones a never-published version and deletes its files.
func (s Service) DiscardVersion(ctx context.Context, in DiscardInput) (model.Site, error) {
	userID, name, expectedSiteID, checkSiteID, n := in.UserID, in.Name, in.ExpectedSiteID, in.CheckSiteID, in.Version
	unlockAccount := s.LockAccount(userID)
	defer unlockAccount()
	unlock := s.LockSite(userID, name)
	defer unlock()
	site, err := repo.GetSiteByUser(ctx, s.DB, userID, name)
	if err == nil && checkSiteID && site.ID != expectedSiteID {
		err = sql.ErrNoRows
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return site, &sites.Error{Code: "site_not_found"}
		}
		return site, err
	}
	if site.Suspended() {
		return site, &sites.Error{Code: "site_suspended", Message: site.SuspendedReason()}
	}
	versions, err := repo.ListVersionsBySite(ctx, s.DB, site.ID)
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
		return site, &sites.Error{Code: "version_not_found"}
	}
	if n == site.ActiveVersion {
		return site, &sites.Error{Code: "version_live", Message: fmt.Sprintf("version %d is live; only a version that was never live can be discarded", n)}
	}
	if found.Status == "active" {
		return site, &sites.Error{Code: "version_published", Message: fmt.Sprintf("version %d has been live; kept versions are removed by retention, not discarded", n)}
	}
	gone, err := repo.DiscardReadyVersion(ctx, s.DB, site.ID, n)
	if err != nil {
		return site, err
	}
	if !gone {
		return site, &sites.Error{Code: "version_not_found"}
	}
	if err := s.Disk.DeleteVersion(site.UserID, site.Name, n); err != nil {
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
func (s Service) ListVersions(ctx context.Context, in VersionListInput) ([]VersionRow, error) {
	site, err := repo.GetSiteByUser(ctx, s.DB, in.UserID, in.Name)
	if err == nil && in.CheckID && site.ID != in.ExpectedID {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &sites.Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return nil, err
	}
	versions, err := repo.ListVersionsBySite(ctx, s.DB, site.ID)
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
