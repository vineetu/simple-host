package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
)

// resolveSiteID resolves the target site's id. When the route carries a {handle} path
// value (the v3 user-scoped routes), it resolves handle->user_id then (user_id,name)->site
// so the lookup is unambiguous even after UNIQUE(name) is dropped; an old handle kept as
// an alias still resolves. The request's host binds the lookup: on a person host
// (<handle>.<SITE_DOMAIN>) only that person's sites exist, and on a site's own domain
// only that site does. Elsewhere (apex, content host) a bare name falls back to the
// legacy global lookup. Returns sql.ErrNoRows if not found (caller maps to 404).
func (h *SiteHandler) resolveSiteID(r *http.Request, siteName string) (string, error) {
	id, _, err := h.resolveSiteIDBare(r, siteName)
	return id, err
}

// resolveWriteSiteID is resolveSiteID for the page-facing storage routes. A
// bare name on the API or shared host
// otherwise means the oldest same-named site; when the caller's key owns a
// site of that name, it means theirs. Everything else is unchanged, and the
// storage gate still decides whether the caller may write.
func (h *SiteHandler) resolveWriteSiteID(r *http.Request, siteName string) (string, error) {
	return h.resolveWriteSiteIDBare(r, siteName)
}

// resolveWriteSiteIDBare also serves speculative access-gate lookups, which
// must not become evidence that the operation selected a site.
func (h *SiteHandler) resolveWriteSiteIDBare(r *http.Request, siteName string) (string, error) {
	id, bare, err := h.resolveSiteIDBare(r, siteName)
	key := r.Header.Get("X-API-Key")
	if !bare || key == "" {
		return id, err
	}
	u, ok, kerr := h.access.resolveWriterKey(r.Context(), key)
	if kerr != nil || !ok || u.ID == "" {
		return id, err
	}
	if s, serr := h.sitesSvc().GetSiteByUser(r.Context(), u.ID, siteName); serr == nil {
		return s.ID, nil
	}
	return id, err
}

// resolveSiteIDBare resolves like resolveSiteID and reports whether the name
// was looked up bare (no handle, no site/person host, no own domain).
func (h *SiteHandler) resolveSiteIDBare(r *http.Request, siteName string) (string, bool, error) {
	id, err := h.resolveSiteIDScoped(r, siteName)
	if !errors.Is(err, errBareSiteName) {
		return id, false, err
	}
	if h.ed.Storage().EventSites {
		// On the hackathon platform every event has teams with the same
		// names: a bare name means the caller's own site, or
		// nothing, never whichever event's site is oldest.
		if key := r.Header.Get("X-API-Key"); key != "" {
			if u, ok, kerr := h.access.resolveWriterKey(r.Context(), key); kerr == nil && ok && u.ID != "" {
				if s, serr := h.sitesSvc().GetSiteByUser(r.Context(), u.ID, siteName); serr == nil {
					h.paperSite(r, s.UserID, s.Name, &u)
					return s.ID, true, nil
				}
			}
		}
		return "", true, sql.ErrNoRows
	}
	id, err = h.sitesSvc().GetSiteIDByName(r.Context(), siteName)
	return id, true, err
}

