package sitesroute

import (
	"strconv"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/web"
)

// SignPreviewToken binds a token to one site, one version and an expiry.
func (h *Handler) SignPreviewToken(siteID string, version int, expires time.Time) string {
	payload := siteID + "." + strconv.Itoa(version) + "." + strconv.FormatInt(expires.Unix(), 10)
	return httpx.SealLink(h.ExportKey, web.PreviewLinkDomain, []byte(payload))
}

// CheckPreviewToken returns the site and version a token was minted for, if
// its authentication holds and it has not expired.
func (h *Handler) CheckPreviewToken(token string, now time.Time) (siteID string, version int, err error) {
	return web.OpenPreviewToken(h.ExportKey, h.Limits.PreviewLinkTTL, token, now)
}

// previewBase is where a site's previews are served: "https://<host>" plus
// the path prefix up to and including "/__preview/". ok=false when the site
// has no address of its own to preview on (site and person hosts off, or a
// site pinned to the content host).
func (h *Handler) previewBase(site model.Site) (string, bool) {
	handle := site.OwnerHandle
	switch {
	case handle == "":
		return "", false
	case h.Hosts.Addressing().SiteHostLiveOn(handle, site.Name, h.Hosts.Addressing().HandoutBase()):
		return "https://" + h.Hosts.Addressing().SiteHostOn(handle, site.Name, h.Hosts.Addressing().HandoutBase()) + "/" + web.PreviewPathSegment + "/", true
	case h.Ed.Storage().EventSites:
		// Person-path addresses are never served on the hackathon platform:
		// a team's work lives on its own origin only.
		return "", false
	case h.Hosts.Addressing().PersonAddressFor(handle, site.Name):
		return "https://" + h.Hosts.Addressing().PersonHostOn(handle, h.Hosts.Addressing().HandoutBase()) + "/" + site.Name + "/" + web.PreviewPathSegment + "/", true
	}
	return "", false
}

// SharedOrigin reports whether every site is served on the content host's
// one origin (no per-site or per-person addresses: a small box).
func (h *Handler) SharedOrigin() bool { return h.Hosts.Addressing().SharedOrigin() }

// noPreviewWhy says why a site has no preview link: previews are served on the site's own address, which a
// server that gives sites none (SITE_HOSTS and PERSON_HOSTS off, a small box) does not have.
func (h *Handler) NoPreviewWhy() string {
	if h.SharedOrigin() {
		return "preview links need a per-site address, and this server serves every site on one shared address, so it makes none. " +
			"To see a stored version, make it live (PUT /v1/sites/<site>/active-version) and switch back if it is not right"
	}
	return "this site has no address of its own yet to preview on (its address is still being set up); try again later"
}

func (h *Handler) PreviewLink(site model.Site, n int) (string, time.Time, bool) {
	base, ok := h.previewBase(site)
	if !ok {
		return "", time.Time{}, false
	}
	expires := time.Now().Add(h.Limits.PreviewLinkTTL).Truncate(time.Second)
	return base + strconv.Itoa(n) + "/" + h.SignPreviewToken(site.ID, n, expires) + "/", expires, true
}
