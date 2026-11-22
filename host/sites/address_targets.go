package sites

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

var ErrInvalidHost = errors.New("invalid site host")

// HostOwner resolves only addressable account/site label pairs, before alias fallback.
func (s *Service) HostOwner(ctx context.Context, handle, name string) (model.User, error) {
	if !HandleAddressable(handle) || !rules.ValidSiteName.MatchString(name) {
		return model.User{}, ErrInvalidHost
	}
	return accounts.GetUserByHandle(ctx, s.database, handle)
}

// SiteHostAddress decides whether the site's host may be served and builds its hostname.
func SiteHostAddress(handle, name, base string, enabled, personAddress, ready bool) string {
	if !enabled || !personAddress || !rules.ValidSiteName.MatchString(name) || strings.HasPrefix(name, "xn--") || !ready {
		return ""
	}
	return strings.ToLower(name) + "." + strings.ToLower(handle) + "." + strings.ToLower(base)
}

// PhotoTarget checks naming before looking up the destination for a photo upload.
func (s *Service) PhotoTarget(ctx context.Context, userID, name string) (model.Site, error) {
	if err := rules.ValidateShape(name); err != nil {
		return model.Site{}, &Error{Code: "invalid_name", Message: "Invalid site name."}
	}
	return repo.GetSiteByUser(ctx, s.database, userID, name)
}

// AddressedSite resolves a DNS-label site name without querying malformed names.
func (s *Service) AddressedSite(ctx context.Context, userID, name string) (model.Site, error) {
	if !rules.ValidSiteName.MatchString(name) {
		return model.Site{}, sql.ErrNoRows
	}
	return repo.GetSiteByUser(ctx, s.database, userID, name)
}

// AddressedOldSite resolves a retired DNS-label name at the address boundary.
func (s *Service) AddressedOldSite(ctx context.Context, userID, name string) (string, string, error) {
	if !rules.ValidSiteName.MatchString(name) {
		return "", "", sql.ErrNoRows
	}
	return repo.ResolveOldSiteName(ctx, s.database, userID, name)
}

// AdminSiteTarget resolves the current site and owner before adapting an admin action.
func (s *Service) AdminSiteTarget(ctx context.Context, id string, deleting bool) (model.Site, model.User, error) {
	site, err := repo.GetSiteByID(ctx, s.database, id)
	if err == nil && site.Deleted {
		err = sql.ErrNoRows
	}
	var owner model.User
	if err == nil {
		owner, err = accounts.GetUserByID(ctx, s.database, site.UserID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return site, owner, &Error{Code: "site_not_found", Message: "site not found"}
	}
	if err != nil {
		return site, owner, err
	}
	if err := rules.ValidateShape(site.Name); err != nil && deleting {
		return site, owner, &Error{Code: "legacy_site_name", Message: "this site's name (" + site.Name + ") is from before the current naming rules, so it cannot be deleted from here; its owner can rename it first, or take it down instead"}
	}
	return site, owner, nil
}

// ContentPathOwner resolves a path-model owner only after both path labels pass
// their existing boundary rules, including aliases of current handles.
func (s *Service) ContentPathOwner(ctx context.Context, handle, name string) (model.User, error) {
	if !ContentPathValid(handle, name) {
		return model.User{}, sql.ErrNoRows
	}
	return accounts.GetUserByHandleOrAlias(ctx, s.database, handle)
}

// ContentPathValid applies the path-model naming rules shared by database and
// copied routing facts. Path handles need not be addressable DNS labels.
func ContentPathValid(handle, name string) bool {
	return rules.VisitorHandle.MatchString(handle) && rules.ValidSiteName.MatchString(name)
}

// LiveURL is the one address to hand out for a site, in serving's own
// priority: its verified own domain; the domain it is moving from while the
// new one is not verified; its canonical family address; its platform
// address (siteURL).
func LiveURL(s model.Site, familyAddress, siteURL string) string {
	switch {
	case s.CustomDomain.String != "" && s.DomainVerifiedAt.Valid:
		return "https://" + s.CustomDomain.String + "/"
	case s.CustomDomain.String != "" && s.PreviousDomain != "":
		return "https://" + s.PreviousDomain + "/"
	case familyAddress != "":
		return familyAddress
	}
	return siteURL
}
