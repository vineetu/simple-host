package sites

import (
	"context"

	"github.com/vsriram/simple-host/host/sites/repo"
)

// PreviewSiteInput selects a complete version for a team preview.
type PreviewSiteInput struct {
	AccountID, Name string
	Version         int
}

// PreviewSite reads the site and verifies that its requested version is kept.
func (s *Service) PreviewSite(ctx context.Context, in PreviewSiteInput) (repo.Site, bool, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, in.AccountID, in.Name)
	if err != nil {
		return repo.Site{}, false, err
	}
	exists, err := repo.VersionKept(ctx, s.database, site.ID, in.Version)
	return site, exists, err
}

// ExportSiteInput selects an owner's site by name or authenticates a token-bound site ID.
type ExportSiteInput struct {
	UserID, Name, SiteID string
	DownloadLink         bool
}

// ExportSite resolves the site for an archive or a download link.
func (s *Service) ExportSite(ctx context.Context, in ExportSiteInput) (repo.Site, error) {
	if !in.DownloadLink {
		return repo.GetSiteByUser(ctx, s.database, in.UserID, in.Name)
	}
	site, err := repo.GetSiteByID(ctx, s.database, in.SiteID)
	if err != nil || site.UserID != in.UserID || site.Deleted {
		return repo.Site{}, &Error{Code: "export_link_invalid"}
	}
	// A link minted before suspension stops with its owner; a site-only takedown still exports.
	if site.OwnerSuspended {
		return repo.Site{}, &Error{Code: "account_suspended"}
	}
	return site, nil
}

// ExportSites lists all instance sites for an administrator's archive.
func (s *Service) ExportSites(ctx context.Context) ([]repo.Site, error) {
	return repo.ListAllSites(ctx, s.database)
}
