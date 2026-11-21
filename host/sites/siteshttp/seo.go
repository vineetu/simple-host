package siteshttp

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// hostSitemapPaths and hackSitemapPaths are the platform's own public
// marketing pages, one list per brand, followed by eligible hosted sites.
var (
	hostSitemapPaths = []string{
		"/", "/features", "/pricing", "/install.html",
		"/enterprise", "/enterprise/brief", "/enterprise/architecture",
		"/architecture.html", "/terms", "/support", "/privacy.html",
	}
)

// isHackHost reports whether the request arrived on the simple-hack.app
// brand, the same Host-prefix check ChromeDataFor uses for nav rendering.
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

// serveRobotsTxt and serveSitemapXML answer the platform's own robots.txt and
// sitemap.xml for the apex marketing site, host-aware. Pages that must not be
// indexed already carry their own noindex meta tag or X-Robots-Tag header
// (admin.html, the signed-out events flow, offline/suspended/preview tenant
// pages), so this stays a plain allow-all plus a pointer at the sitemap
// rather than duplicating that per-path logic here.
func serveRobotsTxt(w http.ResponseWriter, r *http.Request, hostedEvents bool, policies ...edition.PresentationPolicy) {
	base, _ := sitemapBase(r, hostedEvents, policies...)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n"))
}

// sitemapURLLimit is variable so index mode can be exercised without 50,001 fixtures.
var sitemapURLLimit = 50000

// SitemapTTL is short so a site that gains a passcode, named viewers, goes
// offline or is deleted leaves the sitemap within minutes.
const SitemapTTL = 2 * time.Minute

type sitemapURL struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod,omitempty"`
}

type sitemapCache struct {
	mu      sync.Mutex
	ByBrand map[string]cachedSitemaps
}
type cachedSitemaps struct {
	Expires time.Time
	files   map[string][]byte
}

// serveSitemapXML preserves the pages-only helper used by standalone callers.
func serveSitemapXML(w http.ResponseWriter, r *http.Request, hostedEvents bool, policies ...edition.PresentationPolicy) {
	base, product := sitemapBase(r, hostedEvents, policies...)
	serveSitemapFile(w, r, buildSitemaps(base, sitemapPages(base, product, policies...), nil))
}

func sitemapPages(base string, product bool, policies ...edition.PresentationPolicy) []sitemapURL {
	paths := hostSitemapPaths
	if product && len(policies) > 0 {
		paths = strings.Fields(policies[0].SitemapPaths)
	}
	lastmod := ""
	if built, err := time.Parse(time.RFC3339, buildinfo.BuildTime); err == nil {
		lastmod = built.UTC().Format(time.RFC3339)
	}
	out := make([]sitemapURL, 0, len(paths))
	for _, p := range paths {
		out = append(out, sitemapURL{base + p, lastmod})
	}
	return out
}

