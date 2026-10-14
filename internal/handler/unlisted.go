package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/db"
)

// Unlisted sites and search engines (INTENT 2026-10-14).
//
// An unlisted site is left off its owner's showcase page and kept out of
// search engines unless it has an address of its own: a proven custom
// domain, a claimed <name>.<SITE_DOMAIN>, or an address-family address.
// Connecting one is a sign the owner wants the site found, so such a site
// stays indexable on every address. Anyone with the link still opens either
// kind. The database is the record (sites.visibility and the site's
// addresses); an `unlisted` marker file in the site folder, present only
// for an unlisted site with no own address, is what every server reading
// the site's files tests. Go serves the site hosts, person paths and
// claimed names itself; self-hosted Caddy hands a marked site's requests
// to the app through the passcode gate's path (/internal/passcode/...),
// which serves the file when there is no passcode. nginx's custom-domain
// and family vhosts need nothing: a site they serve has an own address, so
// it never has the marker. Every marked address answers the same:
//
//   - every response carries `X-Robots-Tag: noindex, nofollow`;
//   - /robots.txt at the host root answers "Disallow: /", unless the site
//     ships its own robots.txt, which is then served as is (the owner's file
//     wins; the header still applies to every response, robots.txt included).
//
// Public sites are unchanged. The marker is rewritten with a visibility
// change and with every address change (syncDomainRedirect), and checked at
// boot, so the next request sees it; nothing here is cached.

const unlistedRobotsTag = "noindex, nofollow"

const unlistedRobotsTxt = "User-agent: *\nDisallow: /\n"

// unlistedSite reports whether a site is unlisted, from its disk marker.
func (h *SiteHandler) unlistedSite(userID, siteName string) bool {
	return h.disk.IsUnlisted(userID, siteName)
}

// markUnlisted adds the noindex header when the site is unlisted.
func (h *SiteHandler) markUnlisted(w http.ResponseWriter, userID, siteName string) bool {
	if !h.unlistedSite(userID, siteName) {
		return false
	}
	w.Header().Set("X-Robots-Tag", unlistedRobotsTag)
	return true
}

// serveUnlistedRobots answers /robots.txt for an unlisted site served at a
// host root when the site has no robots.txt of its own; it reports whether
// it answered.
func (h *SiteHandler) serveUnlistedRobots(w http.ResponseWriter, r *http.Request, userID, siteName string) bool {
	if r.URL.Path != "/robots.txt" {
		return false
	}
	st, err := os.Stat(filepath.Join(h.disk.SiteDir(userID, siteName), "current", "robots.txt"))
	if err == nil && !st.IsDir() {
		return false // the site's own file is served
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(unlistedRobotsTxt))
	}
	return true
}

// wantUnlistedMarker: the site is unlisted (anything but "public", as
// toSiteResponse reads it) and has no address of its own.
func (h *SiteHandler) wantUnlistedMarker(ctx context.Context, site db.Site) (bool, error) {
	if site.Visibility == "public" || len(h.siteFamilyAddrs(site.UserID, site.Name)) > 0 {
		return false, nil
	}
	_, own, err := h.siteOwnDomain(ctx, site.ID)
	return !own, err
}

// syncUnlistedMarker makes the disk marker match wantUnlistedMarker.
func (h *SiteHandler) syncUnlistedMarker(ctx context.Context, site db.Site) error {
	want, err := h.wantUnlistedMarker(ctx, site)
	if err != nil {
		return err
	}
	return h.disk.SetUnlisted(site.UserID, site.Name, want)
}
