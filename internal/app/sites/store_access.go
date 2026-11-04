package sites

import (
	"context"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	accounts "github.com/vsriram/simple-host/internal/repository/accounts"
	repo "github.com/vsriram/simple-host/internal/repository/sites"
)

// DeleteFamily removes a family (by id) and returns what it was.
func (svc *Service) DeleteFamily(ctx context.Context, id string) (model.AddressFamily, error) {
	return repo.DeleteFamily(ctx, svc.database, id)
}

// DomainHeldByDeletedSite reports whether host is the own address (current or
// earlier) of a site in Recently deleted, which keeps it held until purge.
func (svc *Service) DomainHeldByDeletedSite(ctx context.Context, host string) (bool, error) {
	return repo.DomainHeldByDeletedSite(ctx, svc.database, host, accounts.HostForms(host))
}

// GetFamilyByID is one family by id. sql.ErrNoRows if none.
func (svc *Service) GetFamilyByID(ctx context.Context, id string) (model.AddressFamily, error) {
	return repo.GetFamilyByID(ctx, svc.database, id)
}

// GetFamilyByUserSuffix is one account's family. sql.ErrNoRows if none.
func (svc *Service) GetFamilyByUserSuffix(ctx context.Context, userID string, suffix string) (model.AddressFamily, error) {
	return repo.GetFamilyByUserSuffix(ctx, svc.database, userID, suffix)
}

// GetHandleBySiteName returns the URL-safe handle of the OLDEST owner of a site
// named `name` — i.e. the account the legacy <name>.<siteDomain> host used to
// serve. Used to 301 a deprecated name-subdomain to its canonical path URL.
// Returns sql.ErrNoRows if no such site; the handle is "" if the owner has none.
func (svc *Service) GetHandleBySiteName(ctx context.Context, name string) (string, error) {
	return repo.GetHandleBySiteName(ctx, svc.database, name)
}

// GetRetiredName looks host up in legacy_hostnames. sql.ErrNoRows if absent.
func (svc *Service) GetRetiredName(ctx context.Context, host string) (model.RetiredName, error) {
	return repo.GetRetiredName(ctx, svc.database, host, accounts.HostForms(host))
}

// GetSiteByCustomDomain looks up the site a host serves as its own domain
// (lowercased match): its bound custom domain, or the earlier proven address
// it keeps serving at while a new domain is pending. For the latter, Domain is
// that earlier address and the binding counts as verified. Returns
// sql.ErrNoRows when none.
func (svc *Service) GetSiteByCustomDomain(ctx context.Context, domain string) (model.SiteDomainInfo, error) {
	return repo.GetSiteByCustomDomain(ctx, svc.database, domain, accounts.HostForms(domain))
}

// GetSiteByUser forwards to the sites repository.
func (svc *Service) GetSiteByUser(ctx context.Context, userID string, name string) (model.Site, error) {
	return repo.GetSiteByUser(ctx, svc.database, userID, name)
}

// GetSiteDomainInfo forwards to the sites repository.
func (svc *Service) GetSiteDomainInfo(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	return repo.GetSiteDomainInfo(ctx, svc.database, siteID)
}

// GetSiteOwner returns the owner's handle ("" if none), user id and the
// site's name for siteID. Returns sql.ErrNoRows for an unknown site.
func (svc *Service) GetSiteOwner(ctx context.Context, siteID string) (handle, userID, name string, err error) {
	return repo.GetSiteOwner(ctx, svc.database, siteID)
}

// GetSiteWriteGate returns the owning user_id and the admin override flag
// used by visitorWriteOK.
func (svc *Service) GetSiteWriteGate(ctx context.Context, siteID string) (userID string, allowAnonymous bool, err error) {
	return repo.GetSiteWriteGate(ctx, svc.database, siteID)
}

// HomeSite loads the selected site, including offline and taken-down rows.
func (svc *Service) HomeSite(ctx context.Context, userID string) (model.Site, bool, error) {
	return repo.HomeSite(ctx, svc.database, userID)
}

// ListAllFamilies is every family on the instance (admin view).
func (svc *Service) ListAllFamilies(ctx context.Context) ([]model.AddressFamily, error) {
	return repo.ListAllFamilies(ctx, svc.database)
}

// ListFamiliesByUser is the account's families, most specific first.
func (svc *Service) ListFamiliesByUser(ctx context.Context, userID string) ([]model.AddressFamily, error) {
	return repo.ListFamiliesByUser(ctx, svc.database, userID)
}

// ListFamiliesToCheck is every pending family, plus verified ones whose last
// check is older than recheck.
func (svc *Service) ListFamiliesToCheck(ctx context.Context, recheck time.Duration) ([]model.AddressFamily, error) {
	return repo.ListFamiliesToCheck(ctx, svc.database, recheck)
}

// ListSitesByUser forwards to the sites repository.
func (svc *Service) ListSitesByUser(ctx context.Context, userID string) ([]model.Site, error) {
	return repo.ListSitesByUser(ctx, svc.database, userID)
}

// MarkFamilyLapseNotified records that the owner was told their failing
// family will be let go at releaseAt.
func (svc *Service) MarkFamilyLapseNotified(ctx context.Context, id string, releaseAt time.Time) error {
	return repo.MarkFamilyLapseNotified(ctx, svc.database, id, releaseAt)
}

// ReleaseExpiredFamilies removes pending families older than ttl and
// returns them.
func (svc *Service) ReleaseExpiredFamilies(ctx context.Context, ttl time.Duration) ([]model.AddressFamily, error) {
	return repo.ReleaseExpiredFamilies(ctx, svc.database, ttl)
}

// ResolveOldSiteName returns the current name of the site that used to be
// called name in the account. sql.ErrNoRows when the name was never a renamed
// site's, or that site is in Recently deleted.
func (svc *Service) ResolveOldSiteName(ctx context.Context, userID string, name string) (siteID, current string, err error) {
	return repo.ResolveOldSiteName(ctx, svc.database, userID, name)
}

// SetFamilyCert sets the family's certificate mode and the operator's
// lineage name (admin only).
func (svc *Service) SetFamilyCert(ctx context.Context, id string, mode string, certName string) error {
	return repo.SetFamilyCert(ctx, svc.database, id, mode, certName)
}

// SetFamilyCheck records one check: ok clears any failure; otherwise a
// verified family starts (or keeps) its failing clock and a pending one keeps
// waiting with the reason.
func (svc *Service) SetFamilyCheck(ctx context.Context, id string, ok bool, reason string) (model.FamilyCheck, error) {
	return repo.SetFamilyCheck(ctx, svc.database, id, ok, reason)
}

// SetFamilyProofExempt turns the admin's "no TXT record needed" flag on or off.
func (svc *Service) SetFamilyProofExempt(ctx context.Context, id string, on bool) error {
	return repo.SetFamilyProofExempt(ctx, svc.database, id, on)
}

// UpdateFamily applies p to the account's family and returns it.
func (svc *Service) UpdateFamily(ctx context.Context, userID string, suffix string, p model.FamilyPatch) (model.AddressFamily, error) {
	return repo.UpdateFamily(ctx, svc.database, userID, suffix, p)
}
