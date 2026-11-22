// Package siteshttp holds Sites' HTTP routes: deploys, versions, domains,
// address families, exports, showcase, analytics, admin site actions, and
// serving sites at their own hosts.
//
// Routes are mounted in routes.go (RegisterRoutes); copy a line there, e.g.
//
//	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
//
// A handler reads the request, calls one function of host/sites
// (sites.Service, sites.Hosts) or host/ops and writes the answer; decisions
// live there. Web work stays here: serving sites at their hosts, streaming
// files, redirects, which page to show and mapping a typed refusal to its
// status. Setup, the dashboard pages, event domains and name peers mount
// their own routes (setup.go, ui.go, eventdomain.go, namepeer.go). The
// domain and address-family check workers still live here (domaincheck.go,
// familyapi.go). Visitors pass Access's gate (the gate field, set by
// host/handler). It imports the modules, host/web and host/httpx, never
// another route package or a repo package.
package siteshttp
