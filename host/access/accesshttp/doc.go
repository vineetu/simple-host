// Package accesshttp holds Access's HTTP routes: accounts, keys, sign-in
// (email codes, OAuth, SSO), the connector, site passcodes, named viewers,
// visitor sessions, and the gate in front of every site (SiteGate) and every
// data route (StorageCaller, PasscodeGate).
//
// Routes are mounted in routes.go; copy a line there, e.g.
//
//	mux.Handle("GET /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSiteEditors))))
//
// and write the handler method beside its siblings. The rules (who may do
// what) live in host/access; this package reads the request, calls them and
// writes the answer. It imports host/access, host/sites, host/data, host/web
// and host/httpx, never another route package.
package accesshttp
