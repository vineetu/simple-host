package sites

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access"
	accounts "github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/sites/repo"
)

// ErrBareName: the request host does not name a site, so a bare site name is
// resolved by the caller (the name alone, or the caller's own site).
var ErrBareName = errors.New("bare site name")

// SiteHostSite resolves a site host to its site: the handle is a current
// handle, the site is that account's, and it is live on its site host.
func (h *Hosts) SiteHostSite(ctx context.Context, host string) (model.Site, model.User, bool, error) {
	if !h.addr.SiteHostsOn() {
		return model.Site{}, model.User{}, false, nil
	}
	label, handle, base, ok := h.SiteHostParts(ctx, host)
	if !ok {
		return model.Site{}, model.User{}, false, nil
	}
	user, err := h.hostOwner(ctx, handle, label)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, ErrInvalidHost) {
		return model.Site{}, model.User{}, false, nil
	}
	if err != nil {
		return model.Site{}, model.User{}, false, err
	}
	site, err := repo.GetSiteByUser(ctx, h.db, user.ID, label)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Site{}, model.User{}, false, nil
	}
	if err != nil {
		return model.Site{}, model.User{}, false, err
	}
	if !h.addr.SiteHostLiveOn(user.Handle.String, site.Name, base) {
		return model.Site{}, model.User{}, false, nil
	}
	return site, user, true, nil
}

func (h *Hosts) hostOwner(ctx context.Context, handle, name string) (model.User, error) {
	if !HandleAddressable(handle) || !rules.ValidSiteName.MatchString(name) {
		return model.User{}, ErrInvalidHost
	}
	return accounts.GetUserByHandle(ctx, h.db, handle)
}

// SiteOwnDomain is the site's proven own domain (a verified custom domain or
// claimed name). While a new domain is pending, it is the earlier proven
// address, which keeps serving until the new one is verified.
func (h *Hosts) SiteOwnDomain(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	info, ok, err := repo.GetSiteDomainInfo(ctx, h.db, siteID)
	if err != nil {
		return model.SiteDomainInfo{}, false, err
	}
	if !ok || info.Domain == "" {
		return model.SiteDomainInfo{}, false, nil
	}
	if info.VerifiedAt.Valid {
		return info, true, nil
	}
	if info.PreviousDomain != "" {
		return info.PreviousDomainView(time.Now()), true, nil
	}
	return model.SiteDomainInfo{}, false, nil
}

// SiteOwnAddress is the site's main address when it is not its site host or
// person address: its proven own domain, else its most specific canonical
// family address (Domain is that host, counted as verified).
func (h *Hosts) SiteOwnAddress(ctx context.Context, siteID string) (model.SiteDomainInfo, bool, error) {
	info, has, err := h.SiteOwnDomain(ctx, siteID)
	if err != nil || has || !h.HasLiveFamilies() {
		return info, has, err
	}
	_, userID, name, err := repo.GetSiteOwner(ctx, h.db, siteID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SiteDomainInfo{}, false, nil
	}
	if err != nil {
		return model.SiteDomainInfo{}, false, err
	}
	host, ok := h.SiteFamilyAddress(userID, name)
	if !ok {
		return model.SiteDomainInfo{}, false, nil
	}
	return model.SiteDomainInfo{SiteID: siteID, UserID: userID, Name: name, Domain: host, Status: "active",
		VerifiedAt: sql.NullTime{Time: time.Now(), Valid: true}, CertStatus: "live"}, true, nil
}

// SiteAddressFor is a site's current address with an escaped path appended:
// its proven own address, else the address every tool hands out for it.
func (h *Hosts) SiteAddressFor(ctx context.Context, siteID, escapedPath string) (string, bool) {
	handle, userID, name, err := repo.GetSiteOwner(ctx, h.db, siteID)
	if err != nil {
		return "", false // gone, or in Recently deleted
	}
	return h.SiteAddressOf(ctx, model.Site{ID: siteID, UserID: userID, Name: name, OwnerHandle: handle}, escapedPath)
}

