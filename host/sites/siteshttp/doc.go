// Package siteshttp holds Sites' HTTP routes: deploys, versions, domains,
// address families, exports, showcase, analytics, admin site actions, and
// serving sites at their own hosts.
//
// Routes are mounted in routes.go (RegisterRoutes); copy a line there, e.g.
//
//	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
//
// The site rules and workflows live in host/sites (sites.Service, sites.Hosts);
// this package reads the request, calls them and writes the answer. Visitors
// pass Access's gate (the gate field, set by host/handler). It imports
// host/sites, host/access, host/data, host/web and host/httpx, never another
// route package.
package siteshttp
