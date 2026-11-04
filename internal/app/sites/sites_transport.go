package sites

import (
	"context"
	"encoding/json"
	rules "github.com/vsriram/simple-host/internal/rules/sites"

	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

func (s *Service) ListDeletedSitesByUser(ctx context.Context, userID string) ([]model.DeletedSite, error) {
	return repo.ListDeletedSitesByUser(ctx, s.database, userID)
}

func (s *Service) VersionKept(ctx context.Context, siteID string, version int) (bool, error) {
	return repo.VersionKept(ctx, s.database, siteID, version)
}

func (s *Service) ListVersionsBySite(ctx context.Context, siteID string) ([]model.Version, error) {
	return repo.ListVersionsBySite(ctx, s.database, siteID)
}

func (s *Service) GetSiteIDByName(ctx context.Context, name string) (string, error) {
	return repo.GetSiteIDByName(ctx, s.database, name)
}

func (s *Service) GetSiteStateByID(ctx context.Context, siteID string) (json.RawMessage, int, error) {
	return repo.GetSiteStateByID(ctx, s.database, siteID)
}

func (s *Service) BackfillPreviewExpiry(ctx context.Context, username string, ttlHours int) (int64, error) {
	return repo.BackfillPreviewExpiry(ctx, s.database, username, ttlHours)
}

func (s *Service) GetAllowedOriginsByID(ctx context.Context, siteID string) ([]string, error) {
	return repo.GetAllowedOriginsByID(ctx, s.database, siteID)
}

func (s *Service) GetSiteStateVersionByID(ctx context.Context, siteID string) (int, error) {
	return repo.GetSiteStateVersionByID(ctx, s.database, siteID)
}

func (s *Service) SetHomeSite(ctx context.Context, userID string, name *string) error {
	if name != nil && !rules.ValidSiteName.MatchString(*name) {
		return &Error{Code: "invalid_name", Message: "send site as a name or null"}
	}
	return repo.SetHomeSite(ctx, s.database, userID, name)
}

func (s *Service) ShowcasePreferences(ctx context.Context, userID string) (map[string]model.ShowcasePreference, error) {
	return repo.ShowcasePreferences(ctx, s.database, userID)
}

func (s *Service) GetShowcasePreference(ctx context.Context, userID, name string) (model.ShowcasePreference, error) {
	return repo.GetShowcasePreference(ctx, s.database, userID, name)
}

func (s *Service) SetShowcasePreference(ctx context.Context, userID, name string, pinned *bool, order *int) (model.ShowcasePreference, error) {
	return repo.SetShowcasePreference(ctx, s.database, userID, name, pinned, order)
}

func (s *Service) ListHandlesWithSites(ctx context.Context) ([]string, error) {
	return repo.ListHandlesWithSites(ctx, s.database)
}

func (s *Service) SetAllowedOrigins(ctx context.Context, siteID, origins string) error {
	return repo.SetAllowedOrigins(ctx, s.database, siteID, origins)
}

func (s *Service) SetAllowAnonymousWrites(ctx context.Context, siteID string, allow bool) error {
	return repo.SetAllowAnonymousWrites(ctx, s.database, siteID, allow)
}

func (s *Service) SiteOffline(ctx context.Context, siteID string) (bool, error) {
	return repo.SiteOffline(ctx, s.database, siteID)
}

func (s *Service) SetSiteOffline(ctx context.Context, siteID string, on bool) error {
	return repo.SetSiteOffline(ctx, s.database, siteID, on)
}

func (s *Service) ListSiteIdleFlags(ctx context.Context, userID string) (map[string]model.SiteIdleFlags, error) {
	return repo.ListSiteIdleFlags(ctx, s.database, userID)
}

func (s *Service) AllowDomainCertRequest(ctx context.Context, userID, domain string, dailyCap int) (bool, error) {
	tx, err := s.database.BeginTx(ctx, nil)
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