// SiteAddressOf is SiteAddressFor for a site row the caller already read:
// every name in it comes from that row.
func (h *Hosts) SiteAddressOf(ctx context.Context, site model.Site, escapedPath string) (string, bool) {
	rest := strings.TrimLeft(escapedPath, "/")
	if info, has, err := h.SiteOwnDomain(ctx, site.ID); err == nil && has {
		return "https://" + strings.ToLower(info.Domain) + "/" + rest, true
	}
	if h.HasLiveFamilies() {
		if host, ok := h.SiteFamilyAddress(site.UserID, site.Name); ok {
			return "https://" + strings.ToLower(host) + "/" + rest, true
		}
	}
	handle, name := site.OwnerHandle, site.Name
	if handle == "" {
		return "", false
	}
	if h.addr.PersonHostsCanonical() && h.addr.PersonAddressFor(handle, name) {
		return h.addr.SiteAddressWithPath(handle, name, "/"+rest), true
	}
	return strings.TrimSuffix(h.addr.SiteURL(handle, name), "/") + "/" + rest, true
}

// FamilyRenamedTarget: label under f names an old name of a renamed site of
// f's owner. The site's current address with escapedPath: under the same
// family when its new name still has the prefix, else its main address.
func (h *Hosts) FamilyRenamedTarget(ctx context.Context, f model.AddressFamily, label, escapedPath string) (string, bool) {
	siteID, _, err := repo.ResolveOldSiteName(ctx, h.db, f.UserID, f.SitePrefix+label)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Printf("family host: old name %s%s: %v", f.SitePrefix, label, err)
		}
		return "", false
	}
	if _, has, _ := h.SiteOwnDomain(ctx, siteID); !has {
		if _, _, name, err := repo.GetSiteOwner(ctx, h.db, siteID); err == nil {
			if nl, ok := strings.CutPrefix(name, f.SitePrefix); ok && FamilyLabelOK(f, nl, h.limits) {
				return "https://" + nl + "." + f.Suffix + "/" + strings.TrimLeft(escapedPath, "/"), true
			}
		}
	}
	return h.SiteAddressFor(ctx, siteID, escapedPath)
}

// BoundPlatformSite is the site bound to a claimed platform subdomain host,
// when that binding is proven.
func (h *Hosts) BoundPlatformSite(ctx context.Context, host string) (model.SiteDomainInfo, bool) {
	if !h.IsPlatformSubdomain(host) {
		return model.SiteDomainInfo{}, false
	}
	info, err := h.siteByDomain(ctx, host)
	if err != nil || !info.VerifiedAt.Valid {
		return model.SiteDomainInfo{}, false
	}
	return info, true
}

// IsHomeSiteHost: host is the person host whose owner chose siteID as the
// home site, and that site is live.
func (h *Hosts) IsHomeSiteHost(ctx context.Context, siteID, host string) bool {
	if h.hosted {
		return false
	}
	owner, ok := h.PersonHostOwner(ctx, host)
	if !ok {
		return false
	}
	s, has, err := repo.HomeSite(ctx, h.db, owner.ID)
	return err == nil && has && s.ID == siteID && !s.Deleted && s.Live()
}

// LivesOnDomainElsewhere reports whether a request on host (a shared,
// person, site or family host) is for a site whose home is a domain of its
// own: such a site takes no saves and no sign-in there. Returns the domain.
func (h *Hosts) LivesOnDomainElsewhere(ctx context.Context, host, siteID string) (string, bool) {
	if h.IsHomeSiteHost(ctx, siteID, host) {
		return "", false
	}
	if h.IsFamilyHost(host) {
		info, ok, err := h.SiteOwnDomain(ctx, siteID)
		if err != nil || !ok || strings.EqualFold(info.Domain, host) {
			return "", false
		}
		return info.Domain, true
	}
	if !h.IsSiteHost(ctx, host) && !h.IsPersonHost(ctx, host) {
		return "", false
	}
	info, ok, err := h.SiteOwnAddress(ctx, siteID)
	if err != nil || !ok {
		return "", false
	}
	return info.Domain, true
}

// ReturnSite resolves a sign-in return_to on a family, site or person host
// (p is the return path) to the site the session binds to. The site must
// live there, not on a domain of its own.
func (h *Hosts) ReturnSite(ctx context.Context, host, p string) (string, bool) {
	switch {
	case h.IsFamilyHost(host):
		return h.familyReturnSite(ctx, host)
	case h.IsSiteHost(ctx, host):
		return h.siteReturnSite(ctx, host)
	}
	owner, ok := h.PersonHostOwner(ctx, host)
	if !ok {
		return "", false
	}
	if h.hosted {
		ev, err := h.GroupByAccount(ctx, owner.ID)
		if err != nil || !ev.CustomWebsite || ev.TakenDown {
			return "", false
		}
		site, err := repo.GetSiteByUser(ctx, h.db, owner.ID, ev.ID)
		return site.ID, err == nil
	}
	home, has, err := repo.HomeSite(ctx, h.db, owner.ID)
	if err != nil {
		return "", false
	}
	if has {
		return home.ID, home.Live()
	}
	seg, _, _ := strings.Cut(strings.TrimPrefix(path.Clean("/"+p), "/"), "/")
	if !rules.ValidSiteName.MatchString(seg) {
		return "", false
	}
	site, err := repo.GetSiteByUser(ctx, h.db, owner.ID, seg)
	if err != nil || !h.addr.PersonAddressFor(owner.Handle.String, site.Name) {
		return "", false
	}
	if h.addr.SiteHostCanonicalOn(owner.Handle.String, site.Name, h.HostBase(ctx, host)) {
		return "", false // it signs visitors in on its own site host
	}
	if _, has, err := h.SiteOwnAddress(ctx, site.ID); err != nil || has {
		return "", false
	}
	return site.ID, true
}

