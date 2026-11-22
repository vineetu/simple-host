package sitemap

import (
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
)

var (
	hostSitemapPaths = []string{
		"/", "/features", "/pricing", "/install.html",
		"/enterprise", "/enterprise/brief", "/enterprise/architecture",
		"/architecture.html", "/terms", "/support", "/privacy.html",
	}
)

// Base is the origin the sitemap and robots.txt name, and whether it is a
// product edition's hosted page (its own sitemap paths).
func Base(r *http.Request, hostedEvents bool, policies ...edition.PresentationPolicy) (string, bool) {
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

func sitemapPages(base string, product bool, policies ...edition.PresentationPolicy) []SitemapURL {
	paths := hostSitemapPaths
	if product && len(policies) > 0 {
		paths = strings.Fields(policies[0].SitemapPaths)
	}
	lastmod := ""
	if built, err := time.Parse(time.RFC3339, buildinfo.BuildTime); err == nil {
		lastmod = built.UTC().Format(time.RFC3339)
	}
	out := make([]SitemapURL, 0, len(paths))
	for _, p := range paths {
		out = append(out, SitemapURL{Loc: base + p, Lastmod: lastmod})
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

// Serve answers /sitemap.xml, /sitemap-pages.xml and /sitemap-sites-N.xml
// from the instance's sitemap files, built at most once per SitemapTTL into
// cache.
func Serve(h *sitesroute.Handler, cache *SitemapCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		base, product := Base(r, h.Ed.Routes().HostedEvents, h.Ed.Presentation())
		pages := sitemapPages(base, product, h.Ed.Presentation())
		files, err := Service{Service: h.SitesSvc()}.SitemapFiles(r.Context(), cache, base, pages, h.Pages().HostedOwner() != "", product)
		if err != nil {
			log.Printf("sitemap: %v", err)
			http.Error(w, "sitemap unavailable", http.StatusServiceUnavailable)
			return
		}
		serveSitemapFile(w, r, files)
	}
}