func (h *SiteHandler) resolveSiteIDScoped(r *http.Request, siteName string) (string, error) {
	host := requestHostName(r)
	handle := strings.TrimSpace(r.PathValue("handle"))
	// A site host (<site>.<handle>.<SITE_DOMAIN>) is one site's own origin:
	// only that site resolves there, under its name (what auth.js derives
	// from the first host label) or its owner's handle route. Any other name
	// — another site of the same person included — does not exist there.
	if h.isSiteHostName(r.Context(), host) {
		site, owner, ok, err := h.siteHostSite(r.Context(), host)
		if err != nil {
			return "", err
		}
		if !ok || site.Name != siteName {
			return "", sql.ErrNoRows
		}
		if handle != "" {
			u, err := h.storeAccounts().GetUserByHandleOrAlias(r.Context(), handle)
			if err != nil {
				return "", err
			}
			if u.ID != owner.ID {
				return "", sql.ErrNoRows
			}
		}
		h.paperSite(r, site.UserID, site.Name, auth.GetUser(r.Context()))
		return site.ID, nil
	}
	// On a family address (<label>.<family>) the host names the site too:
	// its full name or the label (what auth.js derives from the host) is
	// that site, under its owner's handle route as well; nothing else
	// resolves there.
	// (A custom domain bound here under the family is that domain's.)
	if h.isFamilyHostName(host) && !h.isBoundDomain(r.Context(), host) {
		m, ok, err := h.familySiteForHost(r.Context(), host)
		if err != nil {
			return "", err
		}
		if !ok || (siteName != m.Site.Name && siteName != m.Label) {
			return "", sql.ErrNoRows
		}
		if handle != "" {
			u, err := h.storeAccounts().GetUserByHandleOrAlias(r.Context(), handle)
			if err != nil {
				return "", err
			}
			if u.ID != m.Site.UserID {
				return "", sql.ErrNoRows
			}
		}
		return m.Site.ID, nil
	}
	owner, onPerson := h.personHostOwner(r.Context(), host)
	if onPerson && !h.ed.Storage().EventSites {
		home, has, err := h.sitesSvc().HomeSite(r.Context(), owner.ID)
		if err != nil {
			return "", err
		}
		if has {
			if home.Name != siteName && owner.Handle.String != siteName {
				return "", sql.ErrNoRows
			}
			if handle != "" {
				u, err := h.storeAccounts().GetUserByHandleOrAlias(r.Context(), handle)
				if err != nil || u.ID != owner.ID {
					return "", sql.ErrNoRows
				}
			}
			return home.ID, nil
		}
	}
	if handle != "" {
		u, err := h.storeAccounts().GetUserByHandleOrAlias(r.Context(), handle)
		if err != nil {
			return "", err
		}
		if onPerson && u.ID != owner.ID {
			return "", sql.ErrNoRows
		}
		s, err := h.sitesSvc().GetSiteByUser(r.Context(), u.ID, siteName)
		if err != nil {
			return "", err
		}
		return s.ID, nil
	}
	if onPerson {
		if h.ed.Storage().EventSites {
			ev, err := h.groupByAccount(r.Context(), owner.ID)
			if err != nil || ev.Slug != siteName || !ev.CustomWebsite || ev.TakenDown {
				return "", sql.ErrNoRows
			}
			s, err := h.sitesSvc().GetSiteByUser(r.Context(), owner.ID, ev.ID)
			if err != nil {
				return "", err
			}
			return s.ID, nil
		}
		s, err := h.sitesSvc().GetSiteByUser(r.Context(), owner.ID, siteName)
		if err != nil {
			return "", err
		}
		return s.ID, nil
	}
	// On a site's own domain (custom or a claimed <name>.<SITE_DOMAIN>) the
	// host names the site: /v1/sites/<its name> there is that site (as is the
	// claimed name itself, which is what auth.js derives from the host), and
	// no other name resolves there.
	if host != "" && !strings.EqualFold(host, h.contentHost) && !h.access.isVisitorApexHost(host) {
		if info, err := h.sitesSvc().GetSiteByCustomDomain(r.Context(), host); err == nil {
			label, _, isPlatform := h.userHostLabel(host)
			if info.Name == siteName || (isPlatform && label == siteName) {
				return info.SiteID, nil
			}
			return "", sql.ErrNoRows
		}
	}
	return "", errBareSiteName
}

// originAllowedForSiteID reports whether the owner has whitelisted this exact
// origin (scheme://host) for the given site_id's API.
func (h *SiteHandler) originAllowedForSiteID(ctx context.Context, siteID, origin string) bool {
	origins, err := h.sitesSvc().GetAllowedOriginsByID(ctx, siteID)
	if err != nil || len(origins) == 0 {
		return false
	}
	o := strings.ToLower(strings.TrimRight(origin, "/"))
	for _, a := range origins {
		a = strings.ToLower(strings.TrimRight(a, "/"))
		if a == o || h.sameOriginAcrossBases(a, o) {
			return true
		}
	}
	return false
}

