package siteshttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites/sitemap"
)

// serveRobotsTxt answers the apex site's robots.txt, host-aware.
// Pages that must not be indexed carry their own noindex meta tag or X-Robots-Tag, so this stays allow-all
// plus a sitemap pointer.
func serveRobotsTxt(w http.ResponseWriter, r *http.Request, hostedEvents bool, policies ...edition.PresentationPolicy) {
	base, _ := sitemap.Base(r, hostedEvents, policies...)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n"))
}

// sitemapCache is the SiteHandler's per-brand sitemap cache (owned by sitemap.SitemapFiles).
type sitemapCache = sitemap.SitemapCache
