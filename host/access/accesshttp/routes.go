package accesshttp

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

// Site editors: a person who may publish, update and roll back one site of
// an account without belonging to it (accounts.ActsAsOwner). The account
// itself, or a member of the team with owner=<team>, manages them; an editor
// never reaches these routes (auth.ScopeGate holds editors to EditorAllows).
// Offered where accounts own their own sites, like teams.
func (h *AccessHandler) RegisterEditors(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	if !h.Ed.Identity().PersonalSites {
		return
	}
	mux.Handle("GET /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSiteEditors))))
	mux.Handle("POST /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.addSiteEditor))))
	mux.Handle("DELETE /v1/sites/{sitename}/editors/{person}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.removeSiteEditor))))
}

// RegisterAccountRoutes mounts the account's own routes and the operator's
// account routes; siteOpLimiter and exportLimiter are the site routes' own,
// shared with them.
func (h *AccessHandler) RegisterAccountRoutes(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler, siteOpLimiter, exportLimiter *httpx.RateLimiter) {
	mux.Handle("POST /v1/admin/users", authMiddleware(http.HandlerFunc(h.createAccounts)))
	mux.Handle("DELETE /v1/admin/users/{id}", authMiddleware(http.HandlerFunc(h.deleteAccount)))
	mux.Handle("POST /v1/admin/users/{id}/key", authMiddleware(http.HandlerFunc(h.reissueAccountKey)))
	mux.Handle("PATCH /v1/me", authMiddleware(http.HandlerFunc(h.patchMe)))
	// Download my data / delete my account and all data (account_data.go),
	// under the export limiter the site export shares.
	mux.Handle("GET /v1/me/export.zip", authMiddleware(httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(h.exportMe))))
	// The address before the download became a .zip; serves the same zip.
	mux.Handle("GET /v1/me/export.tar.gz", authMiddleware(httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(h.exportMe))))
	mux.Handle("DELETE /v1/me", authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteMe))))
	// The limiter sits outside auth so wrong keys typed at /admin count too.
	mux.Handle("GET /v1/admin/signups", httpx.RateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.adminSignups))))
	mux.Handle("GET /v1/admin/signups.csv", httpx.RateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.adminSignupsCSV))))
	// Operator take-down (suspend.go): a site, or a person and all their
	// sites, without deleting anything; restore / enable reverses it.
	mux.Handle("POST /v1/admin/sites/{id}/suspend", authMiddleware(h.setSiteSuspension(true)))
	mux.Handle("POST /v1/admin/sites/{id}/restore", authMiddleware(h.setSiteSuspension(false)))
	mux.Handle("POST /v1/admin/users/{id}/suspend", authMiddleware(h.setUserSuspension(true)))
	mux.Handle("POST /v1/admin/users/{id}/enable", authMiddleware(h.setUserSuspension(false)))
	// Where nginx and Caddy send a request for a site whose folder carries the
	// take-down marker (suspend.go).
	mux.HandleFunc("GET /internal/suspended", h.ServeTakedown) // nginx and Caddy send a request for a site whose folder carries the marker here
}

// RegisterGateRoutes mounts the passcode, named-viewer and visitor sign-in
// routes; siteOpLimiter is the site routes' own, shared with them.
func (h *AccessHandler) RegisterGateRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
	// Site passcodes (passcode_api.go, passcode_unlock.go): the owner's switch, and the gate's form.
	mux.Handle("GET /v1/sites/{sitename}/lock", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSitePasscode))))
	mux.Handle("PUT /v1/sites/{sitename}/lock", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.putSitePasscode)))))
	mux.Handle("DELETE /v1/sites/{sitename}/lock", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.DeleteSitePasscode)))))
	mux.Handle("POST /v1/sites/{sitename}/lock/sign-out-everyone", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.signOutEveryone)))))
	unlockLimiter := httpx.NewRateLimiterFor(h.Limits.RateVisitor.Burst, h.Limits.RateVisitor.Every)
	h.HTTPCaches.Rate("http.site.unlockLimiter", unlockLimiter, 10*time.Minute, 30*time.Minute)
	h.HTTPCaches.Add("http.site.passcodes", 5*time.Minute, h.Passcode.evict)
	mux.Handle("POST /v1/site-unlock", httpx.RateLimitByIP(unlockLimiter, http.HandlerFunc(h.UnlockSite)))
	// Named viewers (viewers.go): who can open the site, and the gate's own
	// sign-in and sign-out forms.
	mux.Handle("GET /v1/sites/{sitename}/access", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteAccess))))
	mux.Handle("PUT /v1/sites/{sitename}/access", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.putSiteAccess)))))
	mux.Handle("POST /v1/sites/{sitename}/viewers", authMiddleware(httpx.RateLimitByIP(siteOpLimiter, h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.addSiteViewers))))))
	mux.Handle("DELETE /v1/sites/{sitename}/viewers/{email}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.removeSiteViewer)))))
	mux.Handle("POST /v1/site-signin", httpx.RateLimitByIP(unlockLimiter, http.HandlerFunc(h.viewerSignIn)))
	mux.Handle("POST /v1/site-signout", httpx.RateLimitByIP(unlockLimiter, http.HandlerFunc(h.viewerSignOut)))

	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/me", h.optionsSiteRoute)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/visitor/auth", h.optionsSiteRoute)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/visitor/auth/verify", h.optionsSiteRoute)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/me", h.optionsSiteRoute)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/visitor/auth", h.optionsSiteRoute)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/visitor/auth/verify", h.optionsSiteRoute)
	mux.Handle("GET /v1/sites/{sitename}/me", h.signInGate(http.HandlerFunc(h.getVisitorMe)))
	mux.Handle("POST /v1/sites/{sitename}/visitor/auth", h.signInGate(http.HandlerFunc(h.requestVisitorEmail)))
	mux.Handle("POST /v1/sites/{sitename}/visitor/auth/verify", h.signInGate(http.HandlerFunc(h.verifyVisitorEmail)))

	// The same sign-in routes under the person's address: {handle} names the site exactly.
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/me", h.signInGate(http.HandlerFunc(h.getVisitorMe)))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/visitor/auth", h.signInGate(http.HandlerFunc(h.requestVisitorEmail)))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/visitor/auth/verify", h.signInGate(http.HandlerFunc(h.verifyVisitorEmail)))

	// Visitor session cookie is issued here (content host / custom domain),
	// never on the apex OAuth callback.
	visitorLimiter := httpx.NewRateLimiterFor(h.Limits.RateVisitor.Burst, h.Limits.RateVisitor.Every)
	h.HTTPCaches.Rate("http.site.visitorLimiter", visitorLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/visitor/establish", httpx.RateLimitByIP(visitorLimiter, http.HandlerFunc(h.establishVisitor)))
	mux.Handle("POST /v1/visitor/logout", httpx.RateLimitByIP(visitorLimiter, http.HandlerFunc(h.logoutVisitor)))
}
