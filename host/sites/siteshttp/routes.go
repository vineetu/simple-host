package siteshttp

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
)

// RegisterRoutes mounts the site routes. The limiters are shared with the account routes; deletePasscode
// is Access's passcode removal, which the admin's moderation route runs as the site's owner.
func (h *SiteHandler) RegisterRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter, exportLimiter *httpx.RateLimiter, deletePasscode http.HandlerFunc) {
	mux.Handle("POST /v1/sites/{sitename}", authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.createSite))))))
	mux.Handle("PUT /v1/sites/{sitename}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(h.updateSite)))))
	mux.Handle("DELETE /v1/sites/{sitename}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteSite)))))
	mux.Handle("PATCH /v1/sites/{sitename}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.patchSite)))))
	mux.Handle("POST /v1/sites/{sitename}/restore", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.restoreSite)))))
	mux.Handle("GET /v1/u/{handle}/showcase.json", http.HandlerFunc(h.showcaseFeed))
	mux.Handle("GET /v1/me/bio", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseBio))))
	mux.Handle("PUT /v1/me/bio", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseBio))))
	mux.Handle("GET /v1/sites/{sitename}/showcase", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseSite))))
	mux.Handle("PUT /v1/sites/{sitename}/showcase", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseSite))))
	mux.Handle("GET /v1/me/home", noticeMiddleware(authMiddleware(http.HandlerFunc(h.homeSetting))))
	mux.Handle("PUT /v1/me/home", noticeMiddleware(authMiddleware(http.HandlerFunc(h.homeSetting))))
	mux.Handle("GET /v1/me/deleted-sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDeletedSites))))
	mux.Handle("GET /v1/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSites))))
	mux.Handle("GET /v1/admin/sites", authMiddleware(http.HandlerFunc(h.adminSites)))
	// The limiter sits outside auth so wrong keys typed at /admin count too.
	mux.Handle("GET /v1/admin/users", httpx.RateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.adminUsers))))
	// Versions, Make active, Data, domain and Delete on anyone's site, by id,
	// run as its owner (adminsite.go).
	h.registerAdminSiteActions(mux, authMiddleware, deletePasscode)
	mux.Handle("GET /v1/admin/export.tar.gz", authMiddleware(http.HandlerFunc(h.exportAll)))
	mux.Handle("GET /v1/admin/usage", authMiddleware(http.HandlerFunc(h.adminUsage)))
	// Take your work with you. An event box is destroyed when the event ends and
	// nothing is backed up, so the only honest answer is to make leaving easy.
	mux.Handle("GET /v1/sites/{sitename}/export.tar.gz", authMiddleware(httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(h.exportSite))))
	// The same export behind a 10-minute signed link, for people with no API
	// key (chat-app connector users): minted by the owner, opened by a click.
	mux.Handle("POST /v1/sites/{sitename}/export-link", authMiddleware(h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.createExportLink)))))
	mux.Handle("GET /v1/export", httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(h.downloadExport)))
	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersionFiles))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files/{path...}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getVersionFile))))
	mux.Handle("PUT /v1/sites/{sitename}/active-version", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setActiveVersion))))
	// Not in auth/scope.go deployRoutes or editorRoutes on purpose: a
	// deploy-only key or a site editor cannot remove a version.
	mux.Handle("DELETE /v1/sites/{sitename}/versions/{version}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.discardVersion)))))
	mux.Handle("POST /v1/sites/{sitename}/versions/{version}/preview-link", authMiddleware(h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.createPreviewLink)))))
	mux.Handle("GET /v1/sites/{sitename}/analytics", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteAnalytics))))
	mux.Handle("GET /v1/sites/{sitename}/analytics/geo", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteGeoAnalytics))))
	analyticsLimiter := httpx.NewRateLimiterFor(h.Limits.RateAnalytics.Burst, h.Limits.RateAnalytics.Every)
	h.Caches.Rate("http.site.analyticsLimiter", analyticsLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/sites/{sitename}/analytics/top", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(analyticsLimiter, http.HandlerFunc(h.getSiteTopAnalytics)))))
	// Deliberately not /v1/sites/analytics: that would collide with a site
	// actually named "analytics".
	mux.Handle("GET /v1/analytics/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getAnalyticsSummary))))
	mux.Handle("PUT /v1/sites/{sitename}/visibility", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setVisibility))))
	mux.Handle("PUT /v1/sites/{sitename}/keep", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setSiteKeep))))
	// 0 = the instance's KEEP_VERSIONS; setting it prunes at once (versions.go).
	mux.Handle("PUT /v1/sites/{sitename}/keep-versions", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.setKeepVersions)))))
	mux.Handle("GET /v1/admin/idle-sites", authMiddleware(http.HandlerFunc(h.adminIdleSites)))

	// JSON deploy: inline file contents, same auth and rate-limit chain as the archive upload.
	mux.Handle("POST /v1/sites/{sitename}/files", authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.createSiteFiles))))))
	mux.Handle("PUT /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(h.UpdateSiteFiles)))))
	photos := h.Ed.Storage().Photos
	mux.Handle("POST /v1/sites/{sitename}/photos", web.PhotosHostOnly(photos, authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, h.CreateIdempotency.Wrap(http.HandlerFunc(h.AddPhotos))))))

	// The page origins allowed to call this site's API (visitor sign-in reads
	// them). Event and team sites never take extra origins.
	if !h.Ed.Storage().EventSites {
		mux.Handle("PUT /v1/sites/{sitename}/allowed-origins", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setAllowedOrigins))))
	}

	// Custom domain (one per site): owner binds a hostname, gets a CNAME record
	// to create; Caddy on-demand TLS asks /internal/tls-ask before issuing a cert.
	mux.Handle("POST /v1/sites/{sitename}/domain", authMiddleware(h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.bindDomain)))))
	mux.Handle("GET /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDomain))))
	mux.Handle("DELETE /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteDomain))))
	mux.Handle("POST /v1/sites/{sitename}/domain/check", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(h.DomainCheckLimiter, http.HandlerFunc(h.checkDomainNow)))))
	tlsAskLimiter := httpx.NewRateLimiterFor(h.Limits.RateTLSAsk.Burst, h.Limits.RateTLSAsk.Every)
	h.Caches.Rate("http.site.tlsAskLimiter", tlsAskLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /internal/tls-ask", httpx.RateLimitByIP(tlsAskLimiter, http.HandlerFunc(h.tlsAsk)))
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}", h.domainRedirect)
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}/{rest...}", h.domainRedirect)

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
	mux.Handle("GET /v1/admin/sites/{id}/versions", authMiddleware(h.asSiteOwner(h.listVersions)))
	mux.Handle("PUT /v1/admin/sites/{id}/active-version", authMiddleware(h.asSiteOwner(h.setActiveVersion)))
	mux.Handle("POST /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.bindDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.deleteDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}", authMiddleware(h.asSiteOwner(h.deleteSite)))
	// Moderation of a site behind a passcode: remove the passcode, or see a
	// version through a preview link, without knowing the passcode (the
	// admin never reads it). Logged like every call here.
	mux.Handle("DELETE /v1/admin/sites/{id}/lock", authMiddleware(h.asSiteOwner(deletePasscode)))
	mux.Handle("POST /v1/admin/sites/{id}/versions/{version}/preview-link", authMiddleware(h.asSiteOwner(h.createPreviewLink)))
	mux.Handle("DELETE /v1/admin/sites/{id}/versions/{version}", authMiddleware(h.asSiteOwner(h.discardVersion)))
}

