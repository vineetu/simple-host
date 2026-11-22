package siteshttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/admin"
	"github.com/vsriram/simple-host/host/sites/deploy"
	"github.com/vsriram/simple-host/host/sites/domains"
	"github.com/vsriram/simple-host/host/sites/export"
	"github.com/vsriram/simple-host/host/sites/families"
	"github.com/vsriram/simple-host/host/sites/idle"
	"github.com/vsriram/simple-host/host/sites/lifecycle"
	"github.com/vsriram/simple-host/host/sites/showcase"
	"github.com/vsriram/simple-host/host/sites/siteanalytics"
	"github.com/vsriram/simple-host/host/sites/sitelist"
	"github.com/vsriram/simple-host/host/sites/versions"
)

// RegisterRoutes mounts the site routes. The limiters are shared with the account routes; deletePasscode
// is Access's passcode removal, which the admin's moderation route runs as the site's owner.
func (h *SiteHandler) RegisterRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter, exportLimiter *httpx.RateLimiter, deletePasscode http.HandlerFunc) {
	deploy.Register(mux, h.Handler, authMiddleware, noticeMiddleware)
	lifecycle.Register(mux, h.Handler, authMiddleware, noticeMiddleware, siteOpLimiter)
	showcase.Register(mux, h.Handler, authMiddleware, noticeMiddleware)
	sitelist.Register(mux, h.Handler, authMiddleware, noticeMiddleware)
	admin.Register(mux, h.Handler, authMiddleware, siteOpLimiter)
	// Versions, Make active, Data, domain and Delete on anyone's site, by id,
	// run as its owner (adminsite.go).
	h.registerAdminSiteActions(mux, authMiddleware, deletePasscode)
	versions.Register(mux, h.Handler, authMiddleware, noticeMiddleware, siteOpLimiter)
	siteanalytics.Register(mux, h.Handler, authMiddleware, noticeMiddleware)
	export.Register(mux, h.Handler, authMiddleware, noticeMiddleware, exportLimiter)
	idle.Register(mux, h.Handler, authMiddleware)

	// Custom domain (one per site): owner binds a hostname, gets a CNAME record
	// to create; Caddy on-demand TLS asks /internal/tls-ask before issuing a cert.
	domains.Register(mux, h.Handler, authMiddleware, noticeMiddleware)

	// Per-user public showcase + branded 404s, reached only via the content-host
	// nginx block (single-segment /<handle> -> showcase; file misses -> notfound).
	mux.HandleFunc("GET /internal/showcase/{handle}", h.Showcase)
	mux.HandleFunc("GET /internal/site-redirect/{handle}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}/{sitename}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}/{sitename}/{rest...}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/notfound", h.NotFound)
	mux.HandleFunc("GET /internal/offline", h.ServeOffline) // nginx and Caddy send sites carrying the offline marker here
	mux.HandleFunc("GET /internal/passcode/{rest...}", h.passcodePageHandler)
	h.registerFamilyRoutes(mux, authMiddleware, noticeMiddleware)
}

func (h *SiteHandler) registerAdminSiteActions(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler, deletePasscode http.HandlerFunc) {
	domains.RegisterAdmin(mux, h.Handler, authMiddleware)
	// Moderation of a site behind a passcode: remove the passcode without
	// knowing it (the admin never reads it; the preview-link mirror is the
	// versions feature's). Logged like every call here.
	mux.Handle("DELETE /v1/admin/sites/{id}/lock", authMiddleware(h.AsSiteOwner(deletePasscode)))
}

func (h *SiteHandler) registerFamilyRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	families.Register(mux, h.Handler, authMiddleware, noticeMiddleware)
	mux.HandleFunc("GET /internal/family/{rest...}", h.familyPageHandler)
}
