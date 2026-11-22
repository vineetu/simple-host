// Package sitemap builds an instance's sitemap files: the host's own pages
// and every site a search engine may index at its live address, sharded past
// 50,000 entries and cached per brand for SitemapTTL. Which sites are eligible
// is the core's ListSitemapSites (with a product's SitemapFilter); the route
// is Serve (routes.go), mounted by host/sites/siteshttp/ui.go.
package sitemap

import "github.com/vsriram/simple-host/host/sites"

// Service builds sitemaps on the instance's site service.
type Service struct{ *sites.Service }
