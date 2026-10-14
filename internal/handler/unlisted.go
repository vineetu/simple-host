package handler

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/db"
)

// Unlisted sites and search engines (INTENT 2026-10-14).
//
// An unlisted site is left off its owner's showcase page and kept out of
// search engines; anyone with the link still opens it. The database is the
// record (sites.visibility); an `unlisted` marker file in the site folder is
// what every server reading the site's files tests. Go serves the site
// hosts, person paths and claimed names itself; nginx (custom domains,
// address families) and Caddy hand an unlisted site's requests to the app
// through the passcode gate's path (/internal/passcode/...), which serves
// the file when there is no passcode. So every address answers the same:
//
//   - every response carries `X-Robots-Tag: noindex, nofollow`;
//   - /robots.txt at the host root answers "Disallow: /", unless the site
//     ships its own robots.txt, which is then served as is (the owner's file
//     wins; the header still applies to every response, robots.txt included).
//
// Public sites are unchanged. The marker is written with the visibility
// change, so the next request sees it; nothing here is cached.

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

// syncUnlistedMarker makes the disk marker match the site's visibility.
// Anything but "public" counts as unlisted, as toSiteResponse reads it.
func (h *SiteHandler) syncUnlistedMarker(site db.Site) error {
	return h.disk.SetUnlisted(site.UserID, site.Name, site.Visibility != "public")
}