func xmlDocument(v any) []byte {
	body, _ := xml.Marshal(v)
	return append([]byte(xml.Header), body...)
}
func urlset(urls []sitemapURL) []byte {
	return xmlDocument(struct {
		XMLName xml.Name     `xml:"urlset"`
		XMLNS   string       `xml:"xmlns,attr"`
		URLs    []sitemapURL `xml:"url"`
	}{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls})
}
func buildSitemaps(base string, pages, sites []sitemapURL) map[string][]byte {
	files := map[string][]byte{"/sitemap-pages.xml": urlset(pages)}
	var children []sitemapURL
	children = append(children, sitemapURL{Loc: base + "/sitemap-pages.xml"})
	// The first site shard exists even when there are no eligible sites.
	for i := 0; i < len(sites) || i == 0; i += sitemapURLLimit {
		end := min(i+sitemapURLLimit, len(sites))
		path := fmt.Sprintf("/sitemap-sites-%d.xml", i/sitemapURLLimit+1)
		files[path] = urlset(sites[i:end])
		children = append(children, sitemapURL{Loc: base + path})
	}
	if len(pages)+len(sites) <= sitemapURLLimit {
		files["/sitemap.xml"] = urlset(append(append([]sitemapURL{}, pages...), sites...))
	} else {
		files["/sitemap.xml"] = xmlDocument(struct {
			XMLName xml.Name     `xml:"sitemapindex"`
			XMLNS   string       `xml:"xmlns,attr"`
			Maps    []sitemapURL `xml:"sitemap"`
		}{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", Maps: children})
	}
	return files
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

// SetSitemapFilter installs the product filter before serving requests.
func (h *SiteHandler) SetSitemapFilter(filter appsites.SitemapFilter) {
	h.sitemapFilter = filter
}

func (h *SiteHandler) ServeSitemap(w http.ResponseWriter, r *http.Request) {
	base, product := sitemapBase(r, h.Ed.Routes().HostedEvents, h.Ed.Presentation())
	h.Sitemaps.mu.Lock()
	cached, ok := h.Sitemaps.ByBrand[base]
	if !ok || time.Now().After(cached.Expires) {
		var urls []sitemapURL
		if h.Pipeline().HostedOwner() != "" {
			sites, err := h.SitesSvc().ListSitemapSites(r.Context())
			if err != nil {
				log.Printf("sitemap: %v", err)
				h.Sitemaps.mu.Unlock()
				http.Error(w, "sitemap unavailable", http.StatusServiceUnavailable)
				return
			}
			for _, s := range sites {
				if h.Disk.IsSuspended(s.UserID, s.Name) || h.Disk.IsOffline(s.UserID, s.Name) || h.Disk.HasPasscodeMarker(s.UserID, s.Name) {
					continue
				}
				if !siteAllowsIndexing(r.Context(), h.Disk, appsites.FileVersion{UserID: s.UserID, SiteName: s.Name}) {
					continue
				}
				family := ""
				if host, ok := h.Hosts.SiteFamilyAddress(s.UserID, s.Name); ok {
					family = "https://" + host + "/"
				}
				address := appsites.LiveURL(s, family, h.Hosts.Addressing().SiteURLFor(s))
				if address == "" {
					continue
				}
				// Simple Hack serves team sites only at a host root: a path
				// fallback (certificate not ready yet) does not serve the site.
				if product {
					if u, err := url.Parse(address); err != nil || (u.Path != "" && u.Path != "/") {
						continue
					}
				}
				lastmod := ""
				if s.LastDeployedAt.Valid {
					lastmod = s.LastDeployedAt.Time.UTC().Format(time.RFC3339)
				}
				urls = append(urls, sitemapURL{address, lastmod})
			}
		}
		cached = cachedSitemaps{time.Now().Add(SitemapTTL), buildSitemaps(base, sitemapPages(base, product, h.Ed.Presentation()), urls)}
		if h.Sitemaps.ByBrand == nil {
			h.Sitemaps.ByBrand = make(map[string]cachedSitemaps)
		}
		h.Sitemaps.ByBrand[base] = cached
	}
	h.Sitemaps.mu.Unlock()
	serveSitemapFile(w, r, cached.files)
}

func siteAllowsIndexing(ctx context.Context, files appsites.SiteFiles, ref appsites.FileVersion) bool {
	f, st, err := files.OpenFile(ctx, ref, "index.html")
	if err != nil {
		return false
	}
	defer f.Close()
	if !st.Mode().IsRegular() {
		return false
	}
	if b, err := files.ReadFile(ctx, ref, "robots.txt"); err == nil && robotsDisallowRoot(string(b)) {
		return false
	}
	z := html.NewTokenizer(io.LimitReader(f, 512*1024))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return true
		case html.StartTagToken, html.SelfClosingTagToken:
			t := z.Token()
			if t.Data != "meta" {
				continue
			}
			name, content := "", ""
			for _, a := range t.Attr {
				if a.Key == "name" {
					name = strings.ToLower(a.Val)
				}
				if a.Key == "content" {
					content = strings.ToLower(a.Val)
				}
			}
			if (name == "robots" || name == "googlebot") && (strings.Contains(content, "noindex") || strings.Contains(content, "none")) {
				return false
			}
		}
	}
}

func robotsDisallowRoot(body string) bool {
	star, rules := false, false
	for _, line := range strings.Split(body, "\n") {
		line, _, _ = strings.Cut(line, "#")
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		if key == "user-agent" {
			if rules {
				star = false
				rules = false
			}
			// A group for every crawler or for Google's applies to us.
			if v := strings.ToLower(value); v == "*" || v == "googlebot" {
				star = true
			}
		} else {
			rules = true
			if star && key == "disallow" && (value == "/" || value == "/*" || value == "*") {
				return true
			}
		}
	}
	return false
}

// markRestrictedSite preserves noindex before an address redirect can return;
// the file-serving gates retain their own headers, including disk-only gates.
func markRestrictedSite(w http.ResponseWriter, site model.Site) {
	if !site.OpenToAnyone() {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
}
