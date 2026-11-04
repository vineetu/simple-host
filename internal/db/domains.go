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

func BindCustomDomain(ctx context.Context, database *sql.DB, siteID, domain string) (*SiteDomainInfo, string, error) {
	return repo.BindCustomDomain(ctx, database, siteID, domain, platformDomains()...)
}
func BoundDomainOf(info SiteDomainInfo) BoundDomain { return model.BoundDomainOf(info) }
func GetRetiredName(ctx context.Context, database *sql.DB, host string) (RetiredName, error) {
	return repo.GetRetiredName(ctx, database, host, hostForms(host))
}
func GetSiteByCustomDomain(ctx context.Context, database *sql.DB, domain string) (SiteDomainInfo, error) {
	return repo.GetSiteByCustomDomain(ctx, database, domain, hostForms(domain))
}
func ListDomainsToCheck(ctx context.Context, database *sql.DB, activeAge time.Duration) ([]BoundDomain, error) {
	return repo.ListDomainsToCheck(ctx, database, activeAge)
}
func ReleaseExpiredDomains(ctx context.Context, database *sql.DB, maxAge, ttl time.Duration) ([]SiteDomainInfo, error) {
	return repo.ReleaseExpiredDomains(ctx, database, maxAge, ttl)
}

// RetireSiteNames records the site's claimed <name>.<SITE_DOMAIN> addresses
// (current and earlier) as retired names before the site is deleted; the
// rows outlive it (site_id becomes NULL). Returns every domain the site had,
// for the caller to unbind on disk.
func RetireSiteNames(ctx context.Context, q Querier, siteID string) ([]string, error) {
	return repo.RetireSiteNames(ctx, q, siteID, platformDomains()...)
}
