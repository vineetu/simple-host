package siteanalytics

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// routes are the analytics handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts a site's analytics reads and the per-site summary.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	a := routes{h}
	mux.Handle("GET /v1/sites/{sitename}/analytics", noticeMiddleware(authMiddleware(http.HandlerFunc(a.getSiteAnalytics))))
	mux.Handle("GET /v1/sites/{sitename}/analytics/geo", noticeMiddleware(authMiddleware(http.HandlerFunc(a.getSiteGeoAnalytics))))
	analyticsLimiter := httpx.NewRateLimiterFor(h.Limits.RateAnalytics.Burst, h.Limits.RateAnalytics.Every)
	h.Caches.Rate("http.site.analyticsLimiter", analyticsLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/sites/{sitename}/analytics/top", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(analyticsLimiter, http.HandlerFunc(a.getSiteTopAnalytics)))))
	// Deliberately not /v1/sites/analytics: that would collide with a site
	// actually named "analytics".
	mux.Handle("GET /v1/analytics/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(a.getAnalyticsSummary))))
}
