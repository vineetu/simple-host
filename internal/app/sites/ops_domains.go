package sites

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"time"
)

func (s *Service) AllowDomainCertRequest(ctx context.Context, userID string, domain string, dailyCap int) (bool, error) {
	return repo.AllowDomainCertRequest(ctx, s.database, userID, domain, dailyCap)
}
func (s *Service) BindCustomDomain(ctx context.Context, siteID string, domain string) (*model.SiteDomainInfo, string, error) {
	return repo.BindCustomDomain(ctx, s.database, siteID, domain, s.deps.PlatformDomains...)
}
func (s *Service) ClaimPlatformSubdomain(ctx context.Context, siteID string, host string) ([]string, error) {
	return repo.ClaimPlatformSubdomain(ctx, s.database, siteID, host, s.deps.PlatformDomains...)
}
func (s *Service) DomainBoundToOtherSite(ctx context.Context, domain string, siteID string) (bool, error) {
	return repo.DomainBoundToOtherSite(ctx, s.database, domain, siteID)
}
func (s *Service) DropCustomDomain(ctx context.Context, siteID string, expected string) (dropped, restored string, err error) {
	return repo.DropCustomDomain(ctx, s.database, siteID, expected, s.deps.PlatformDomains...)
}
func (s *Service) GetSiteOwnerEmail(ctx context.Context, siteID string) (string, error) {
	return repo.GetSiteOwnerEmail(ctx, s.database, siteID)
}
func (s *Service) LapseDomain(ctx context.Context, siteID string, domain string) (bool, error) {
	return repo.LapseDomain(ctx, s.database, siteID, domain, s.deps.PlatformDomains...)
}
func (s *Service) LapsePreviousDomain(ctx context.Context, siteID string, prev string) (bool, error) {
	return repo.LapsePreviousDomain(ctx, s.database, siteID, prev, s.deps.PlatformDomains...)
}
func (s *Service) ListDomainsToCheck(ctx context.Context, activeAge time.Duration) ([]model.BoundDomain, error) {
	return repo.ListDomainsToCheck(ctx, s.database, activeAge)
}
func (s *Service) MarkDomainLapseNotified(ctx context.Context, siteID string, domain string, releaseAt time.Time) error {
	return repo.MarkDomainLapseNotified(ctx, s.database, siteID, domain, releaseAt)
}
func (s *Service) PromoteDomain(ctx context.Context, siteID string) (string, error) {
	return repo.PromoteDomain(ctx, s.database, siteID, s.deps.PlatformDomains...)
}
func (s *Service) PruneDomainCertRequests(ctx context.Context) error {
	return repo.PruneDomainCertRequests(ctx, s.database)
}
func (s *Service) ReleaseExpiredDomains(ctx context.Context, maxAge time.Duration, ttl time.Duration) ([]model.SiteDomainInfo, error) {
	return repo.ReleaseExpiredDomains(ctx, s.database, maxAge, ttl)
}
func (s *Service) SetCustomDomain(ctx context.Context, siteID string, domain string) error {
	return repo.SetCustomDomain(ctx, s.database, siteID, domain)
}
func (s *Service) SetDomainStatus(ctx context.Context, siteID string, domain string, status string, lastErr string, certStatus string) (model.DomainCheck, error) {
	return repo.SetDomainStatus(ctx, s.database, siteID, domain, status, lastErr, certStatus)
}
func (s *Service) SetPreviousDomainCheck(ctx context.Context, siteID string, prev string, ok bool) (sql.NullTime, error) {
	return repo.SetPreviousDomainCheck(ctx, s.database, siteID, prev, ok)
}

func (s *Service) WithPlatformDomains(domains []string) *Service {
	s.deps.PlatformDomains = append([]string(nil), domains...)
	return s
}
