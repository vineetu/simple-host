package domains

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
)

func (s Service) BindCustomDomain(ctx context.Context, siteID string, domain string) (*model.SiteDomainInfo, string, error) {
	return repo.BindCustomDomain(ctx, s.DB, siteID, domain, s.Deps.PlatformDomains...)
}
func (s Service) ClaimPlatformSubdomain(ctx context.Context, siteID string, host string) ([]string, error) {
	return repo.ClaimPlatformSubdomain(ctx, s.DB, siteID, host, s.Deps.PlatformDomains...)
}
func (s Service) DropCustomDomain(ctx context.Context, siteID string, expected string) (dropped, restored string, err error) {
	return repo.DropCustomDomain(ctx, s.DB, siteID, expected, s.Deps.PlatformDomains...)
}
func (s Service) LapseDomain(ctx context.Context, siteID string, domain string) (bool, error) {
	return repo.LapseDomain(ctx, s.DB, siteID, domain, s.Deps.PlatformDomains...)
}
func (s Service) LapsePreviousDomain(ctx context.Context, siteID string, prev string) (bool, error) {
	return repo.LapsePreviousDomain(ctx, s.DB, siteID, prev, s.Deps.PlatformDomains...)
}
func (s Service) ListDomainsToCheck(ctx context.Context, activeAge time.Duration) ([]model.BoundDomain, error) {
	return repo.ListDomainsToCheck(ctx, s.DB, activeAge)
}
func (s Service) PromoteDomain(ctx context.Context, siteID string) (string, error) {
	return repo.PromoteDomain(ctx, s.DB, siteID, s.Deps.PlatformDomains...)
}
func (s Service) PruneDomainCertRequests(ctx context.Context) error {
	return repo.PruneDomainCertRequests(ctx, s.DB)
}
func (s Service) ReleaseExpiredDomains(ctx context.Context, maxAge time.Duration, ttl time.Duration) ([]model.SiteDomainInfo, error) {
	return repo.ReleaseExpiredDomains(ctx, s.DB, maxAge, ttl)
}
func (s Service) SetDomainStatus(ctx context.Context, siteID string, domain string, status string, lastErr string, certStatus string) (model.DomainCheck, error) {
	return repo.SetDomainStatus(ctx, s.DB, siteID, domain, status, lastErr, certStatus)
}
func (s Service) SetPreviousDomainCheck(ctx context.Context, siteID string, prev string, ok bool) (sql.NullTime, error) {
	return repo.SetPreviousDomainCheck(ctx, s.DB, siteID, prev, ok)
}
