package accesshttp

import (
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/papermetrics"
	"github.com/vsriram/simple-host/host/sites"
)

// resolveSiteID is the site a page-facing request names (sites.Hosts binds
// the name to the request's host or {handle}). A bare name on a shared host
// means the oldest site of that name, or on the hosted-events platform the
// key holder's own. sql.ErrNoRows if none.
func (h *AccessHandler) resolveSiteID(r *http.Request, siteName string) (string, error) {
	id, _, err := h.resolveSiteIDBare(r, siteName)
	return id, err
}

// resolveSiteIDBare also reports whether the name was looked up bare.
func (h *AccessHandler) resolveSiteIDBare(r *http.Request, siteName string) (string, bool, error) {
	id, err := h.ResolveSiteIDScoped(r, siteName)
	if !errors.Is(err, sites.ErrBareName) {
		return id, false, err
	}
	var writer *model.User
	if h.Ed.Storage().EventSites {
		writer = h.writerOf(r)
	}
	site, err := h.Hosts.BareSite(r.Context(), siteName, writer)
	if err == nil && site.UserID != "" {
		papermetrics.RecordSite(h.Ed, r, site.UserID, site.Name, writer)
	}
	return site.ID, true, err
}

// resolveWriteSiteID is resolveSiteID for the page-facing storage routes: a
// bare name means the key holder's own site of that name when there is one.
// The storage gate still decides whether the caller may write. It also serves
// speculative access-gate lookups, so it must not record that the operation
// selected a site.
func (h *AccessHandler) resolveWriteSiteID(r *http.Request, siteName string) (string, error) {
	id, bare, err := h.resolveSiteIDBare(r, siteName)
	if !bare {
		return id, err
	}
	if s, werr := h.Hosts.WriterSite(r.Context(), siteName, h.writerOf(r)); werr == nil {
		return s.ID, nil
	}
	return id, err
}

// ResolveSiteIDScoped is the site the request's host or {handle} names;
// sites.ErrBareName when neither does.
func (h *AccessHandler) ResolveSiteIDScoped(r *http.Request, siteName string) (string, error) {
	site, onSiteHost, err := h.Hosts.ResolveSite(r.Context(), httpx.HostName(r), strings.TrimSpace(r.PathValue("handle")), siteName)
	if err != nil {
		return "", err
	}
	if onSiteHost {
		papermetrics.RecordSite(h.Ed, r, site.UserID, site.Name, auth.GetUser(r.Context()))
	}
	return site.ID, nil
}

// writerOf is the account of the request's X-API-Key, nil when none.
func (h *AccessHandler) writerOf(r *http.Request) *model.User {
	key := r.Header.Get("X-API-Key")
	if key == "" {
		return nil
	}
	u, ok, err := h.resolveWriterKey(r.Context(), key)
	if err != nil || !ok || u.ID == "" {
		return nil
	}
	return &u
}

// authorizeStateOrigin checks Origin/Referer against the site's own
// addresses (sites.Hosts.OriginAllowed) and, on a match, sets the CORS
// headers that let the page read the response with its cookies. The site is
// the same one the data routes resolve, so two same-named sites cannot widen
// each other's origins.
func (h *AccessHandler) authorizeStateOrigin(w http.ResponseWriter, r *http.Request, siteName string) bool {
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		return false
	}
	origin := httpx.Origin(r)
	if origin == "" || !h.Hosts.OriginAllowed(r.Context(), siteID, origin, httpx.HostName(r)) {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", origin)
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	// The page's JS reads the state version for optimistic concurrency.
	w.Header().Set("Access-Control-Expose-Headers", "ETag")
	w.Header().Set("Vary", "Origin")
	return true
}
