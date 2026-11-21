package handler

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	"github.com/vsriram/simple-host/host/data/datahttp"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
)

// Register mounts the site, access and data routes on mux. The rate
// limiters for site operations and exports are shared between the site
// routes and the account routes.
func Register(mux *http.ServeMux, h *siteshttp.SiteHandler, a *accesshttp.AccessHandler, d *datahttp.DataHandler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	d.RegisterNotifications(mux, authMiddleware, noticeMiddleware)
	a.RegisterEditors(mux, authMiddleware, noticeMiddleware)
	d.RegisterWebPush(mux, a.PasscodeGate, a.StorageCaller, authMiddleware, noticeMiddleware)
	d.RegisterStorageRoutes(mux, a.StorageCaller)
	// Delete, rename and restore each take per-site locks: rate-limited so a
	// loop cannot pile up locks or disk moves.
	siteOpLimiter := httpx.NewRateLimiterFor(h.Limits.RateSiteOps.Burst, h.Limits.RateSiteOps.Every)
	h.Caches.Rate("http.site.siteOpLimiter", siteOpLimiter, 10*time.Minute, 30*time.Minute)
	// Owner downloads are heavy (a tar of every site): RATE_LIMIT_EXPORT per
	// address, like the signed export link; shared with the account export.
	exportLimiter := httpx.NewRateLimiterFor(h.Limits.RateExport.Burst, h.Limits.RateExport.Every)
	h.Caches.Rate("http.site.exportLimiter", exportLimiter, 10*time.Minute, 30*time.Minute)
	h.RegisterRoutes(mux, authMiddleware, noticeMiddleware, siteOpLimiter, exportLimiter, a.DeleteSitePasscode)
	a.RegisterAccountRoutes(mux, authMiddleware, siteOpLimiter, exportLimiter)
	a.RegisterGateRoutes(mux, authMiddleware, noticeMiddleware, siteOpLimiter)
}