func (h *Hosts) siteReturnSite(ctx context.Context, host string) (string, bool) {
	site, _, ok, err := h.SiteHostSite(ctx, host)
	if err != nil || !ok {
		return "", false
	}
	if _, has, err := h.SiteOwnAddress(ctx, site.ID); err != nil || has {
		return "", false
	}
	return site.ID, true
}

// familyReturnSite also requires the host to resolve to this server right
// now: a family whose DNS moved away must not receive a sign-in.
func (h *Hosts) familyReturnSite(ctx context.Context, host string) (string, bool) {
	m, ok, err := h.FamilySiteForHost(ctx, host)
	if err != nil || !ok {
		return "", false
	}
	if _, has, err := h.SiteOwnDomain(ctx, m.Site.ID); err != nil || has {
		return "", false
	}
	if !h.PointsHere(ctx, host) {
		return "", false
	}
	return m.Site.ID, true
}

// ResolveSite is the site a page-facing request names on host. A site host
// or family host names one site; a person host names its owner's sites (or
// home site, or group site); a site's own domain names that site; with a
// {handle} route the name is that account's. siteHost says the site host
// named it. ErrBareName when the host names nothing and there is no handle.
func (h *Hosts) ResolveSite(ctx context.Context, host, handle, name string) (site model.Site, siteHost bool, err error) {
	if h.IsSiteHost(ctx, host) {
		s, owner, ok, err := h.SiteHostSite(ctx, host)
		if err != nil {
			return model.Site{}, false, err
		}
		if !ok || s.Name != name {
			return model.Site{}, false, sql.ErrNoRows
		}
		if err := h.handleIs(ctx, handle, owner.ID); err != nil {
			return model.Site{}, false, err
		}
		return s, true, nil
	}
	if h.IsFamilyHost(host) && !h.IsBoundDomain(ctx, host) {
		m, ok, err := h.FamilySiteForHost(ctx, host)
		if err != nil {
			return model.Site{}, false, err
		}
		if !ok || (name != m.Site.Name && name != m.Label) {
			return model.Site{}, false, sql.ErrNoRows
		}
		return m.Site, false, h.handleIs(ctx, handle, m.Site.UserID)
	}
	owner, onPerson := h.PersonHostOwner(ctx, host)
	if onPerson {
		s, err := h.personHostSite(ctx, owner, handle, name)
		return s, false, err
	}
	if handle != "" {
		u, err := accounts.GetUserByHandleOrAlias(ctx, h.db, handle)
		if err != nil {
			return model.Site{}, false, err
		}
		s, err := repo.GetSiteByUser(ctx, h.db, u.ID, name)
		return s, false, err
	}
	if host != "" && !strings.EqualFold(host, h.addr.ContentHost) && !h.IsApexHost(host) {
		if info, err := h.siteByDomain(ctx, host); err == nil {
			label, _, isPlatform := h.UserHostLabel(host)
			if info.Name == name || (isPlatform && label == name) {
				return model.Site{ID: info.SiteID, UserID: info.UserID, Name: info.Name}, false, nil
			}
			return model.Site{}, false, sql.ErrNoRows
		}
	}
	return model.Site{}, false, ErrBareName
}

// handleIs refuses a {handle} route that is not the account ownerID (an old
// handle kept as an alias counts).
func (h *Hosts) handleIs(ctx context.Context, handle, ownerID string) error {
	if handle == "" {
		return nil
	}
	u, err := accounts.GetUserByHandleOrAlias(ctx, h.db, handle)
	if err != nil {
		return err
	}
	if u.ID != ownerID {
		return sql.ErrNoRows
	}
	return nil
}

