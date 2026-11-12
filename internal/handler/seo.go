package handler

import (
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/vsriram/simple-host/internal/buildinfo"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/webassets"
)

// hostSitemapPaths and hackSitemapPaths are the platform's own public
// marketing pages, one list per brand, followed by docs and eligible hosted sites.
var (
	hostSitemapPaths = []string{
		"/", "/features", "/pricing", "/install.html", "/hackathons",
		"/enterprise", "/enterprise/brief", "/enterprise/architecture",
		"/architecture.html", "/terms", "/support", "/privacy.html",
	}
	hackSitemapPaths = []string{
		"/", "/get-started", "/companies", "/directory", "/watch", "/terms", "/privacy.html",
	}
)

// isHackHost reports whether the request arrived on the simple-hack.app
// brand, the same Host-prefix check chromeDataFor uses for nav rendering.
func isHackHost(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.HasPrefix(strings.ToLower(host), "simple-hack.")
}

// sitemapBase is the origin the sitemap names, and whether it is a Simple
// Hack one: the hosted brands by name, and any other server (a self-hosted
// Simple Host or Simple Hack) by its own SITE_DOMAIN, never by the request's Host.
func sitemapBase(r *http.Request, hostedEvents bool) (string, bool) {
	if isHackHost(r) {
		return "https://simple-hack.app", true
	}
	if instanceSiteDomain == "" {
		return "https://" + canonicalSiteDomain, hostedEvents
	}
	return "https://" + instanceSiteDomain, hostedEvents
}

// serveRobotsTxt and serveSitemapXML answer the platform's own robots.txt and
// sitemap.xml for the apex marketing site, host-aware. Pages that must not be
// indexed already carry their own noindex meta tag or X-Robots-Tag header
// (admin.html, the signed-out events flow, offline/suspended/preview tenant
// pages), so this stays a plain allow-all plus a pointer at the sitemap
// rather than duplicating that per-path logic here.
func serveRobotsTxt(w http.ResponseWriter, r *http.Request, hostedEvents bool) {
	base, _ := sitemapBase(r, hostedEvents)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n"))
}

// sitemapURLLimit is variable so index mode can be exercised without 50,001 fixtures.
var sitemapURLLimit = 50000

// sitemapTTL is short so a site that gains a passcode, named viewers, goes
// offline or is deleted leaves the sitemap within minutes.
const sitemapTTL = 2 * time.Minute

type sitemapURL struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod,omitempty"`
}

type sitemapCache struct {
	mu      sync.Mutex
	byBrand map[string]cachedSitemaps
}
type cachedSitemaps struct {
	expires time.Time
	files   map[string][]byte
}

// serveSitemapXML preserves the pages-only helper used by standalone callers.
func serveSitemapXML(w http.ResponseWriter, r *http.Request, hostedEvents bool) {
	base, hack := sitemapBase(r, hostedEvents)
	serveSitemapFile(w, r, buildSitemaps(base, sitemapPages(base, hack), nil))
}

func sitemapPages(base string, hack bool) []sitemapURL {
	paths := hostSitemapPaths
	if hack {
		paths = hackSitemapPaths
	}
	paths = append(append([]string{}, paths...), webassets.DocsPaths(hack)...)
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

func (h *SiteHandler) serveSitemap(w http.ResponseWriter, r *http.Request) {
	base, hack := sitemapBase(r, h.ed.Routes().HostedEvents)
	h.sitemaps.mu.Lock()
	cached, ok := h.sitemaps.byBrand[base]
	if !ok || time.Now().After(cached.expires) {
		var urls []sitemapURL
		if h.pipeline().hostedOwner() != "" {
			sites, err := h.sitesSvc().ListSitemapSites(r.Context(), hack)
			if err != nil {
				log.Printf("sitemap: %v", err)
				h.sitemaps.mu.Unlock()
				http.Error(w, "sitemap unavailable", http.StatusServiceUnavailable)
				return
			}
			for _, s := range sites {
				if h.disk.IsSuspended(s.UserID, s.Name) || h.disk.IsOffline(s.UserID, s.Name) || h.disk.HasPasscodeMarker(s.UserID, s.Name) {
					continue
				}
				if !siteAllowsIndexing(filepath.Join(h.disk.SiteDir(s.UserID, s.Name), "current")) {
					continue
				}
				address := h.siteURLFor(s)
				// Match serving's own-domain > canonical-family > platform priority;
				// never enumerate platform addresses that redirect to an own address.
				if s.CustomDomain.String != "" && s.DomainVerifiedAt.Valid {
					address = "https://" + s.CustomDomain.String + "/"
				} else if s.CustomDomain.String != "" && s.PreviousDomain != "" {
					address = "https://" + s.PreviousDomain + "/"
				} else if host, ok := h.siteFamilyAddress(s.UserID, s.Name); ok {
					address = "https://" + host + "/"
				}
				if address == "" {
					continue
				}
				// Simple Hack serves team sites only at a host root: a path
				// fallback (certificate not ready yet) does not serve the site.
				if hack {
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
		cached = cachedSitemaps{time.Now().Add(sitemapTTL), buildSitemaps(base, sitemapPages(base, hack), urls)}
		if h.sitemaps.byBrand == nil {
			h.sitemaps.byBrand = make(map[string]cachedSitemaps)
		}
		h.sitemaps.byBrand[base] = cached
	}
	h.sitemaps.mu.Unlock()
	serveSitemapFile(w, r, cached.files)
}

func siteAllowsIndexing(dir string) bool {
	f, err := os.Open(filepath.Join(dir, "index.html"))
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	if b, err := os.ReadFile(filepath.Join(dir, "robots.txt")); err == nil && robotsDisallowRoot(string(b)) {
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
	if site.Passcode || site.NamedViewers || site.Offline || site.Suspended() {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
}
