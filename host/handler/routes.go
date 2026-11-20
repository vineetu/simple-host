package handler

import (
	"net/http"
	"time"
)

func (h *SiteHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h.data.registerNotifications(mux, authMiddleware, noticeMiddleware)
	h.access.registerEditors(mux, authMiddleware, noticeMiddleware)
	h.data.registerWebPush(mux, authMiddleware, noticeMiddleware)
	h.data.registerStorageRoutes(mux)
	mux.HandleFunc("GET /v1/turnstile/frame", h.data.turnstileFrame)
	mux.Handle("POST /v1/sites/{sitename}", authMiddleware(rateLimitByIP(h.uploadLimiter, h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.createSite))))))
	mux.Handle("PUT /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSite)))))
	// Delete, rename and restore each take per-site locks: rate-limited so a
	// loop cannot pile up locks or disk moves.
	siteOpLimiter := newRateLimiterFor(h.limits.RateSiteOps)
	h.httpCaches.Rate("http.site.siteOpLimiter", siteOpLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("DELETE /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteSite)))))
	mux.Handle("PATCH /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.patchSite)))))
	mux.Handle("POST /v1/sites/{sitename}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.restoreSite)))))
	mux.Handle("GET /v1/u/{handle}/showcase.json", http.HandlerFunc(h.showcaseFeed))
	mux.Handle("GET /v1/me/bio", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseBio))))
	mux.Handle("PUT /v1/me/bio", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseBio))))
	mux.Handle("GET /v1/sites/{sitename}/showcase", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseSite))))
	mux.Handle("PUT /v1/sites/{sitename}/showcase", noticeMiddleware(authMiddleware(http.HandlerFunc(h.showcaseSite))))
	mux.Handle("GET /v1/me/home", noticeMiddleware(authMiddleware(http.HandlerFunc(h.homeSetting))))
	mux.Handle("PUT /v1/me/home", noticeMiddleware(authMiddleware(http.HandlerFunc(h.homeSetting))))
	mux.Handle("GET /v1/me/deleted-sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDeletedSites))))
	mux.Handle("GET /v1/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSites))))
	mux.Handle("POST /v1/admin/users", authMiddleware(http.HandlerFunc(h.access.createAccounts)))
	mux.Handle("DELETE /v1/admin/users/{id}", authMiddleware(http.HandlerFunc(h.access.deleteAccount)))
	mux.Handle("POST /v1/admin/users/{id}/key", authMiddleware(http.HandlerFunc(h.access.reissueAccountKey)))
	mux.Handle("PATCH /v1/me", authMiddleware(http.HandlerFunc(h.access.patchMe)))
	// Download my data / delete my account and all data (account_data.go).
	// Owner downloads are heavy (a tar of every site): RATE_LIMIT_EXPORT per
	// address, like the signed export link.
	exportLimiter := newRateLimiterFor(h.limits.RateExport)
	h.httpCaches.Rate("http.site.exportLimiter", exportLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/me/export.zip", authMiddleware(rateLimitByIP(exportLimiter, http.HandlerFunc(h.access.exportMe))))
	// The address before the download became a .zip; serves the same zip.
	mux.Handle("GET /v1/me/export.tar.gz", authMiddleware(rateLimitByIP(exportLimiter, http.HandlerFunc(h.access.exportMe))))
	mux.Handle("DELETE /v1/me", authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.access.deleteMe))))
	mux.Handle("GET /v1/admin/sites", authMiddleware(http.HandlerFunc(h.adminSites)))
	// The limiter sits outside auth so wrong keys typed at /admin count too.
	mux.Handle("GET /v1/admin/users", rateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.adminUsers))))
	mux.Handle("GET /v1/admin/signups", rateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.access.adminSignups))))
	mux.Handle("GET /v1/admin/signups.csv", rateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(h.access.adminSignupsCSV))))
	// Operator take-down (suspend.go): a site, or a person and all their
	// sites, without deleting anything; restore / enable reverses it.
	mux.Handle("POST /v1/admin/sites/{id}/suspend", authMiddleware(h.access.setSiteSuspension(true)))
	mux.Handle("POST /v1/admin/sites/{id}/restore", authMiddleware(h.access.setSiteSuspension(false)))
	mux.Handle("POST /v1/admin/users/{id}/suspend", authMiddleware(h.access.setUserSuspension(true)))
	mux.Handle("POST /v1/admin/users/{id}/enable", authMiddleware(h.access.setUserSuspension(false)))
	// Versions, Make active, Data, domain and Delete on anyone's site, by id,
	// run as its owner (adminsite.go).
	h.registerAdminSiteActions(mux, authMiddleware)
	// Every site on the instance in one archive (export.go).
	mux.Handle("GET /v1/admin/export.tar.gz", authMiddleware(http.HandlerFunc(h.exportAll)))
	// What the disk is actually holding, and how much is left.
	mux.Handle("GET /v1/admin/usage", authMiddleware(http.HandlerFunc(h.adminUsage)))
	// Take your work with you. An event box is destroyed when the event ends and
	// nothing is backed up, so the only honest answer is to make leaving easy.
	mux.Handle("GET /v1/sites/{sitename}/export.tar.gz", authMiddleware(rateLimitByIP(exportLimiter, http.HandlerFunc(h.exportSite))))
	// The same export behind a 10-minute signed link, for people with no API
	// key (chat-app connector users): minted by the owner, opened by a click.
	mux.Handle("POST /v1/sites/{sitename}/export-link", authMiddleware(h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.createExportLink)))))
	mux.Handle("GET /v1/export", rateLimitByIP(exportLimiter, http.HandlerFunc(h.downloadExport)))
	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersionFiles))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files/{path...}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getVersionFile))))
	mux.Handle("PUT /v1/sites/{sitename}/active-version", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setActiveVersion))))
	// Not in auth/scope.go deployRoutes or editorRoutes on purpose: a
	// deploy-only key or a site editor cannot remove a version.
	mux.Handle("DELETE /v1/sites/{sitename}/versions/{version}", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.discardVersion)))))
	// An hour-long, owner-only preview address for one kept version (preview.go).
	mux.Handle("POST /v1/sites/{sitename}/versions/{version}/preview-link", authMiddleware(h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.createPreviewLink)))))
	mux.Handle("GET /v1/sites/{sitename}/analytics", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteAnalytics))))
	mux.Handle("GET /v1/sites/{sitename}/analytics/geo", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteGeoAnalytics))))
	analyticsLimiter := newRateLimiterFor(h.limits.RateAnalytics)
	h.httpCaches.Rate("http.site.analyticsLimiter", analyticsLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/sites/{sitename}/analytics/top", noticeMiddleware(authMiddleware(rateLimitByIP(analyticsLimiter, http.HandlerFunc(h.getSiteTopAnalytics)))))
	// Deliberately not /v1/sites/analytics: that would collide with a site
	// actually named "analytics".
	mux.Handle("GET /v1/analytics/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getAnalyticsSummary))))
	mux.Handle("PUT /v1/sites/{sitename}/visibility", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setVisibility))))
	// Idle-site cleanup (idle.go): the Keep flag and the admin dry run.
	mux.Handle("PUT /v1/sites/{sitename}/keep", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setSiteKeep))))
	// Deploys kept for this site (0 = the instance's KEEP_VERSIONS); setting
	// it prunes at once, and every later deploy prunes to it (versions.go).
	mux.Handle("PUT /v1/sites/{sitename}/keep-versions", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.setKeepVersions)))))
	mux.Handle("GET /v1/admin/idle-sites", authMiddleware(http.HandlerFunc(h.adminIdleSites)))

	// JSON deploy (LLM-friendly): file contents inline, no archive. Same auth +
	// rate-limit chain as the archive upload; CORS preflight is handled by the
	// CORS middleware.
	mux.Handle("POST /v1/sites/{sitename}/files", authMiddleware(rateLimitByIP(h.uploadLimiter, h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.createSiteFiles))))))
	mux.Handle("PUT /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSiteFiles)))))
	photos := h.ed.Storage().Photos
	mux.Handle("POST /v1/sites/{sitename}/photos", photosHostOnly(photos, authMiddleware(rateLimitByIP(h.uploadLimiter, h.createIdempotency.wrap(http.HandlerFunc(h.addPhotos))))))

	// The page origins allowed to call this site's API (visitor sign-in reads
	// them). Event and team sites never take extra origins.
	if !h.ed.Storage().EventSites {
		mux.Handle("PUT /v1/sites/{sitename}/allowed-origins", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setAllowedOrigins))))
	}

	// Custom domain (one per site): owner binds a hostname, gets a CNAME record
	// to create; Caddy on-demand TLS asks /internal/tls-ask before issuing a cert.
	mux.Handle("POST /v1/sites/{sitename}/domain", authMiddleware(h.createIdempotency.wrap(noticeMiddleware(http.HandlerFunc(h.bindDomain)))))
	mux.Handle("GET /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDomain))))
	mux.Handle("DELETE /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteDomain))))
	// "Check again": re-verify this site's pending domain now instead of
	// waiting for the next background pass.
	mux.Handle("POST /v1/sites/{sitename}/domain/check", noticeMiddleware(authMiddleware(rateLimitByIP(h.domainCheckLimiter, http.HandlerFunc(h.checkDomainNow)))))
	tlsAskLimiter := newRateLimiterFor(h.limits.RateTLSAsk)
	h.httpCaches.Rate("http.site.tlsAskLimiter", tlsAskLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /internal/tls-ask", rateLimitByIP(tlsAskLimiter, http.HandlerFunc(h.tlsAsk)))
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}", h.domainRedirect)
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}/{rest...}", h.domainRedirect)

	// Per-user public showcase + branded 404s, reached only via the content-host
	// nginx block (single-segment /<handle> -> showcase; file misses -> notfound).
	mux.HandleFunc("GET /internal/showcase/{handle}", h.showcase)
	// Old content-host addresses, once nginx hands them over (personhost.go).
	mux.HandleFunc("GET /internal/site-redirect/{handle}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}/{sitename}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/site-redirect/{handle}/{sitename}/{rest...}", h.contentHostRedirect)
	mux.HandleFunc("GET /internal/notfound", h.notFound)
	// Where nginx and Caddy send a request for a site whose folder carries the
	// take-down marker (suspend.go).
	mux.HandleFunc("GET /internal/suspended", h.access.suspendedPage)
	// ... and for a site its owner took offline (offline.go).
	mux.HandleFunc("GET /internal/offline", h.offlinePageHandler)
	// ... and for a site with a passcode (passcode.go): the gate, the file,
	// or the site's own address.
	mux.HandleFunc("GET /internal/passcode/{rest...}", h.access.passcodePageHandler)
	// Address families (familyapi.go): the API and the lives-elsewhere page.
	h.registerFamilyRoutes(mux, authMiddleware, noticeMiddleware)
	h.access.registerGateRoutes(mux, authMiddleware, noticeMiddleware, siteOpLimiter)
}

