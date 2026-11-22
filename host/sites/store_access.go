package sites

import (
	"context"
	"database/sql"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// DomainHeldByDeletedSite reports whether host is the own address (current or
// earlier) of a site in Recently deleted, which keeps it held until purge.
func (svc *Service) DomainHeldByDeletedSite(ctx context.Context, host string) (bool, error) {
	return repo.DomainHeldByDeletedSite(ctx, svc.DB, host, accounts.HostForms(host))
}

// GetFamilyByID is one family by id. sql.ErrNoRows if none.
func (svc *Service) GetFamilyByID(ctx context.Context, id string) (model.AddressFamily, error) {
	return repo.GetFamilyByID(ctx, svc.DB, id)
}

// GetHandleBySiteName returns the URL-safe handle of the OLDEST owner of a site
// named `name` — i.e. the account the legacy <name>.<siteDomain> host used to
// serve. Used to 301 a deprecated name-subdomain to its canonical path URL.
// Returns sql.ErrNoRows if no such site; the handle is "" if the owner has none.
func (svc *Service) GetHandleBySiteName(ctx context.Context, name string) (string, error) {
	return repo.GetHandleBySiteName(ctx, svc.DB, name)
}

// GetRetiredName looks host up in legacy_hostnames. sql.ErrNoRows if absent.
func (svc *Service) GetRetiredName(ctx context.Context, host string) (model.RetiredName, error) {
	return repo.GetRetiredName(ctx, svc.DB, host, accounts.HostForms(host))
}

// GetSiteByCustomDomain looks up the site a host serves as its own domain
// (lowercased match): its bound custom domain, or the earlier proven address
// it keeps serving at while a new domain is pending. For the latter, Domain is
// that earlier address and the binding counts as verified. Returns
// sql.ErrNoRows when none.
func (svc *Service) GetSiteByCustomDomain(ctx context.Context, domain string) (model.SiteDomainInfo, error) {
	return repo.GetSiteByCustomDomain(ctx, svc.DB, domain, accounts.HostForms(domain))
}

func (svc *Service) GetSiteByUser(ctx context.Context, userID string, name string) (model.Site, error) {
	return repo.GetSiteByUser(ctx, svc.DB, userID, name)
}

func (svc *Service) GetSiteDomainInfo(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	return repo.GetSiteDomainInfo(ctx, svc.DB, siteID)
}

// GetSiteOwner returns the owner's handle ("" if none), user id and the
// site's name for siteID. Returns sql.ErrNoRows for an unknown site.
func (svc *Service) GetSiteOwner(ctx context.Context, siteID string) (handle, userID, name string, err error) {
	return repo.GetSiteOwner(ctx, svc.DB, siteID)
}

// HomeSite loads the selected site, including offline and taken-down rows.
func (svc *Service) HomeSite(ctx context.Context, userID string) (model.Site, bool, error) {
	return repo.HomeSite(ctx, svc.DB, userID)
}

// ListAllFamilies is every family on the instance (admin view).
func (svc *Service) ListAllFamilies(ctx context.Context) ([]model.AddressFamily, error) {
	return repo.ListAllFamilies(ctx, svc.DB)
}

// ListFamiliesByUser is the account's families, most specific first.
func (svc *Service) ListFamiliesByUser(ctx context.Context, userID string) ([]model.AddressFamily, error) {
	return repo.ListFamiliesByUser(ctx, svc.DB, userID)
}

func (svc *Service) ListSitesByUser(ctx context.Context, userID string) ([]model.Site, error) {
	return repo.ListSitesByUser(ctx, svc.DB, userID)
}

// ResolveOldSiteName returns the current name of the site that used to be
// called name in the account. sql.ErrNoRows when the name was never a renamed
// site's, or that site is in Recently deleted.
func (svc *Service) ResolveOldSiteName(ctx context.Context, userID string, name string) (siteID, current string, err error) {
	return repo.ResolveOldSiteName(ctx, svc.DB, userID, name)
}

// GetSiteByCustomDomain and HomeSite answer sign-in, which has no Service.
func GetSiteByCustomDomain(ctx context.Context, db *sql.DB, domain string) (model.SiteDomainInfo, error) {
	return repo.GetSiteByCustomDomain(ctx, db, domain, accounts.HostForms(domain))
}
func HomeSite(ctx context.Context, db *sql.DB, userID string) (model.Site, bool, error) {
	return repo.HomeSite(ctx, db, userID)
}

func (svc *Service) GetSiteByID(ctx context.Context, id string) (model.Site, error) {
	return repo.GetSiteByID(ctx, svc.DB, id)
}

func (svc *Service) GetDeletedSiteByUser(ctx context.Context, userID, name string) (repo.DeletedSite, error) {
	return repo.GetDeletedSiteByUser(ctx, svc.DB, userID, name)
}

// MoveSiteBase moves stored platform addresses while preserving reserved names.
func (svc *Service) MoveSiteBase(ctx context.Context, from, to string, apply bool) (repo.MoveSiteBaseReport, error) {
	return repo.MoveSiteBase(ctx, svc.DB, from, to, rules.LabelReserved, apply)
}
