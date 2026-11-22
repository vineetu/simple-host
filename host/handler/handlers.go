package handler

import (
	"context"
	"database/sql"
	"log"
	"time"

	"github.com/vsriram/simple-host/host/access/accesshttp"
	appstorage "github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/data/notify"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/storage"
	"github.com/vsriram/simple-host/host/web"
)

// NewSiteHandlers builds the site, access and data handlers and the state
// they share (limits, caches, idempotency, the passcode key, notifications,
// the visitor gate). productInstanceURL is a product edition's own public
// address (the pipeline's InkBaseURL; "" takes the edition's canonical one).
func NewSiteHandlers(ed *edition.Edition, productInstanceURL string, database *sql.DB, disk *storage.DiskStorage, hosts *sites.Hosts, siteDomain, contentHost, cnameTarget, customDomainIP, deployScript, adminAPIKey string, previewAccounts map[string]bool, previewTTL time.Duration, adminUserID string, mailer email.Sender, emailLimiter *httpx.RateLimiter) (*siteshttp.SiteHandler, *accesshttp.AccessHandler, *appstorage.Handler) {
	lim := ed.Limits()
	if hosts == nil {
		hosts = sites.NewHosts(database, disk, ed, siteDomain, contentHost, cnameTarget, customDomainIP)
	}
	uploadLimiter := httpx.NewRateLimiterFor(lim.RateUpload.Burst, lim.RateUpload.Every)
	storageIPLimiter := httpx.NewRateLimiterFor(lim.RateStorageIP.Burst, lim.RateStorageIP.Every)
	storageVisitorLimiter := httpx.NewRateLimiterFor(lim.RateStorageVisitor.Burst, lim.RateStorageVisitor.Every)

	visitorAuthLimiter := httpx.NewRateLimiterFor(lim.RateVisitorAuth.Burst, lim.RateVisitorAuth.Every)
	domainCheckLimiter := httpx.NewRateLimiterFor(lim.RateDomainCheck.Burst, lim.RateDomainCheck.Every)
	domainCheckUserLimiter := httpx.NewRateLimiterFor(lim.RateDomainCheckUser.Burst, lim.RateDomainCheckUser.Every)

	h := &siteshttp.SiteHandler{Handler: &sitesroute.Handler{
		Ed:                     ed,
		Limits:                 lim,
		Mailer:                 mailer,
		Database:               database,
		Disk:                   disk,
		SiteDomain:             siteDomain,
		ContentHost:            contentHost,
		CNAMETarget:            cnameTarget,
		CustomDomainIP:         customDomainIP,
		Hosts:                  hosts,
		DeployScript:           deployScript,
		UploadLimiter:          uploadLimiter,
		DomainCheckLimiter:     domainCheckLimiter,
		DomainCheckUserLimiter: domainCheckUserLimiter,
		PreviewAccounts:        previewAccounts,
		PreviewTTL:             previewTTL,
		ExportKey:              sitesroute.NewExportKey(),
		Network:                operations.NewNetworkUsage(database, ed),
		Reporting:              operations.NewReporting(database),
	}}
	h.Assets = web.NewAssetPipeline(ed, productInstanceURL)
	h.CreateIdempotency = idempotency.New(lim)
	access := &accesshttp.AccessHandler{Hosts: hosts, Ed: ed, Limits: h.Limits, Database: database, Disk: disk, Mailer: mailer, EmailLimiter: emailLimiter, VisitorAuthLimiter: visitorAuthLimiter,
		SiteDomain: siteDomain, ContentHost: contentHost, ExportKey: h.ExportKey, UploadLocks: &h.UploadLocks, AdminAPIKey: adminAPIKey, AdminUserID: adminUserID,
		Pages: h.Assets, HTTPCaches: &h.Caches, CreateIdempotency: h.CreateIdempotency, Reporting: h.Reporting}
	access.Passcode.Limits = h.Limits
	data := &appstorage.Handler{Hosts: hosts, Ed: ed, Limits: h.Limits, Database: database, Disk: disk, Mailer: mailer,
		ExportKey: h.ExportKey, CreateIdempotency: h.CreateIdempotency, HTTPCaches: &h.Caches, PasscodeKeys: access.PasscodeKeys(), UploadLocks: &h.UploadLocks,
		Sqlite: appstorage.NewSQLiteGate(lim.StorageSQLConcurrency, lim.StorageAcquireWait), StorageIPLimiter: storageIPLimiter, StorageVisitorLimiter: storageVisitorLimiter,
		SpamIPLimiter: httpx.NewRateLimiterFor(lim.RateSpamCheckedIP.Burst, lim.RateSpamCheckedIP.Every), SpamSiteLimiter: httpx.NewRateLimiterFor(lim.RateSpamCheckedSite.Burst, lim.RateSpamCheckedSite.Every)}
	h.Caches.Add("http.site.idempotency", time.Hour, h.CreateIdempotency.EvictExpired)
	h.ShowcaseLimiter = httpx.NewRateLimiter(60, 30)
	h.Caches.Rate("http.site.showcaseLimiter", h.ShowcaseLimiter, 10*time.Minute, 30*time.Minute)
	if len(previewAccounts) > 0 {
		ttlHours := int(previewTTL.Hours())
		for u := range previewAccounts {
			if n, err := h.SitesSvc().BackfillPreviewExpiry(context.Background(), u, ttlHours); err != nil {
				log.Printf("preview backfill %q: %v", u, err)
			} else if n > 0 {
				log.Printf("preview backfill: stamped expiry on %d existing site(s) for %q", n, u)
			}
		}
		log.Printf("preview-site expiry enabled: accounts=%d ttl=%s", len(previewAccounts), previewTTL)
	}
	if customDomainIP != "" {
		log.Printf("custom-domain verification enabled: every %s, active re-proved after %s", h.Limits.DomainCheckInterval, sitesroute.DomainActiveAge)
	}
	h.Caches.Rate("http.site.storageIPLimiter", storageIPLimiter, 10*time.Minute, 30*time.Minute)
	h.Caches.Rate("http.site.storageVisitorLimiter", storageVisitorLimiter, 10*time.Minute, 30*time.Minute)
	h.Caches.Rate("http.site.domainCheckLimiter", domainCheckLimiter, 10*time.Minute, 30*time.Minute)
	h.Caches.Rate("http.site.domainCheckUserLimiter", domainCheckUserLimiter, 10*time.Minute, 30*time.Minute)
	h.Caches.Rate("http.site.visitorAuthLimiter", visitorAuthLimiter, 10*time.Minute, 30*time.Minute)
	h.Caches.Rate("http.site.uploadLimiter", uploadLimiter, 10*time.Minute, 30*time.Minute)
	data.SqliteSites = appstorage.NewSiteSlots(lim.StorageSiteInflight, data.Sqlite, lim.StorageAcquireWait)
	data.Notifications = notify.NewEngine(database, ed, lim, siteDomain)
	h.SqliteSites, h.Notify, access.Notify = data.SqliteSites, data.Notifications, data.Notifications
	access.SiteFiles, access.SiteArchive = h.LifecycleSvc(), h.SiteArchive()
	h.Gate = access.SiteGate()
	return h, access, data
}

// SetStorageBindings gives Access and Data the product's storage bindings,
// once at startup: Data's saved-data rules and Access's "is this visitor the
// site's owner here" read the same provider. nil keeps the platform's own.
func SetStorageBindings(a *accesshttp.AccessHandler, d *appstorage.Handler, p appstorage.BindingProvider) {
	d.SetStorageBindings(p)
	a.SetStorageBindings(p)
}
