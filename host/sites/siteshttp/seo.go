package siteshttp

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

var (
	hostSitemapPaths = []string{
		"/", "/features", "/pricing", "/install.html",
		"/enterprise", "/enterprise/brief", "/enterprise/architecture",
		"/architecture.html", "/terms", "/support", "/privacy.html",
	}
)

func sitemapBase(r *http.Request, hostedEvents bool, policies ...edition.PresentationPolicy) (string, bool) {
	if len(policies) > 0 && policies[0].CanonicalBaseURL != "" {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if policies[0].HostedHostPrefix != "" && strings.HasPrefix(strings.ToLower(host), policies[0].HostedHostPrefix) {
			return policies[0].CanonicalBaseURL, true
		}
	}
	if web.InstanceSiteDomain == "" {
		return "https://" + web.CanonicalSiteDomain, hostedEvents
	}
	return "https://" + web.InstanceSiteDomain, hostedEvents
}

// serveRobotsTxt answers the apex site's robots.txt, host-aware.
// Pages that must not be indexed carry their own noindex meta tag or X-Robots-Tag, so this stays allow-all
// plus a sitemap pointer.
func serveRobotsTxt(w http.ResponseWriter, r *http.Request, hostedEvents bool, policies ...edition.PresentationPolicy) {
	base, _ := sitemapBase(r, hostedEvents, policies...)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n"))
}

// sitemapCache is the SiteHandler's per-brand sitemap cache (owned by sites.SitemapFiles).
type sitemapCache = appsites.SitemapCache

func sitemapPages(base string, product bool, policies ...edition.PresentationPolicy) []appsites.SitemapURL {
	paths := hostSitemapPaths
	if product && len(policies) > 0 {
		paths = strings.Fields(policies[0].SitemapPaths)
	}
	lastmod := ""
	if built, err := time.Parse(time.RFC3339, buildinfo.BuildTime); err == nil {
		lastmod = built.UTC().Format(time.RFC3339)
	}
	out := make([]appsites.SitemapURL, 0, len(paths))
	for _, p := range paths {
		out = append(out, appsites.SitemapURL{Loc: base + p, Lastmod: lastmod})
	}
	return out
}

func serveSitemapFile(w http.ResponseWriter, r *http.Request, files map[string][]byte) {
	body, ok := files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *SiteHandler) SetSitemapFilter(filter appsites.SitemapFilter) {
	h.sitemapFilter = filter
}

func (h *SiteHandler) ServeSitemap(w http.ResponseWriter, r *http.Request) {
	base, product := sitemapBase(r, h.Ed.Routes().HostedEvents, h.Ed.Presentation())
	pages := sitemapPages(base, product, h.Ed.Presentation())
	files, err := h.SitesSvc().SitemapFiles(r.Context(), &h.Sitemaps, base, pages, h.Pages().HostedOwner() != "", product)
	if err != nil {
		log.Printf("sitemap: %v", err)
		http.Error(w, "sitemap unavailable", http.StatusServiceUnavailable)
		return
	}
	serveSitemapFile(w, r, files)
}

// markRestrictedSite sets noindex before an address redirect can return; the
// file-serving gates set their own headers, including disk-only gates.
func markRestrictedSite(w http.ResponseWriter, site model.Site) {
	if !site.OpenToAnyone() {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
}
