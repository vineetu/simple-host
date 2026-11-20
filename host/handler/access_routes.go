package handler

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
func (h *AccessHandler) registerEditors(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	if !h.ed.Identity().PersonalSites {
		return
	}
	mux.Handle("GET /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSiteEditors))))
	mux.Handle("POST /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.addSiteEditor))))
	mux.Handle("DELETE /v1/sites/{sitename}/editors/{person}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.removeSiteEditor))))
}

// registerGateRoutes mounts the passcode, named-viewer and visitor sign-in
// routes; siteOpLimiter is the site routes' own, shared with them.
func (h *AccessHandler) registerGateRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
	// Site passcodes (passcode.go): the owner's switch, and the gate's form.
	mux.Handle("GET /v1/sites/{sitename}/lock", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSitePasscode))))
	mux.Handle("PUT /v1/sites/{sitename}/lock", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.putSitePasscode)))))
	mux.Handle("DELETE /v1/sites/{sitename}/lock", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteSitePasscode)))))
	mux.Handle("POST /v1/sites/{sitename}/lock/sign-out-everyone", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.signOutEveryone)))))
	unlockLimiter := newRateLimiterFor(h.limits.RateVisitor)
	h.site.httpCaches.Rate("http.site.unlockLimiter", unlockLimiter, 10*time.Minute, 30*time.Minute)
	h.site.httpCaches.Add("http.site.passcodes", 5*time.Minute, h.passcode.evict)
	mux.Handle("POST /v1/site-unlock", rateLimitByIP(unlockLimiter, http.HandlerFunc(h.unlockSite)))
	// Named viewers (viewers.go): who can open the site, and the gate's own
	// sign-in and sign-out forms.
	mux.Handle("GET /v1/sites/{sitename}/access", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteAccess))))
	mux.Handle("PUT /v1/sites/{sitename}/access", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.putSiteAccess)))))
	mux.Handle("POST /v1/sites/{sitename}/viewers", authMiddleware(rateLimitByIP(siteOpLimiter, h.site.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.addSiteViewers))))))
	mux.Handle("DELETE /v1/sites/{sitename}/viewers/{email}", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.removeSiteViewer)))))
	mux.Handle("POST /v1/site-signin", rateLimitByIP(unlockLimiter, http.HandlerFunc(h.viewerSignIn)))
	mux.Handle("POST /v1/site-signout", rateLimitByIP(unlockLimiter, http.HandlerFunc(h.viewerSignOut)))

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
	visitorLimiter := newRateLimiterFor(h.limits.RateVisitor)
	h.site.httpCaches.Rate("http.site.visitorLimiter", visitorLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/visitor/establish", rateLimitByIP(visitorLimiter, http.HandlerFunc(h.establishVisitor)))
	mux.Handle("POST /v1/visitor/logout", rateLimitByIP(visitorLimiter, http.HandlerFunc(h.logoutVisitor)))
}
