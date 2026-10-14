package handler

import (
	"net"
	"net/http"
	"strings"
)

// hostSitemapPaths and hackSitemapPaths are the platform's own public
// marketing pages, one list per brand — the same binary answers both domains,
// told apart the same way chromeDataFor does. User showcases and sites live
// on their own hosts or handle paths and are not enumerated here; Google
// discovers those from their own links, same as any other user-generated page.
var (
	hostSitemapPaths = []string{
		"/", "/features", "/install.html", "/hackathons",
		"/enterprise", "/enterprise/brief", "/enterprise/architecture",
		"/docs.html", "/architecture.html", "/terms", "/support", "/privacy.html",
	}
	hackSitemapPaths = []string{
		"/", "/get-started", "/directory", "/watch", "/terms", "/privacy.html",
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

// serveRobotsTxt and serveSitemapXML answer the platform's own robots.txt and
// sitemap.xml for the apex marketing site, host-aware. Pages that must not be
// indexed already carry their own noindex meta tag or X-Robots-Tag header
// (admin.html, the signed-out events flow, offline/suspended/preview tenant
// pages), so this stays a plain allow-all plus a pointer at the sitemap
// rather than duplicating that per-path logic here.
func serveRobotsTxt(w http.ResponseWriter, r *http.Request) {
	base := "https://simple-host.app"
	if isHackHost(r) {
		base = "https://simple-hack.app"
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("User-agent: *\nAllow: /\nSitemap: " + base + "/sitemap.xml\n"))
}

func serveSitemapXML(w http.ResponseWriter, r *http.Request) {
	base := "https://simple-host.app"
	paths := hostSitemapPaths
	if isHackHost(r) {
		base = "https://simple-hack.app"
		paths = hackSitemapPaths
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range paths {
		b.WriteString("  <url><loc>" + base + p + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}