func (h *SiteHandler) registerAdminSiteActions(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/admin/sites/{id}/versions", authMiddleware(h.asSiteOwner(h.listVersions)))
	mux.Handle("PUT /v1/admin/sites/{id}/active-version", authMiddleware(h.asSiteOwner(h.setActiveVersion)))
	mux.Handle("POST /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.bindDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.deleteDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}", authMiddleware(h.asSiteOwner(h.deleteSite)))
	// Moderation of a site behind a passcode: remove the passcode, or see a
	// version through a preview link, without knowing the passcode (the
	// admin never reads it). Logged like every call here.
	mux.Handle("DELETE /v1/admin/sites/{id}/lock", authMiddleware(h.asSiteOwner(h.access.deleteSitePasscode)))
	mux.Handle("POST /v1/admin/sites/{id}/versions/{version}/preview-link", authMiddleware(h.asSiteOwner(h.createPreviewLink)))
	mux.Handle("DELETE /v1/admin/sites/{id}/versions/{version}", authMiddleware(h.asSiteOwner(h.discardVersion)))
}

// registerFamilyRoutes adds the family API to the mux.
func (h *SiteHandler) registerFamilyRoutes(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	checkIP := h.familyCheckLimiter
	if checkIP == nil {
		checkIP = newRateLimiterFor(h.limits.RateFamilyCheck)
	}
	mux.Handle("POST /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createFamily))))
	mux.Handle("GET /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listFamilies))))
	mux.Handle("GET /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getFamily))))
	mux.Handle("PATCH /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.patchFamily))))
	mux.Handle("DELETE /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteFamily))))
	mux.Handle("POST /v1/me/address-families/{suffix}/check", noticeMiddleware(authMiddleware(rateLimitByIP(checkIP, http.HandlerFunc(h.checkFamilyNow)))))
	mux.Handle("GET /v1/admin/address-families", authMiddleware(http.HandlerFunc(h.adminListFamilies)))
	mux.Handle("POST /v1/admin/users/{id}/address-families", authMiddleware(http.HandlerFunc(h.adminCreateFamily)))
	mux.Handle("PUT /v1/admin/address-families/{id}/cert-mode", authMiddleware(http.HandlerFunc(h.adminSetFamilyCert)))
	mux.Handle("PUT /v1/admin/address-families/{id}/proof-exempt", authMiddleware(http.HandlerFunc(h.adminSetFamilyProofExempt)))
	mux.Handle("POST /v1/admin/address-families/{id}/check", authMiddleware(http.HandlerFunc(h.adminCheckFamily)))
	mux.Handle("DELETE /v1/admin/address-families/{id}", authMiddleware(http.HandlerFunc(h.adminDeleteFamily)))
	mux.HandleFunc("GET /internal/family/{rest...}", h.familyPageHandler)
}
