package sites

import (
	"context"
	"database/sql"
	"errors"

	rules "github.com/vsriram/simple-host/host/rules/sites"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

func (s *Service) ListDeletedSitesByUser(ctx context.Context, userID string) ([]model.DeletedSite, error) {
	return repo.ListDeletedSitesByUser(ctx, s.DB, userID)
}

func (s *Service) VersionKept(ctx context.Context, siteID string, version int) (bool, error) {
	return repo.VersionKept(ctx, s.DB, siteID, version)
}

func (s *Service) ListVersionsBySite(ctx context.Context, siteID string) ([]model.Version, error) {
	return repo.ListVersionsBySite(ctx, s.DB, siteID)
}

func (s *Service) GetSiteIDByName(ctx context.Context, name string) (string, error) {
	return repo.GetSiteIDByName(ctx, s.DB, name)
}

func (s *Service) BackfillPreviewExpiry(ctx context.Context, username string, ttlHours int) (int64, error) {
	return repo.BackfillPreviewExpiry(ctx, s.DB, username, ttlHours)
}

func (s *Service) GetAllowedOriginsByID(ctx context.Context, siteID string) ([]string, error) {
	return repo.GetAllowedOriginsByID(ctx, s.DB, siteID)
}

// SetHomeSite makes the account's site name (nil: none) its home page and
// returns the home as it now is. Refusals: invalid_name, home_content_host
// (a site that depends on the content host), not_found (no such site).
func (s *Service) SetHomeSite(ctx context.Context, user *model.User, name *string) (model.Site, bool, error) {
	if name != nil && !rules.ValidSiteName.MatchString(*name) {
		return model.Site{}, false, &Error{Code: "invalid_name", Message: "send site as a name or null"}
	}
	if name != nil && ContentHostOnly(user.Handle.String, *name) {
		return model.Site{}, false, &Error{Code: "home_content_host", Message: "this site depends on the content host"}
	}
	if err := repo.SetHomeSite(ctx, s.DB, user.ID, name); errors.Is(err, sql.ErrNoRows) {
		return model.Site{}, false, &Error{Code: "not_found", Message: "site not found"}
	} else if err != nil {
		return model.Site{}, false, err
	}
	return s.HomeSite(ctx, user.ID)
}

func (s *Service) ShowcasePreferences(ctx context.Context, userID string) (map[string]model.ShowcasePreference, error) {
	return repo.ShowcasePreferences(ctx, s.DB, userID)
}

// GetShowcasePreference is one site's showcase pin and order; Error code
// not_found when the account has no such site.
func (s *Service) GetShowcasePreference(ctx context.Context, userID, name string) (model.ShowcasePreference, error) {
	return showcasePreference(repo.GetShowcasePreference(ctx, s.DB, userID, name))
}

// SetShowcasePreference sets one site's showcase pin and/or order; Error
// code not_found when the account has no such site.
func (s *Service) SetShowcasePreference(ctx context.Context, userID, name string, pinned *bool, order *int) (model.ShowcasePreference, error) {
	return showcasePreference(repo.SetShowcasePreference(ctx, s.DB, userID, name, pinned, order))
}

func showcasePreference(pref model.ShowcasePreference, err error) (model.ShowcasePreference, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return pref, &Error{Code: "not_found", Message: "site not found"}
	}
	return pref, err
}

func (s *Service) ListHandlesWithSites(ctx context.Context) ([]string, error) {
	return repo.ListHandlesWithSites(ctx, s.DB)
}

func (s *Service) SiteOffline(ctx context.Context, siteID string) (bool, error) {
	return repo.SiteOffline(ctx, s.DB, siteID)
}

func (s *Service) AllowDomainCertRequest(ctx context.Context, userID, domain string, dailyCap int) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := repo.LockDomainCertAccount(ctx, tx, userID); err != nil {
		return false, err
	}
	again, n, err := repo.DomainCertRequestCounts(ctx, tx, userID, domain)
	if err != nil {
		return false, err
	}
	if again {
		return true, nil
	}
	if n >= dailyCap {
		return false, nil
	}
	if err := repo.RecordDomainCertRequest(ctx, tx, userID, domain); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
