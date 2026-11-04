package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"time"
)

type SiteDomainInfo = model.SiteDomainInfo
type BoundDomain = model.BoundDomain
type DomainCheck = model.DomainCheck
type RetiredName = model.RetiredName

var ErrDomainTaken = model.ErrDomainTaken

func AllowDomainCertRequest(ctx context.Context, database *sql.DB, userID, domain string, dailyCap int) (bool, error) {
	return repo.AllowDomainCertRequest(ctx, database, userID, domain, dailyCap)
}
func BindCustomDomain(ctx context.Context, database *sql.DB, siteID, domain string) (*SiteDomainInfo, string, error) {
	return repo.BindCustomDomain(ctx, database, siteID, domain, platformDomains()...)
}
func BoundDomainOf(info SiteDomainInfo) BoundDomain { return model.BoundDomainOf(info) }
func ClaimPlatformSubdomain(ctx context.Context, database *sql.DB, siteID, host string) ([]string, error) {
	return repo.ClaimPlatformSubdomain(ctx, database, siteID, host, platformDomains()...)
}
func DomainBoundToOtherSite(ctx context.Context, q Querier, domain, siteID string) (bool, error) {
	return repo.DomainBoundToOtherSite(ctx, q, domain, siteID)
}
func DropCustomDomain(ctx context.Context, database *sql.DB, siteID, expected string) (dropped, restored string, err error) {
	return repo.DropCustomDomain(ctx, database, siteID, expected, platformDomains()...)
}
func GetRetiredName(ctx context.Context, database *sql.DB, host string) (RetiredName, error) {
	return repo.GetRetiredName(ctx, database, host, hostForms(host))
}
func GetSiteByCustomDomain(ctx context.Context, database *sql.DB, domain string) (SiteDomainInfo, error) {
	return repo.GetSiteByCustomDomain(ctx, database, domain, hostForms(domain))
}
func GetSiteOwnerEmail(ctx context.Context, database *sql.DB, siteID string) (string, error) {
	return repo.GetSiteOwnerEmail(ctx, database, siteID)
}
func LapseDomain(ctx context.Context, database *sql.DB, siteID, domain string) (bool, error) {
	return repo.LapseDomain(ctx, database, siteID, domain, platformDomains()...)
}
func LapsePreviousDomain(ctx context.Context, database *sql.DB, siteID, prev string) (bool, error) {
	return repo.LapsePreviousDomain(ctx, database, siteID, prev, platformDomains()...)
}
func ListDomainsToCheck(ctx context.Context, database *sql.DB, activeAge time.Duration) ([]BoundDomain, error) {
	return repo.ListDomainsToCheck(ctx, database, activeAge)
}
func MarkDomainLapseNotified(ctx context.Context, database *sql.DB, siteID, domain string, releaseAt time.Time) error {
	return repo.MarkDomainLapseNotified(ctx, database, siteID, domain, releaseAt)
}
func PromoteDomain(ctx context.Context, database *sql.DB, siteID string) (string, error) {
	return repo.PromoteDomain(ctx, database, siteID, platformDomains()...)
}
func PruneDomainCertRequests(ctx context.Context, database *sql.DB) error {
	return repo.PruneDomainCertRequests(ctx, database)
}
func ReleaseExpiredDomains(ctx context.Context, database *sql.DB, maxAge, ttl time.Duration) ([]SiteDomainInfo, error) {
	return repo.ReleaseExpiredDomains(ctx, database, maxAge, ttl)
}
func SetCustomDomain(ctx context.Context, database Querier, siteID, domain string) error {
	return repo.SetCustomDomain(ctx, database, siteID, domain)
}
func SetDomainStatus(ctx context.Context, database *sql.DB, siteID, domain, status, lastErr, certStatus string) (DomainCheck, error) {
	return repo.SetDomainStatus(ctx, database, siteID, domain, status, lastErr, certStatus)
}
func SetPreviousDomainCheck(ctx context.Context, database *sql.DB, siteID, prev string, ok bool) (sql.NullTime, error) {
	return repo.SetPreviousDomainCheck(ctx, database, siteID, prev, ok)
}

// retirePlatformName keeps a released claimed <name>.<SITE_DOMAIN> with its
// site: it is recorded in legacy_hostnames, so the name redirects to the
// site's current address (or says the site was removed) instead of passing to
// whichever site happens to share its name, and nobody else can claim it.
// Any other host is ignored.
func retirePlatformName(ctx context.Context, q Querier, host, siteID, userID string) error {
	return repo.RetirePlatformName(ctx, q, host, siteID, userID, platformDomains()...)
}

// RetireSiteNames records the site's claimed <name>.<SITE_DOMAIN> addresses
// (current and earlier) as retired names before the site is deleted; the
// rows outlive it (site_id becomes NULL). Returns every domain the site had,
// for the caller to unbind on disk.
func RetireSiteNames(ctx context.Context, q Querier, siteID string) ([]string, error) {
	return repo.RetireSiteNames(ctx, q, siteID, platformDomains()...)
}

// isPlatformName: host is exactly one label under a platform domain.
func isPlatformName(host string) bool {
	_, ok := platformNameLabel(host)
	return ok
}
