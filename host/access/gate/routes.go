package gate

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
)

// RegisterGateRoutes mounts the passcode, named-viewer and visitor sign-in
// routes; siteOpLimiter is the site routes' own, shared with them.
func (h *Handler) RegisterGateRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
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