func (h *SiteHandler) registerFamilyRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	checkIP := h.familyCheckLimiter
	if checkIP == nil {
		checkIP = httpx.NewRateLimiterFor(h.Limits.RateFamilyCheck.Burst, h.Limits.RateFamilyCheck.Every)
	}
	mux.Handle("POST /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createFamily))))
	mux.Handle("GET /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listFamilies))))
	mux.Handle("GET /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getFamily))))
	mux.Handle("PATCH /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.patchFamily))))
	mux.Handle("DELETE /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteFamily))))
	mux.Handle("POST /v1/me/address-families/{suffix}/check", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(checkIP, http.HandlerFunc(h.checkFamilyNow)))))
	mux.Handle("GET /v1/admin/address-families", authMiddleware(http.HandlerFunc(h.adminListFamilies)))
	mux.Handle("POST /v1/admin/users/{id}/address-families", authMiddleware(http.HandlerFunc(h.adminCreateFamily)))
	mux.Handle("PUT /v1/admin/address-families/{id}/cert-mode", authMiddleware(http.HandlerFunc(h.adminSetFamilyCert)))
	mux.Handle("PUT /v1/admin/address-families/{id}/proof-exempt", authMiddleware(http.HandlerFunc(h.adminSetFamilyProofExempt)))
	mux.Handle("POST /v1/admin/address-families/{id}/check", authMiddleware(http.HandlerFunc(h.adminCheckFamily)))
	mux.Handle("DELETE /v1/admin/address-families/{id}", authMiddleware(http.HandlerFunc(h.adminDeleteFamily)))
	mux.HandleFunc("GET /internal/family/{rest...}", h.familyPageHandler)
}