// personHostSite is the site name means on owner's person host: the home
// site when one is chosen (under its name or the handle), the group's site
// on the hosted-events platform, else owner's site of that name.
func (h *Hosts) personHostSite(ctx context.Context, owner model.User, handle, name string) (model.Site, error) {
	if !h.eventSites {
		home, has, err := repo.HomeSite(ctx, h.db, owner.ID)
		if err != nil {
			return model.Site{}, err
		}
		if has {
			if home.Name != name && owner.Handle.String != name {
				return model.Site{}, sql.ErrNoRows
			}
			if err := h.handleIs(ctx, handle, owner.ID); err != nil {
				return model.Site{}, sql.ErrNoRows
			}
			return home, nil
		}
	}
	if handle != "" {
		if err := h.handleIs(ctx, handle, owner.ID); err != nil {
			return model.Site{}, err
		}
		return repo.GetSiteByUser(ctx, h.db, owner.ID, name)
	}
	if h.eventSites {
		ev, err := h.GroupByAccount(ctx, owner.ID)
		if err != nil || ev.Slug != name || !ev.CustomWebsite || ev.TakenDown {
			return model.Site{}, sql.ErrNoRows
		}
		return repo.GetSiteByUser(ctx, h.db, owner.ID, ev.ID)
	}
	return repo.GetSiteByUser(ctx, h.db, owner.ID, name)
}

// BareSite is a bare site name with no host to bind it. On the
// hosted-events platform every event has teams of the same names, so it is
// writer's own site (writer: the request key's account, or nil) or nothing;
// elsewhere it is the oldest site of that name.
func (h *Hosts) BareSite(ctx context.Context, name string, writer *model.User) (model.Site, error) {
	if h.eventSites {
		return h.WriterSite(ctx, name, writer)
	}
	id, err := repo.GetSiteIDByName(ctx, h.db, name)
	return model.Site{ID: id}, err
}

// WriterSite is writer's own site of that name (sql.ErrNoRows when writer
// is nil or has none).
func (h *Hosts) WriterSite(ctx context.Context, name string, writer *model.User) (model.Site, error) {
	if writer == nil || writer.ID == "" {
		return model.Site{}, sql.ErrNoRows
	}
	return repo.GetSiteByUser(ctx, h.db, writer.ID, name)
}

// OriginAllowed reports whether a credentialed request from origin may use
// siteID's saved data when it arrives on requestHost: the content host (only
// on a shared host, where no session is valid), the site's site host,
// family address, person address, own domain, or an origin the owner
// allowed for this site (never the content host).
func (h *Hosts) OriginAllowed(ctx context.Context, siteID, origin, requestHost string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	content := strings.EqualFold(parsed.Host, h.addr.ContentHost)
	return (content && h.IsSharedHost(requestHost)) ||
		h.OriginIsSiteHost(ctx, siteID, parsed.Host) ||
		h.originIsFamilyHost(ctx, siteID, parsed) ||
		h.OriginIsPersonHost(ctx, siteID, parsed.Host) ||
		h.originIsBoundDomain(ctx, siteID, parsed.Host) ||
		(!content && h.OriginListed(ctx, siteID, origin))
}

func (h *Hosts) OriginListed(ctx context.Context, siteID, origin string) bool {
	origins, err := repo.GetAllowedOriginsByID(ctx, h.db, siteID)
	if err != nil {
		return false
	}
	o := strings.ToLower(strings.TrimRight(origin, "/"))
	for _, a := range origins {
		a = strings.ToLower(strings.TrimRight(a, "/"))
		if a == o || h.SameOriginAcrossBases(a, o) {
			return true
		}
	}
	return false
}

