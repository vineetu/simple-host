// Package accesshttp holds Access's HTTP routes: accounts, keys, sign-in
// (email codes, OAuth, SSO), the connector, site passcodes, named viewers,
// visitor sessions, and the gate in front of every site (SiteGate) and every
// data route (StorageCaller, PasscodeGate).
//
// Routes are mounted in routes.go; copy a line there, e.g.
//
//	mux.Handle("GET /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSiteEditors))))
//
// and write the handler method beside its siblings. A handler reads the
// request, calls one function of host/access (or host/ops for a workflow
// that spans modules) and writes the answer; decisions live there. Web work
// stays here: cookies, redirects, which page to show and mapping a typed
// refusal to its status (httpx.RefusalStatus). The account, OAuth and
// connector handlers mount their own routes in Register (user.go, oauth.go,
// connector.go). It imports the modules, host/web and host/httpx, never
// another route package or a repo package.
package accesshttp
