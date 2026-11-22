package sites

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// SitemapFilter applies a product's exclusions in the given order. Nil keeps
// every site that meets the platform's eligibility checks.
type SitemapFilter func(ctx context.Context, q sqlkit.Querier, sites []repo.Site) ([]repo.Site, error)

func (s *Service) ListSitemapSites(ctx context.Context) ([]model.Site, error) {
	sites, err := repo.ListSitemapSites(ctx, s.database)
	if err != nil {
		return nil, err
	}
	if s.deps.Sitemap != nil {
		// Repository rows are closed before the product's second read. A
		// concurrent take-down has the same visibility window as the cache.
		return s.deps.Sitemap(ctx, s.database, sites)
	}
	return sites, nil
}

// SitemapURL is one <url> (or <sitemap>) entry.
type SitemapURL struct {
	Loc     string `xml:"loc"`
	Lastmod string `xml:"lastmod,omitempty"`
}

// SitemapTTL is short so a site that gains a passcode, named viewers, goes
// offline or is deleted leaves the sitemap within minutes.
const SitemapTTL = 2 * time.Minute

// sitemapURLLimit is variable so index mode can be exercised without 50,001 fixtures.
var sitemapURLLimit = 50000

// SitemapCache holds each brand's built sitemap files (keyed by base URL)
// until Expires. The zero value is ready to use.
type SitemapCache struct {
	mu      sync.Mutex
	ByBrand map[string]CachedSitemaps
}

// CachedSitemaps is one brand's sitemap files by request path.
type CachedSitemaps struct {
	Expires time.Time
	files   map[string][]byte
}

// SitemapFiles returns the sitemap files for base (request path to XML),
// built at most once per SitemapTTL and held in cache. pages are the host's
// own pages. withSites lists every site a search engine may index: live, not
// suspended, offline or behind a passcode, with an index.html that does not
// refuse indexing, at its live address. rootOnly keeps only sites served at a
// host root (Simple Hack serves team sites only there: a path fallback while a
// certificate is not ready does not serve the site). A failed listing is not
// cached.
func (s *Service) SitemapFiles(ctx context.Context, cache *SitemapCache, base string, pages []SitemapURL, withSites, rootOnly bool) (map[string][]byte, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cached, ok := cache.ByBrand[base]; ok && !time.Now().After(cached.Expires) {
		return cached.files, nil
	}
	var urls []SitemapURL
	if withSites {
		list, err := s.ListSitemapSites(ctx)
		if err != nil {
			return nil, err
		}
		for _, site := range list {
			if address := s.sitemapAddress(ctx, site, rootOnly); address != "" {
				lastmod := ""
				if site.LastDeployedAt.Valid {
					lastmod = site.LastDeployedAt.Time.UTC().Format(time.RFC3339)
				}
				urls = append(urls, SitemapURL{address, lastmod})
			}
		}
	}
	files := buildSitemaps(base, pages, urls)
	if cache.ByBrand == nil {
		cache.ByBrand = make(map[string]CachedSitemaps)
	}
	cache.ByBrand[base] = CachedSitemaps{time.Now().Add(SitemapTTL), files}
	return files, nil
}

// sitemapAddress is the address the sitemap lists for site, or "" when the
// site must not be listed.
func (s *Service) sitemapAddress(ctx context.Context, site model.Site, rootOnly bool) string {
	if s.disk.IsSuspended(site.UserID, site.Name) || s.disk.IsOffline(site.UserID, site.Name) || s.disk.HasPasscodeMarker(site.UserID, site.Name) {
		return ""
	}
	if !siteAllowsIndexing(ctx, s.disk, FileVersion{UserID: site.UserID, SiteName: site.Name}) {
		return ""
	}
	hosts := s.deps.Hosts
	family := ""
	if host, ok := hosts.SiteFamilyAddress(site.UserID, site.Name); ok {
		family = "https://" + host + "/"
	}
	address := LiveURL(site, family, hosts.Addressing().SiteURLFor(site))
	if address != "" && rootOnly {
		if u, err := url.Parse(address); err != nil || (u.Path != "" && u.Path != "/") {
			return ""
		}
	}
	return address
}

func xmlDocument(v any) []byte {
	body, _ := xml.Marshal(v)
	return append([]byte(xml.Header), body...)
}

func urlset(urls []SitemapURL) []byte {
	return xmlDocument(struct {
		XMLName xml.Name     `xml:"urlset"`
		XMLNS   string       `xml:"xmlns,attr"`
		URLs    []SitemapURL `xml:"url"`
	}{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", URLs: urls})
}

func buildSitemaps(base string, pages, sites []SitemapURL) map[string][]byte {
	files := map[string][]byte{"/sitemap-pages.xml": urlset(pages)}
	var children []SitemapURL
	children = append(children, SitemapURL{Loc: base + "/sitemap-pages.xml"})
	// The first site shard exists even when there are no eligible sites.
	for i := 0; i < len(sites) || i == 0; i += sitemapURLLimit {
		end := min(i+sitemapURLLimit, len(sites))
		path := fmt.Sprintf("/sitemap-sites-%d.xml", i/sitemapURLLimit+1)
		files[path] = urlset(sites[i:end])
		children = append(children, SitemapURL{Loc: base + path})
	}
	if len(pages)+len(sites) <= sitemapURLLimit {
		files["/sitemap.xml"] = urlset(append(append([]SitemapURL{}, pages...), sites...))
	} else {
		files["/sitemap.xml"] = xmlDocument(struct {
			XMLName xml.Name     `xml:"sitemapindex"`
			XMLNS   string       `xml:"xmlns,attr"`
			Maps    []SitemapURL `xml:"sitemap"`
		}{XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9", Maps: children})
	}
	return files
}

func siteAllowsIndexing(ctx context.Context, files SiteFiles, ref FileVersion) bool {
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