// SameOriginAcrossBases: two origins that differ only in the base domain of
// a person, site or claimed-name host, same scheme and port.
func (h *Hosts) SameOriginAcrossBases(a, b string) bool {
	if !h.addr.BaseSplit() {
		return false
	}
	ua, err := url.Parse(a)
	if err != nil || ua.Host == "" {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil || ub.Host == "" {
		return false
	}
	if ua.Scheme != ub.Scheme || ua.Port() != ub.Port() {
		return false
	}
	t := h.TwinHost(ua.Hostname())
	return t != "" && strings.EqualFold(t, ub.Hostname())
}

// originIsBoundDomain is keyed by site id so a domain bound to one
// same-named site cannot authorize writes to another. The earlier address
// a site still serves at while a new one is pending counts too.
func (h *Hosts) originIsBoundDomain(ctx context.Context, siteID, host string) bool {
	info, ok, err := repo.GetSiteDomainInfo(ctx, h.db, siteID)
	if err != nil || !ok {
		return false
	}
	return h.SameUserHost(info.Domain, host) || (info.PreviousDomain != "" && h.SameUserHost(info.PreviousDomain, host))
}

// OriginIsPersonHost: host is the person address of the site's owner.
func (h *Hosts) OriginIsPersonHost(ctx context.Context, siteID, host string) bool {
	if !h.addr.PersonHostsOn() {
		return false
	}
	if h.IsHomeSiteHost(ctx, siteID, host) {
		return true
	}
	if owner, ok := h.PersonHostOwner(ctx, host); ok {
		_, has, err := repo.HomeSite(ctx, h.db, owner.ID)
		if err != nil || has {
			return false
		}
	}
	handle, _, name, err := repo.GetSiteOwner(ctx, h.db, siteID)
	if err != nil || !h.addr.PersonAddressFor(handle, name) {
		return false
	}
	if !h.eventSites && h.addr.SiteHostCanonicalOn(handle, name, h.HostBase(ctx, host)) {
		return false
	}
	return h.SameUserHost(h.addr.PersonHostOn(handle, h.addr.HandoutBase()), host)
}

// OriginIsSiteHost: host is the site's own site host while it serves it.
func (h *Hosts) OriginIsSiteHost(ctx context.Context, siteID, host string) bool {
	if !h.addr.SiteHostsOn() || !h.IsSiteHost(ctx, host) {
		return false
	}
	handle, _, name, err := repo.GetSiteOwner(ctx, h.db, siteID)
	if err != nil || !h.addr.SiteHostLiveOn(handle, name, h.HostBase(ctx, host)) {
		return false
	}
	return h.SameUserHost(h.addr.SiteHostOn(handle, name, h.addr.HandoutBase()), host)
}

// originIsFamilyHost: the origin is one of the site's family addresses
// (https, default port) while the family serves it.
func (h *Hosts) originIsFamilyHost(ctx context.Context, siteID string, origin *url.URL) bool {
	if origin.Scheme != "https" || origin.Port() != "" || !h.IsFamilyHost(origin.Hostname()) {
		return false
	}
	m, ok, err := h.FamilySiteForHost(ctx, origin.Hostname())
	return err == nil && ok && m.Site.ID == siteID
}

// GroupTLSHostExists is the on-demand TLS allowlist on the hosted-events
// platform: the content host, a served base, its www, a group's host, or a
// live team site under it (two-level or flat).
func (h *Hosts) GroupTLSHostExists(ctx context.Context, host string) bool {
	if host == strings.ToLower(h.addr.ContentHost) {
		return true
	}
	for _, base := range h.ServedBases() {
		if host == base || host == "www."+base {
			return true
		}
		if !strings.HasSuffix(host, "."+base) {
			continue
		}
		label := strings.TrimSuffix(host, "."+base)
		if h.addr.TeamSiteFlatHostsOn() && h.FlatLabelNotAHandle(ctx, label) {
			if team, event, ok := TeamSiteHostLabels(label); ok {
				return h.groupSiteExists(ctx, event, team)
			}
		}
		labels := strings.Split(label, ".")
		if len(labels) < 1 || len(labels) > 2 {
			return false
		}
		if _, err := h.GroupBySlug(ctx, labels[len(labels)-1]); err != nil {
			return false
		}
		if len(labels) == 1 {
			return true
		}
		return h.groupSiteExists(ctx, labels[1], labels[0])
	}
	return false
}

// groupSiteExists: team is a real team of event with a live site.
func (h *Hosts) groupSiteExists(ctx context.Context, eventSlug, teamSlug string) bool {
	ev, err := h.GroupBySlug(ctx, eventSlug)
	if err != nil {
		return false
	}
	if ok, err := h.groupHasSite(ctx, ev.ID, teamSlug); err != nil || !ok {
		return false
	}
	site, err := repo.GetSiteByUser(ctx, h.db, ev.AccountID, teamSlug)
	return err == nil && site.HasVersion()
}

// GateSite is what the passcode and named-viewers rules read about a site
// and this server: whether it is served on an address of its own.
func (h *Hosts) GateSite(site model.Site, noVisitorSignIn bool) access.GateSite {
	own := !h.addr.SharedOrigin() && !ContentHostOnly(site.OwnerHandle, site.Name) && h.addr.PersonAddressFor(site.OwnerHandle, site.Name)
	return access.GateSite{OwnAddress: own, EventSites: h.eventSites, NoVisitorSignIn: noVisitorSignIn}
}