// sameOriginAcrossBases: two origins that differ only in the base domain of
// a person, site or claimed-name host (sitebase.go), same scheme and port.
// An owner who allowed https://x.simple-host.app keeps it working at
// https://x.simple-host.site without their data being rewritten.
func (h *SiteHandler) sameOriginAcrossBases(a, b string) bool {
	if !h.baseSplit() {
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
	t := h.twinHost(ua.Hostname())
	return t != "" && strings.EqualFold(t, ub.Hostname())
}

// originIsBoundDomainID reports whether host is this site's own custom_domain
// (same-origin state on a connected domain). Keyed by site_id so a domain bound
// to one same-named site cannot authorize writes to another.
func (h *SiteHandler) originIsBoundDomainID(ctx context.Context, siteID, host string) bool {
	info, ok, err := h.sitesSvc().GetSiteDomainInfo(ctx, siteID)
	if err != nil || !ok {
		return false
	}
	// Also the earlier address it still serves at while a new one is pending.
	// A claimed name answers under either base while the base moves.
	return h.sameUserHost(info.Domain, host) || (info.PreviousDomain != "" && h.sameUserHost(info.PreviousDomain, host))
}

// originIsPersonHostID reports whether host is the person address of the
// site's owner: a page served there may use the site's API from anywhere
// (the apex, the content host, or its own host), like a bound domain.
func (h *SiteHandler) originIsPersonHostID(ctx context.Context, siteID, host string) bool {
	if !h.personHostsOn() {
		return false
	}
	if h.isHomeSiteHost(ctx, siteID, host) {
		return true
	}
	owner, ok := h.personHostOwner(ctx, host)
	if ok {
		_, has, err := h.sitesSvc().HomeSite(ctx, owner.ID)
		if err != nil || has {
			return false
		}
	}
	handle, _, name, err := h.sitesSvc().GetSiteOwner(ctx, siteID)
	if err != nil || !h.personAddressFor(handle, name) || (!h.ed.Storage().EventSites && h.siteHostCanonicalOn(handle, name, h.hostBase(ctx, host))) {
		return false
	}
	// Its person host under any base that serves it.
	return h.sameUserHost(h.personHostFor(handle), host)
}

// originIsSiteHostID reports whether host is the site's own site host
// (<site>.<handle>.<SITE_DOMAIN>) while that host serves it.
func (h *SiteHandler) originIsSiteHostID(ctx context.Context, siteID, host string) bool {
	if !h.siteHostsOn() || !h.isSiteHostName(ctx, host) {
		return false
	}
	handle, _, name, err := h.sitesSvc().GetSiteOwner(ctx, siteID)
	if err != nil || !h.siteHostLiveOn(handle, name, h.hostBase(ctx, host)) {
		return false
	}
	return h.sameUserHost(h.siteHostFor(handle, name), host)
}

// originIsFamilyHostID reports whether the origin is one of the site's
// family addresses (https, default port) while the family serves it.
func (h *SiteHandler) originIsFamilyHostID(ctx context.Context, siteID string, origin *url.URL) bool {
	if origin.Scheme != "https" || origin.Port() != "" || !h.isFamilyHostName(origin.Hostname()) {
		return false
	}
	m, ok, err := h.familySiteForHost(ctx, origin.Hostname())
	return err == nil && ok && m.Site.ID == siteID
}

// authorizeStateOrigin checks Origin/Referer and, on a match, sets the CORS
// headers that allow the calling site to read the response. Returns true if
// the request is allowed. The gate is keyed to the same site_id that the data
// handlers resolve (via resolveSiteID), so two same-named sites cannot widen
// each other's allowed_origins or bound custom_domain.
func (h *SiteHandler) authorizeStateOrigin(w http.ResponseWriter, r *http.Request, siteName string) bool {
	// Resolve once; if we can't attribute the request to a real site, deny.
	// Key-aware, so the origin is checked against the same site a keyed
	// write or read targets (the caller's own same-named site on a bare name).
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		return false
	}

	origin := requestOrigin(r)
	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	// Accept the shared v3 content host (sites.<SITE_DOMAIN> — all path-model
	// pages share this Origin), this site's owner's person address, this
	// site's own bound custom domain, OR any origin the owner has explicitly
	// allowed for THIS site_id. A browser cannot forge Origin, so this is the
	// same attribution-grade gate, just widened to owner-approved origins, the
	// co-tenant content host, and the site's own hosts. (The retired
	// <name>.<SITE_DOMAIN> host only redirects, so it serves no page to trust.)
	//
	// The content host is every path-model site's origin at once, so it is
	// trusted only for requests to a shared host (the content host itself or
	// the apex), where no visitor session or passcode unlock is ever valid. A
	// site's own host, person host, family host or domain answers only its own
	// addresses: a co-tenant page never gets a credentialed read of another
	// site's visitor (SIM-348).
	if !(strings.EqualFold(parsed.Host, h.contentHost) && h.access.sharedHost(requestHostName(r))) &&
		!h.originIsSiteHostID(r.Context(), siteID, parsed.Host) &&
		!h.originIsFamilyHostID(r.Context(), siteID, parsed) &&
		!h.originIsPersonHostID(r.Context(), siteID, parsed.Host) &&
		!h.originIsBoundDomainID(r.Context(), siteID, parsed.Host) &&
		// An owner cannot list the content host back in either.
		!(h.originAllowedForSiteID(r.Context(), siteID, origin) && !strings.EqualFold(parsed.Host, h.contentHost)) {
		return false
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	// Allow credentials so the page can send its cookies (the visitor
	// session, and a passcode site's unlock) with the request (ACAO is the
	// specific origin above, never "*", as credentials require).
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	// Expose ETag so the page's JS can read the state version for optimistic
	// concurrency (ETag is not a CORS-safelisted response header).
	w.Header().Set("Access-Control-Expose-Headers", "ETag")
	w.Header().Set("Vary", "Origin")
	return true
}

// writeOriginRefused is the 403 for a request from a page that is not one of
// the site's own addresses (or that names no page and holds no key of the
// site's owner).
func writeOriginRefused(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: "this request comes from a page that is not one of this site's own addresses, so it cannot use the site's saved data. " +
			"A page saves from the site itself; an agent or script sends the site owner's X-API-Key and no Origin header",
		Code: "origin_not_allowed",
	})
}
