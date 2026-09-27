package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/email"
	"github.com/vsriram/simple-host/internal/storage"
	"github.com/vsriram/simple-host/internal/tarball"
)

// maxSiteArchiveSize caps the upload body. Default 100 MB; override with
// MAX_ARCHIVE_MB on an instance you run yourself (a hosted instance has other
// people's disk to protect, your own does not).
//
// It is a guard against one upload filling a disk, not a budget anyone spends:
// measured across the sites running on simple-host, the median is 25 KB and
// nine in ten are under a megabyte. Nothing should be derived from this number
// — what the disk is really holding is reported by /v1/admin/usage.
//
// Raising this alone is not enough to accept a bigger site: any reverse proxy
// in front needs its own body cap raised to match, or it rejects the request
// before Go ever sees it. Lowering it goes through SetSiteLimit, which moves
// the extractor's uncompressed caps with it; setting this variable on its own
// would bound compressed bytes and nothing else.
var maxSiteArchiveSize int64 = 100 << 20

const maxSiteStateSize = 1 << 20

func init() {
	if v := os.Getenv("MAX_ARCHIVE_MB"); v != "" {
		if mb, err := strconv.Atoi(v); err == nil && mb > 0 {
			SetSiteLimit(int64(mb) << 20)
		}
	}
}

type SiteHandler struct {
	mailer         email.Sender
	emailLimiter   *rateLimiter
	database       *sql.DB
	disk           *storage.DiskStorage
	siteDomain     string
	contentHost    string // shared v3 content host, e.g. sites.simple-host.app
	cnameTarget    string // CNAME target for custom domains, e.g. cname.simple-host.app
	customDomainIP string // box public IPv4 for APEX custom-domain A records
	deployScript   string

	// uploadLocks serializes write+promote per site name (sitename -> *sync.Mutex).
	uploadLocks sync.Map

	// usage caches the disk measurement behind /v1/admin/usage. Measuring walks
	// every file under the data directory, so it is not done per request.
	usage usageCache

	// uploadLimiter throttles create/update uploads per client IP; stateLimiter
	// throttles per-site state writes (Origin-gated reads; writes also go
	// through visitorWriteOK). See ratelimit.go.
	uploadLimiter      *rateLimiter
	stateLimiter       *rateLimiter
	visitorAuthLimiter *rateLimiter
	domainCheckLimiter *rateLimiter
	// domainCheckUserLimiter: "Check again" per account.
	domainCheckUserLimiter *rateLimiter

	// previewAccounts (by username/email) get ephemeral sites: a site they create
	// expires after previewTTL and is removed by the background sweep. Empty =off.
	previewAccounts map[string]bool
	previewTTL      time.Duration

	// writeAuthMode is off | log | on (config default is log, never on).
	writeAuthMode string
	// adminAPIKey / adminUserID recognize owner/admin X-API-Key callers on
	// routes that are not wrapped with auth.Middleware: state/collections
	// writers, and the public collection GET (which also accepts an owner key).
	adminAPIKey string
	adminUserID string

	// personHosts is PERSON_HOSTS (personhost.go).
	personHosts personHostMode
	// siteHosts is SITE_HOSTS and siteCertDir SITE_CERT_DIR (sitehost.go).
	siteHosts   siteHostMode
	siteCertDir string
	// domainCertDir is DOMAIN_CERT_DIR (domaincert.go).
	domainCertDir string
	// idleCleanup is IDLE_CLEANUP=on and idleMaxEmails its per-run email cap
	// (idle.go).
	idleCleanup   bool
	idleMaxEmails int
	idleExempt    db.IdleExempt

	// exportKey signs short-lived export download links (exportlink.go); per
	// process, used for nothing else. publicBaseURL is the apex they point at.
	exportKey     []byte
	publicBaseURL string

	// savedData is the SAVED_DATA_* knobs, and readLimiter and appendLimiter
	// the saved-data read and list-append limits built from them (saveddata.go).
	savedData     config.SavedData
	readLimiter   *rateLimiter
	appendLimiter *rateLimiter
	// thinLimiter spaces out boundHistory's thinning: once a second per site.
	thinLimiter *rateLimiter
}

// lockSite acquires the per-site upload mutex for one account's site name and
// returns its unlock func. Keyed by account and name: two people's sites of the
// same name never wait on each other.
func (h *SiteHandler) lockSite(userID, name string) func() {
	mu, _ := h.uploadLocks.LoadOrStore(userID+"/"+name, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	m.Lock()
	return m.Unlock
}

type siteResponse struct {
	ID            string    `json:"id"`
	UserID        string    `json:"user_id"`
	Name          string    `json:"name"`
	ActiveVersion int       `json:"active_version"`
	SiteURL       string    `json:"site_url"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	CustomDomain  string    `json:"custom_domain,omitempty"`
	DomainStatus  string    `json:"domain_status,omitempty"`
	// Only in the site list: what is wrong with a domain that is not active
	// yet, the DNS record it needs, and when an unproven binding lapses.
	DomainLastError string     `json:"domain_last_error,omitempty"`
	DomainDNS       *dnsRecord `json:"domain_dns,omitempty"`
	// DomainDNSTXT is the ownership record the domain also needs (TXT
	// _simple-host.<domain> = the site's token).
	DomainDNSTXT    *dnsRecord `json:"domain_dns_txt,omitempty"`
	DomainExpiresAt *time.Time `json:"domain_expires_at,omitempty"`
	// DomainCertStatus is the pending domain's certificate (pending, issuing,
	// live, failed) and PreviousDomain the earlier address the site keeps
	// answering at until the new domain is live.
	DomainCertStatus string `json:"domain_certificate_status,omitempty"`
	PreviousDomain   string `json:"previous_domain,omitempty"`
	// DomainPartner is the custom domain's www / bare partner, a
	// redirect-only host (see GET /domain: partner_domain and friends).
	DomainPartner       string     `json:"domain_partner,omitempty"`
	DomainPartnerDNS    *dnsRecord `json:"domain_partner_dns,omitempty"`
	DomainPartnerStatus string     `json:"domain_partner_status,omitempty"`
	DomainPartnerNote   string     `json:"domain_partner_note,omitempty"`
	// DeployedAt is when the newest version went live (site list only).
	DeployedAt    *time.Time `json:"deployed_at,omitempty"`
	Visibility    string     `json:"visibility,omitempty"`
	OwnerUsername string     `json:"owner_username,omitempty"`
	Note          string     `json:"note,omitempty"`
	// Suspended: the operator has taken the site down (by itself or with its
	// owner's account). It keeps everything and refuses changes until restored.
	Suspended       bool   `json:"suspended,omitempty"`
	SuspendedReason string `json:"suspended_reason,omitempty"`
	// Offline: its owner has taken it offline (every address shows "This
	// site is offline", visitor saves are refused; nothing is deleted).
	Offline bool `json:"offline,omitempty"`
	// Only on a deploy with publish=false: the version it stored (not live)
	// and an hour-long, owner-only preview link for it (preview.go).
	UnpublishedVersion int        `json:"unpublished_version,omitempty"`
	PreviewURL         string     `json:"preview_url,omitempty"`
	PreviewExpiresAt   *time.Time `json:"preview_expires_at,omitempty"`
	// AddressState: present while the site is handed out at its interim
	// address (<handle>.<SITE_DOMAIN>/<site>/) because its owner's
	// certificate is not ready yet — waiting or failing, with a rough time.
	AddressState *addressState `json:"address_state,omitempty"`
	// Keep: the owner marked the site to stay up for good; it is never
	// warned or removed as idle. IdleRemovalAt: the site was found idle and
	// its owner emailed; it moves to Recently deleted then unless kept,
	// visited or updated (site list only).
	Keep          bool       `json:"keep,omitempty"`
	IdleRemovalAt *time.Time `json:"idle_removal_at,omitempty"`
}

type versionResponse struct {
	VersionNumber int       `json:"version_number"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	IsActive      bool      `json:"is_active"`
}

func NewSiteHandler(database *sql.DB, disk *storage.DiskStorage, siteDomain, contentHost, cnameTarget, customDomainIP, deployScript, adminAPIKey string, previewAccounts map[string]bool, previewTTL time.Duration, writeAuthMode, adminUserID string, mailer email.Sender, emailLimiter *rateLimiter) *SiteHandler {
	// Uploads: ~6/min/IP, burst 30. State writes: ~1/s/IP sustained, burst 60
	// (a browser app may persist state on each interaction).
	lim := config.Active()
	uploadLimiter := newRateLimiterFor(lim.RateUpload)
	stateLimiter := newRateLimiterFor(lim.RateState)
	visitorAuthLimiter := newRateLimiterFor(lim.RateVisitorAuth)
	domainCheckLimiter := newRateLimiterFor(lim.RateDomainCheck)
	domainCheckLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	domainCheckUserLimiter := newRateLimiterFor(lim.RateDomainCheckUser)
	domainCheckUserLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	visitorAuthLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	uploadLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	stateLimiter.startCleanup(10*time.Minute, 30*time.Minute)

	h := &SiteHandler{
		mailer:                 mailer,
		emailLimiter:           emailLimiter,
		database:               database,
		disk:                   disk,
		siteDomain:             siteDomain,
		contentHost:            contentHost,
		cnameTarget:            cnameTarget,
		customDomainIP:         customDomainIP,
		deployScript:           deployScript,
		uploadLimiter:          uploadLimiter,
		stateLimiter:           stateLimiter,
		visitorAuthLimiter:     visitorAuthLimiter,
		domainCheckLimiter:     domainCheckLimiter,
		domainCheckUserLimiter: domainCheckUserLimiter,
		previewAccounts:        previewAccounts,
		previewTTL:             previewTTL,
		writeAuthMode:          writeAuthMode,
		adminAPIKey:            adminAPIKey,
		adminUserID:            adminUserID,
		exportKey:              newExportKey(),
	}
	h.SetSavedData(config.DefaultSavedData())
	if len(previewAccounts) > 0 {
		ttlHours := int(previewTTL.Hours())
		for u := range previewAccounts {
			if n, err := db.BackfillPreviewExpiry(context.Background(), database, u, ttlHours); err != nil {
				log.Printf("preview backfill %q: %v", u, err)
			} else if n > 0 {
				log.Printf("preview backfill: stamped expiry on %d existing site(s) for %q", n, u)
			}
		}
		h.startExpirySweep(time.Hour)
		log.Printf("preview-site expiry enabled: accounts=%d ttl=%s", len(previewAccounts), previewTTL)
	}
	if customDomainIP != "" {
		h.startDomainChecks(domainCheckInterval())
		log.Printf("custom-domain verification enabled: every %s, active re-proved after %s", domainCheckInterval(), domainActiveAge)
	}
	return h
}

// previewExpiry returns a per-site expiry timestamp when the owner is a
// configured preview account, or nil for a permanent site.
func (h *SiteHandler) previewExpiry(user *db.User) *time.Time {
	if user == nil || len(h.previewAccounts) == 0 || !h.previewAccounts[strings.ToLower(user.Username)] {
		return nil
	}
	t := time.Now().Add(h.previewTTL)
	return &t
}

// startExpirySweep periodically deletes sites whose expires_at has passed
// (DB rows + on-disk files), keeping the platform free of stale preview sites.
func (h *SiteHandler) startExpirySweep(every time.Duration) {
	go func() {
		// Run once shortly after boot, then on the interval.
		time.Sleep(30 * time.Second)
		for {
			h.sweepExpiredSites()
			time.Sleep(every)
		}
	}()
}

func (h *SiteHandler) sweepExpiredSites() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	expired, err := db.ListExpiredSites(ctx, h.database)
	if err != nil {
		log.Printf("expiry sweep: list failed: %v", err)
		return
	}
	for _, s := range expired {
		unlock := h.lockSite(s.UserID, s.Name)
		if err := db.DeleteSite(ctx, h.database, s.ID); err != nil {
			log.Printf("expiry sweep: delete row %s (%s): %v", s.Name, s.ID, err)
			unlock()
			continue
		}
		if err := h.disk.DeleteSite(s.UserID, s.Name); err != nil {
			log.Printf("expiry sweep: delete disk %s: %v", s.Name, err)
		}
		unlock()
		log.Printf("expiry sweep: removed preview site %q", s.Name)
	}
}

func (h *SiteHandler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.createSite)))))
	mux.Handle("PUT /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSite)))))
	// Delete, rename and restore each take per-site locks: rate-limited so a
	// loop cannot pile up locks or disk moves.
	siteOpLimiter := newRateLimiterFor(config.Active().RateSiteOps)
	siteOpLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	mux.Handle("DELETE /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteSite)))))
	mux.Handle("PATCH /v1/sites/{sitename}", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.patchSite)))))
	mux.Handle("POST /v1/sites/{sitename}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.restoreSite)))))
	mux.Handle("GET /v1/me/deleted-sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDeletedSites))))
	mux.Handle("GET /v1/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSites))))
	mux.Handle("POST /v1/admin/users", authMiddleware(http.HandlerFunc(h.createAccounts)))
	mux.Handle("DELETE /v1/admin/users/{id}", authMiddleware(http.HandlerFunc(h.deleteAccount)))
	mux.Handle("POST /v1/admin/users/{id}/key", authMiddleware(http.HandlerFunc(h.reissueAccountKey)))
	mux.Handle("PATCH /v1/me", authMiddleware(http.HandlerFunc(h.patchMe)))
	// Download my data / delete my account and all data (account_data.go).
	mux.Handle("GET /v1/me/export.zip", authMiddleware(http.HandlerFunc(h.exportMe)))
	// The address before the download became a .zip; serves the same zip.
	mux.Handle("GET /v1/me/export.tar.gz", authMiddleware(http.HandlerFunc(h.exportMe)))
	mux.Handle("DELETE /v1/me", authMiddleware(rateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteMe))))
	mux.Handle("GET /v1/admin/users", authMiddleware(http.HandlerFunc(h.adminUsers)))
	// Operator take-down (suspend.go): a site, or a person and all their
	// sites, without deleting anything; restore / enable reverses it.
	mux.Handle("POST /v1/admin/sites/{id}/suspend", authMiddleware(h.setSiteSuspension(true)))
	mux.Handle("POST /v1/admin/sites/{id}/restore", authMiddleware(h.setSiteSuspension(false)))
	mux.Handle("POST /v1/admin/users/{id}/suspend", authMiddleware(h.setUserSuspension(true)))
	mux.Handle("POST /v1/admin/users/{id}/enable", authMiddleware(h.setUserSuspension(false)))
	// Every site on the instance in one archive (export.go).
	mux.Handle("GET /v1/admin/export.tar.gz", authMiddleware(http.HandlerFunc(h.exportAll)))
	// What the disk is actually holding, and how much is left.
	mux.Handle("GET /v1/admin/usage", authMiddleware(http.HandlerFunc(h.adminUsage)))
	// Take your work with you. An event box is destroyed when the event ends and
	// nothing is backed up, so the only honest answer is to make leaving easy.
	mux.Handle("GET /v1/sites/{sitename}/export.tar.gz", authMiddleware(http.HandlerFunc(h.exportSite)))
	// The same export behind a 10-minute signed link, for people with no API
	// key (chat-app connector users): minted by the owner, opened by a click.
	mux.Handle("POST /v1/sites/{sitename}/export-link", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createExportLink))))
	exportLimiter := newRateLimiterFor(config.Active().RateExport)
	exportLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/export", rateLimitByIP(exportLimiter, http.HandlerFunc(h.downloadExport)))
	mux.Handle("GET /v1/sites/{sitename}/versions", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersions))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listVersionFiles))))
	mux.Handle("GET /v1/sites/{sitename}/versions/{version}/files/{path...}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getVersionFile))))
	mux.Handle("PUT /v1/sites/{sitename}/active-version", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setActiveVersion))))
	// An hour-long, owner-only preview address for one kept version (preview.go).
	mux.Handle("POST /v1/sites/{sitename}/versions/{version}/preview-link", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createPreviewLink))))
	mux.Handle("GET /v1/sites/{sitename}/analytics", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteAnalytics))))
	mux.Handle("GET /v1/sites/{sitename}/analytics/geo", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteGeoAnalytics))))
	mux.Handle("GET /v1/sites/{sitename}/analytics/top", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSiteTopAnalytics))))
	// Deliberately not /v1/sites/analytics: that would collide with a site
	// actually named "analytics".
	mux.Handle("GET /v1/analytics/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getAnalyticsSummary))))
	mux.Handle("PUT /v1/sites/{sitename}/visibility", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setVisibility))))
	// Idle-site cleanup (idle.go): the Keep flag, the admin dry run, and the
	// emailed links, which need no sign-in (the token is the authorization).
	mux.Handle("PUT /v1/sites/{sitename}/keep", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setSiteKeep))))
	mux.Handle("GET /v1/admin/idle-sites", authMiddleware(http.HandlerFunc(h.adminIdleSites)))
	idleLinkLimiter := newRateLimiter(10, 0.1)
	idleLinkLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/idle/keep", rateLimitByIP(idleLinkLimiter, http.HandlerFunc(h.idleKeep)))
	mux.Handle("POST /v1/idle/keep", rateLimitByIP(idleLinkLimiter, http.HandlerFunc(h.idleKeep)))
	mux.Handle("GET /v1/idle/restore", rateLimitByIP(idleLinkLimiter, http.HandlerFunc(h.idleRestore)))
	mux.Handle("POST /v1/idle/restore", rateLimitByIP(idleLinkLimiter, http.HandlerFunc(h.idleRestore)))

	// JSON deploy (LLM-friendly): file contents inline, no archive. Same auth +
	// rate-limit chain as the archive upload; CORS preflight is handled by the
	// CORS middleware (these paths do not end in /state).
	mux.Handle("POST /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.createSiteFiles)))))
	mux.Handle("PUT /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(rateLimitByIP(h.uploadLimiter, http.HandlerFunc(h.updateSiteFiles)))))

	// State routes are deliberately NOT wrapped — they serve browser pages
	// that parse the JSON state object directly. Adding _notice would
	// corrupt that contract.
	//
	// TRUST MODEL: site state is PUBLIC per-site scratch storage for reads.
	// A GET with no Origin and no Referer (curl, an agent) is served as is; any
	// request that names a page is Origin-checked (authorizeStateOrigin), on
	// every method. Writes additionally go through visitorWriteOK: a visitor
	// session, the owner's (or admin's) X-API-Key, WRITE_AUTH_MODE=log (measure, allow),
	// or the admin allow_anonymous_writes hatch. Do not store secrets in it.
	// Abuse is bounded by stateLimiter (rate) and maxSiteStateSize (1 MB cap).
	mux.Handle("PUT /v1/sites/{sitename}/allowed-origins", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setAllowedOrigins))))
	mux.Handle("PUT /v1/sites/{sitename}/allow-anonymous-writes", noticeMiddleware(authMiddleware(auth.RequireAdmin(http.HandlerFunc(h.setAllowAnonymousWrites)))))

	// Custom domain (one per site): owner binds a hostname, gets a CNAME record
	// to create; Caddy on-demand TLS asks /internal/tls-ask before issuing a cert.
	mux.Handle("POST /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.bindDomain))))
	mux.Handle("GET /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDomain))))
	mux.Handle("DELETE /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteDomain))))
	// "Check again": re-verify this site's pending domain now instead of
	// waiting for the next background pass.
	mux.Handle("POST /v1/sites/{sitename}/domain/check", noticeMiddleware(authMiddleware(rateLimitByIP(h.domainCheckLimiter, http.HandlerFunc(h.checkDomainNow)))))
	mux.HandleFunc("GET /internal/tls-ask", h.tlsAsk)
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
	mux.HandleFunc("GET /internal/suspended", h.suspendedPage)
	// ... and for a site its owner took offline (offline.go).
	mux.HandleFunc("GET /internal/offline", h.offlinePageHandler)

	// Append-only collections (second backend type): cheap O(1) appends +
	// paginated reads for large/high-volume lists. Origin-gated like state.
	// Owner-authenticated list + CSV export sit next to
	// them so a site owner can see (and download) what the site saved.
	mux.Handle("GET /v1/sites/{sitename}/collections", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSiteCollections))))
	mux.Handle("GET /v1/sites/{sitename}/collections/{coll}/export.csv", authMiddleware(http.HandlerFunc(h.exportCollectionCSV)))
	// Owner-only: make one list private (owner-only reads, signed-in
	// submissions on the site's own domain) or public again.
	mux.Handle("PUT /v1/sites/{sitename}/collections/{coll}/privacy", noticeMiddleware(authMiddleware(http.HandlerFunc(h.setCollectionPrivacy))))
	// The owner or the platform admin (key, connector token, or the owner's
	// session on the site's own domain) deletes one item in any list, edits
	// one in a private list, or empties a whole list. Visitors only append.
	mux.Handle("PATCH /v1/sites/{sitename}/collections/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.updatePrivateItem)))
	mux.Handle("DELETE /v1/sites/{sitename}/collections/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.deletePrivateItem)))
	mux.Handle("PATCH /v1/u/{handle}/sites/{sitename}/collections/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.updatePrivateItem)))
	mux.Handle("DELETE /v1/u/{handle}/sites/{sitename}/collections/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.deletePrivateItem)))
	mux.Handle("DELETE /v1/sites/{sitename}/collections/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.clearCollection)))
	mux.Handle("DELETE /v1/u/{handle}/sites/{sitename}/collections/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.clearCollection)))
	// History, Recently deleted, restore and delete for good (saveddata.go):
	// owner or admin only, key or connector token; the {handle} forms name
	// the site exactly.
	mux.Handle("GET /v1/sites/{sitename}/collections/{coll}/history", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDataHistory))))
	mux.Handle("GET /v1/sites/{sitename}/collections/{coll}/history/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDataHistory))))
	mux.Handle("POST /v1/sites/{sitename}/collections/{coll}/history/{id}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreListHistory)))))
	mux.Handle("GET /v1/sites/{sitename}/collections/{coll}/deleted", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDeletedItems))))
	mux.Handle("POST /v1/sites/{sitename}/collections/{coll}/deleted/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreDeletedItem)))))
	mux.Handle("POST /v1/sites/{sitename}/collections/{coll}/items/{id}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreDeletedItem)))))
	mux.Handle("DELETE /v1/sites/{sitename}/collections/{coll}/deleted", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.purgeDeletedItems)))))
	mux.Handle("DELETE /v1/sites/{sitename}/collections/{coll}/deleted/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.purgeDeletedItems)))))
	mux.Handle("GET /v1/sites/{sitename}/state/history", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDataHistory))))
	mux.Handle("GET /v1/sites/{sitename}/state/history/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDataHistory))))
	mux.Handle("POST /v1/sites/{sitename}/state/history/{id}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreStateHistory)))))
	mux.Handle("DELETE /v1/sites/{sitename}/history", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.clearDataHistory)))))
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/collections/{coll}/history", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDataHistory))))
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/collections/{coll}/history/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDataHistory))))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/collections/{coll}/history/{id}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreListHistory)))))
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDeletedItems))))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreDeletedItem)))))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/collections/{coll}/items/{id}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreDeletedItem)))))
	mux.Handle("DELETE /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.purgeDeletedItems)))))
	mux.Handle("DELETE /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted/{id}", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.purgeDeletedItems)))))
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/state/history", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listDataHistory))))
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/state/history/{id}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDataHistory))))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/state/history/{id}/restore", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.restoreStateHistory)))))
	mux.Handle("DELETE /v1/u/{handle}/sites/{sitename}/history", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.clearDataHistory)))))
	// The saved-data watch: what the later tightening would affect.
	mux.Handle("GET /v1/admin/data-watch", authMiddleware(auth.RequireAdmin(http.HandlerFunc(h.adminDataWatch))))
	// Kinds (kinds.go): the owner declares what each data name is and who may
	// save; pages read Page info and add, list, change and withdraw their own
	// Submissions. The page-facing routes run the same Origin policy as the
	// collections; the owner's use the key or connector token.
	mux.Handle("GET /v1/sites/{sitename}/data", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listData))))
	mux.Handle("PUT /v1/sites/{sitename}/data/{coll}/kind", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.declareData)))))
	mux.Handle("GET /v1/sites/{sitename}/savers", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getSavers))))
	mux.Handle("PUT /v1/sites/{sitename}/savers", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.putSavers)))))
	mux.Handle("POST /v1/sites/{sitename}/savers/block", noticeMiddleware(authMiddleware(rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.blockSaver)))))
	stopLimiter := newRateLimiter(10, 0.1)
	stopLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/data-notify/stop", rateLimitByIP(stopLimiter, http.HandlerFunc(h.notifyStop)))
	mux.Handle("POST /v1/data-notify/stop", rateLimitByIP(stopLimiter, http.HandlerFunc(h.notifyStop)))
	mux.Handle("GET /v1/sites/{sitename}/data/{coll}", http.HandlerFunc(h.getData))
	mux.Handle("GET /v1/sites/{sitename}/data/{coll}/kind", http.HandlerFunc(h.getDataKind))
	mux.Handle("POST /v1/sites/{sitename}/data/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.appendCollection)))
	mux.Handle("PUT /v1/sites/{sitename}/data/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.putContent)))
	mux.Handle("PATCH /v1/sites/{sitename}/data/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.updateEntry)))
	mux.Handle("DELETE /v1/sites/{sitename}/data/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.withdrawEntry)))
	mux.Handle("POST /v1/sites/{sitename}/data/{coll}/items/{id}/undo", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.undoWithdraw)))
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/data/{coll}", h.optionsData)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/data/{coll}/kind", h.optionsData)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/data/{coll}/items/{id}", h.optionsData)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/data/{coll}/items/{id}/undo", h.optionsData)
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/data/{coll}", http.HandlerFunc(h.getData))
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/data/{coll}/kind", http.HandlerFunc(h.getDataKind))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/data/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.appendCollection)))
	mux.Handle("PUT /v1/u/{handle}/sites/{sitename}/data/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.putContent)))
	mux.Handle("PATCH /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.updateEntry)))
	mux.Handle("DELETE /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.withdrawEntry)))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}/undo", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.undoWithdraw)))
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/data/{coll}", h.optionsData)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/data/{coll}/kind", h.optionsData)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}", h.optionsData)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}/undo", h.optionsData)
	mux.Handle("GET /v1/sites/{sitename}/collections/{coll}", http.HandlerFunc(h.listCollection))
	mux.Handle("POST /v1/sites/{sitename}/collections/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.appendCollection)))
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/collections/{coll}", h.optionsCollection)

	mux.HandleFunc("GET /v1/sites/{sitename}/me", h.getVisitorMe)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/me", h.optionsVisitorMe)
	mux.HandleFunc("POST /v1/sites/{sitename}/visitor/auth", h.requestVisitorEmail)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/visitor/auth", h.optionsVisitorEmail)
	mux.HandleFunc("POST /v1/sites/{sitename}/visitor/auth/verify", h.verifyVisitorEmail)
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/visitor/auth/verify", h.optionsVisitorEmail)
	mux.Handle("GET /v1/sites/{sitename}/state", http.HandlerFunc(h.getSiteState))
	mux.Handle("PUT /v1/sites/{sitename}/state", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.putSiteState)))
	mux.Handle("PATCH /v1/sites/{sitename}/state", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.patchSiteState)))
	mux.HandleFunc("OPTIONS /v1/sites/{sitename}/state", h.optionsSiteState)

	// v3 user-scoped state/collections: unambiguous after UNIQUE(name) drops.
	// Same handlers as above; resolveSiteID reads {handle} when present.
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/collections/{coll}", http.HandlerFunc(h.listCollection))
	mux.Handle("POST /v1/u/{handle}/sites/{sitename}/collections/{coll}", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.appendCollection)))
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/collections/{coll}", h.optionsCollection)

	mux.HandleFunc("GET /v1/u/{handle}/sites/{sitename}/me", h.getVisitorMe)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/me", h.optionsVisitorMe)
	mux.HandleFunc("POST /v1/u/{handle}/sites/{sitename}/visitor/auth", h.requestVisitorEmail)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/visitor/auth", h.optionsVisitorEmail)
	mux.HandleFunc("POST /v1/u/{handle}/sites/{sitename}/visitor/auth/verify", h.verifyVisitorEmail)
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/visitor/auth/verify", h.optionsVisitorEmail)
	mux.Handle("GET /v1/u/{handle}/sites/{sitename}/state", http.HandlerFunc(h.getSiteState))
	mux.Handle("PUT /v1/u/{handle}/sites/{sitename}/state", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.putSiteState)))
	mux.Handle("PATCH /v1/u/{handle}/sites/{sitename}/state", rateLimitByIP(h.stateLimiter, http.HandlerFunc(h.patchSiteState)))
	mux.HandleFunc("OPTIONS /v1/u/{handle}/sites/{sitename}/state", h.optionsSiteState)

	// Visitor session cookie is issued here (content host / custom domain),
	// never on the apex OAuth callback.
	visitorLimiter := newRateLimiterFor(config.Active().RateVisitor)
	visitorLimiter.startCleanup(10*time.Minute, 30*time.Minute)
	mux.Handle("GET /v1/visitor/establish", rateLimitByIP(visitorLimiter, http.HandlerFunc(h.establishVisitor)))
	mux.Handle("POST /v1/visitor/logout", rateLimitByIP(visitorLimiter, http.HandlerFunc(h.logoutVisitor)))
}

// renameSite renames the caller's site oldName to newName (PATCH with
// {"name": ...}, see patchSite).
func (h *SiteHandler) renameSite(w http.ResponseWriter, r *http.Request, oldName, newName string) {
	user := auth.GetUser(r.Context())
	if err := validateSiteShape(newName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_name"})
		return
	}
	if err := validateSiteReserved(newName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "name_reserved"})
		return
	}
	if oldName == newName {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "new site name must be different"})
		return
	}
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, oldName); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	first, second := oldName, newName
	if second < first {
		first, second = second, first
	}
	unlockFirst := h.lockSite(user.ID, first)
	defer unlockFirst()
	unlockSecond := h.lockSite(user.ID, second)
	defer unlockSecond()

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, oldName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, newName); err == nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "you already have a site with that name", Code: "site_exists"})
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if msg, held := h.deletedNameConflict(r.Context(), user.ID, newName); held {
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "code": "recently_deleted", "recently_deleted": true})
		return
	}
	oldURL := h.siteURLFor(site)
	newURL := h.SiteURL(user.Handle.String, newName)
	if newURL == "" {
		newURL = fmt.Sprintf("https://%s/%s/%s/", h.contentHost, user.Handle.String, newName)
	}
	domain := ""
	if site.CustomDomain.Valid {
		domain = site.CustomDomain.String
	}
	// The earlier address a site keeps serving while a new domain is pending
	// has its own link, which must follow the rename too.
	domains := []string{domain}
	if info, ok, err := db.GetSiteDomainInfo(r.Context(), h.database, site.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	} else if ok && info.PreviousDomain != "" {
		domains = append(domains, info.PreviousDomain)
	}
	if err := h.disk.RenameSite(user.ID, oldName, newName, domains...); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not move site files"})
		return
	}
	// The row and the old name go together: links to the old name redirect
	// to the new address from the moment the rename is visible.
	err = func() error {
		tx, err := h.database.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := db.RenameSite(r.Context(), tx, site.ID, newName, newURL); err != nil {
			return err
		}
		if err := db.KeepOldSiteName(r.Context(), tx, user.ID, site.ID, oldName, newName); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		_ = h.disk.RenameSite(user.ID, newName, oldName, domains...)
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "you already have a site with that name", Code: "site_exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": site.ID, "old_name": oldName, "name": newName,
		"old_url": oldURL, "site_url": newURL, "old_url_status": "redirects",
		"message":                 "Site renamed. Links to the old address redirect to the new one until a new site takes the old name.",
		"custom_domain_unchanged": domain != "",
	})
}

// setAllowedOrigins lets a site owner list extra origins (scheme://host) that may
// call the site's state/collections API cross-origin — so a page hosted anywhere
// (external hosting, Netlify, …) can use this site as its backend. Owner-only.
func (h *SiteHandler) setAllowedOrigins(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}

	var req struct {
		Origins []string `json:"origins"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body (expected {\"origins\":[\"https://you.github.io\"]})"})
		return
	}
	clean := make([]string, 0, len(req.Origins))
	for _, o := range req.Origins {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" || (u.Path != "" && u.Path != "/") {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "each origin must be scheme://host with no path, e.g. https://you.github.io"})
			return
		}
		clean = append(clean, u.Scheme+"://"+u.Host)
	}
	if len(clean) > 20 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "too many origins (max 20)"})
		return
	}
	if err := db.SetAllowedOrigins(r.Context(), h.database, site.ID, strings.Join(clean, ",")); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "allowed_origins": clean})
}

// setAllowAnonymousWrites is the admin-only hatch so one site can keep
// accepting unsigned writes while WRITE_AUTH_MODE=on.
func (h *SiteHandler) setAllowAnonymousWrites(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	var req struct {
		Allow *bool `json:"allow"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Allow == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	// ?owner=<handle> names the exact site; without it, the oldest site of
	// that name (the legacy bare-name lookup).
	var siteID string
	var err error
	if owner := strings.TrimSpace(r.URL.Query().Get("owner")); owner != "" {
		var u db.User
		if u, err = db.GetUserByHandleOrAlias(r.Context(), h.database, owner); err == nil {
			var s db.Site
			if s, err = db.GetSiteByUser(r.Context(), h.database, u.ID, siteName); err == nil {
				siteID = s.ID
			}
		}
	} else {
		siteID, err = db.GetSiteIDByName(r.Context(), h.database, siteName)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := db.SetAllowAnonymousWrites(r.Context(), h.database, siteID, *req.Allow); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "allow_anonymous_writes": *req.Allow})
}

// resolveSiteID resolves the target site's id. When the route carries a {handle} path
// value (the v3 user-scoped routes), it resolves handle->user_id then (user_id,name)->site
// so the lookup is unambiguous even after UNIQUE(name) is dropped; an old handle kept as
// an alias still resolves. The request's host binds the lookup: on a person host
// (<handle>.<SITE_DOMAIN>) only that person's sites exist, and on a site's own domain
// only that site does. Elsewhere (apex, content host) a bare name falls back to the
// legacy global lookup. Returns sql.ErrNoRows if not found (caller maps to 404).
func (h *SiteHandler) resolveSiteID(r *http.Request, siteName string) (string, error) {
	id, _, err := h.resolveSiteIDBare(r, siteName)
	return id, err
}

// resolveWriteSiteID is resolveSiteID for the page-data write routes (state
// PUT/PATCH, collection append). A bare name on the API or shared host
// otherwise means the oldest same-named site; when the caller's key owns a
// site of that name, it means theirs. Everything else is unchanged, and the
// write gate (visitorWriteOK) still decides whether the caller may write.
func (h *SiteHandler) resolveWriteSiteID(r *http.Request, siteName string) (string, error) {
	id, bare, err := h.resolveSiteIDBare(r, siteName)
	key := r.Header.Get("X-API-Key")
	if !bare || key == "" {
		return id, err
	}
	u, ok, kerr := h.resolveWriterKey(r.Context(), key)
	if kerr != nil || !ok || u.ID == "" {
		return id, err
	}
	if s, serr := db.GetSiteByUser(r.Context(), h.database, u.ID, siteName); serr == nil {
		return s.ID, nil
	}
	return id, err
}

// resolveSiteIDBare resolves like resolveSiteID and reports whether the name
// was looked up bare (no handle, no site/person host, no own domain).
func (h *SiteHandler) resolveSiteIDBare(r *http.Request, siteName string) (string, bool, error) {
	id, err := h.resolveSiteIDScoped(r, siteName)
	if !errors.Is(err, errBareSiteName) {
		return id, false, err
	}
	id, err = db.GetSiteIDByName(r.Context(), h.database, siteName)
	return id, true, err
}

var errBareSiteName = errors.New("bare site name")

func (h *SiteHandler) resolveSiteIDScoped(r *http.Request, siteName string) (string, error) {
	host := requestHostName(r)
	handle := strings.TrimSpace(r.PathValue("handle"))
	// A site host (<site>.<handle>.<SITE_DOMAIN>) is one site's own origin:
	// only that site resolves there, under its name (what auth.js derives
	// from the first host label) or its owner's handle route. Any other name
	// — another site of the same person included — does not exist there.
	if h.isSiteHostName(host) {
		site, owner, ok, err := h.siteHostSite(r.Context(), host)
		if err != nil {
			return "", err
		}
		if !ok || site.Name != siteName {
			return "", sql.ErrNoRows
		}
		if handle != "" {
			u, err := db.GetUserByHandleOrAlias(r.Context(), h.database, handle)
			if err != nil {
				return "", err
			}
			if u.ID != owner.ID {
				return "", sql.ErrNoRows
			}
		}
		return site.ID, nil
	}
	owner, onPerson := h.personHostOwner(r.Context(), host)
	if handle != "" {
		u, err := db.GetUserByHandleOrAlias(r.Context(), h.database, handle)
		if err != nil {
			return "", err
		}
		if onPerson && u.ID != owner.ID {
			return "", sql.ErrNoRows
		}
		s, err := db.GetSiteByUser(r.Context(), h.database, u.ID, siteName)
		if err != nil {
			return "", err
		}
		return s.ID, nil
	}
	if onPerson {
		s, err := db.GetSiteByUser(r.Context(), h.database, owner.ID, siteName)
		if err != nil {
			return "", err
		}
		return s.ID, nil
	}
	// On a site's own domain (custom or a claimed <name>.<SITE_DOMAIN>) the
	// host names the site: /v1/sites/<its name> there is that site (as is the
	// claimed name itself, which is what auth.js derives from the host), and
	// no other name resolves there.
	if host != "" && !strings.EqualFold(host, h.contentHost) && !h.isVisitorApexHost(host) {
		if info, err := db.GetSiteByCustomDomain(r.Context(), h.database, host); err == nil {
			label, isPlatform := platformSubdomainLabel(host, h.siteDomain)
			if info.Name == siteName || (isPlatform && label == siteName) {
				return info.SiteID, nil
			}
			return "", sql.ErrNoRows
		}
	}
	return "", errBareSiteName
}

// originAllowedForSiteID reports whether the owner has whitelisted this exact
// origin (scheme://host) for the given site_id's state/collections API.
func (h *SiteHandler) originAllowedForSiteID(ctx context.Context, siteID, origin string) bool {
	origins, err := db.GetAllowedOriginsByID(ctx, h.database, siteID)
	if err != nil || len(origins) == 0 {
		return false
	}
	o := strings.ToLower(strings.TrimRight(origin, "/"))
	for _, a := range origins {
		if strings.ToLower(strings.TrimRight(a, "/")) == o {
			return true
		}
	}
	return false
}

// originIsBoundDomainID reports whether host is this site's own custom_domain
// (same-origin state on a connected domain). Keyed by site_id so a domain bound
// to one same-named site cannot authorize writes to another.
func (h *SiteHandler) originIsBoundDomainID(ctx context.Context, siteID, host string) bool {
	info, ok, err := db.GetSiteDomainInfo(ctx, h.database, siteID)
	if err != nil || !ok {
		return false
	}
	// Also the earlier address it still serves at while a new one is pending.
	return strings.EqualFold(info.Domain, host) || (info.PreviousDomain != "" && strings.EqualFold(info.PreviousDomain, host))
}

// originIsPersonHostID reports whether host is the person address of the
// site's owner: a page served there may use the site's API from anywhere
// (the apex, the content host, or its own host), like a bound domain.
func (h *SiteHandler) originIsPersonHostID(ctx context.Context, siteID, host string) bool {
	if !h.personHostsOn() {
		return false
	}
	handle, _, name, err := db.GetSiteOwner(ctx, h.database, siteID)
	if err != nil || !h.personAddressFor(handle, name) {
		return false
	}
	return strings.EqualFold(host, h.personHostFor(handle))
}

// originIsSiteHostID reports whether host is the site's own site host
// (<site>.<handle>.<SITE_DOMAIN>) while that host serves it.
func (h *SiteHandler) originIsSiteHostID(ctx context.Context, siteID, host string) bool {
	if !h.siteHostsOn() || !h.isSiteHostName(host) {
		return false
	}
	handle, _, name, err := db.GetSiteOwner(ctx, h.database, siteID)
	if err != nil || !h.siteHostLive(handle, name) {
		return false
	}
	return strings.EqualFold(host, h.siteHostFor(handle, name))
}

// authorizeStateOrigin checks Origin/Referer and, on a match, sets the CORS
// headers that allow the calling site to read the response. Returns true if
// the request is allowed. The gate is keyed to the same site_id that the data
// handlers resolve (via resolveSiteID), so two same-named sites cannot widen
// each other's allowed_origins or bound custom_domain.
func (h *SiteHandler) authorizeStateOrigin(w http.ResponseWriter, r *http.Request, siteName string) bool {
	// Resolve once; if we can't attribute the request to a real site, deny.
	// Key-aware, so the origin is checked against the same site a keyed
	// write or read targets (the caller's own same-named site on a bare name).
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		if ref := r.Header.Get("Referer"); ref != "" {
			if u, err := url.Parse(ref); err == nil {
				origin = u.Scheme + "://" + u.Host
			}
		}
	}

	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	// Accept the shared v3 content host (sites.<SITE_DOMAIN> — all path-model
	// pages share this Origin), this site's owner's person address, this
	// site's own bound custom domain, OR any origin the owner has explicitly
	// allowed for THIS site_id. A browser cannot forge Origin, so this is the
	// same attribution-grade gate, just widened to owner-approved origins, the
	// co-tenant content host, and the site's own hosts. (The retired
	// <name>.<SITE_DOMAIN> host only redirects, so it serves no page to trust.)
	if !strings.EqualFold(parsed.Host, h.contentHost) &&
		!h.originIsSiteHostID(r.Context(), siteID, parsed.Host) &&
		!h.originIsPersonHostID(r.Context(), siteID, parsed.Host) &&
		!h.originIsBoundDomainID(r.Context(), siteID, parsed.Host) &&
		!h.originAllowedForSiteID(r.Context(), siteID, origin) {
		return false
	}

	w.Header().Set("Access-Control-Allow-Origin", origin)
	// Allow credentials so the page can send its view-session cookie on locked
	// sites (ACAO is the specific origin above, never "*", as credentials require).
	w.Header().Set("Access-Control-Allow-Credentials", "true")
	// Expose ETag so the page's JS can read the state version for optimistic
	// concurrency (ETag is not a CORS-safelisted response header).
	w.Header().Set("Access-Control-Expose-Headers", "ETag")
	w.Header().Set("Vary", "Origin")
	return true
}

// noBrowserOrigin reports whether r carries neither Origin nor Referer: a
// script, curl or an agent rather than a page. Saved state and public lists
// are public to read, so such a read needs no Origin check; there is no
// calling page to hand CORS headers to, and nothing it could not already see.
func noBrowserOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") == "" && r.Header.Get("Referer") == ""
}

// authorizePublicRead gates a public read (GET state, GET a public list): with
// no Origin or Referer it is allowed outright; a browser read from a page still
// needs that page to be one of the site's own origins, exactly like a write.
func (h *SiteHandler) authorizePublicRead(w http.ResponseWriter, r *http.Request, siteName string) bool {
	if noBrowserOrigin(r) {
		return true
	}
	return h.authorizeStateOrigin(w, r, siteName)
}

func (h *SiteHandler) optionsSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, PATCH, OPTIONS")
	// If-Match / If-None-Match carry the state version for optimistic-concurrency
	// PUTs and conditional GETs; PATCH sends ops as JSON. X-SH-CSRF is required
	// on cookie-authenticated writes.
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match, If-None-Match, X-SH-CSRF, Idempotency-Key")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

func (h *SiteHandler) getSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	// The owner's (or admin's) key reads its own site's state from any page,
	// as it reads a list: that is how the owner app shows saved data. It also
	// pins the site to the key's owner rather than the oldest same name.
	siteID, ownerKey := h.ownerSiteIDFromKey(r, siteName)
	if !ownerKey && !h.authorizePublicRead(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	// Resolve name -> site_id once; all subsequent state ops key by id.
	var err error
	if !ownerKey {
		siteID, err = h.resolveSiteID(r, siteName)
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !h.allowRead(w, r, siteID) {
		return
	}
	// A taken-down site serves nothing to the public; the owner's (or the
	// admin's) key still reads it.
	if _, owner := h.ownerSiteIDFromKey(r, siteName); !owner && (h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID)) {
		return
	}

	// Conditional GET: if the caller already has the current version, do a cheap
	// version-only check and return 304 — no fetch/serialize of the document.
	// Makes pollers (e.g. the feedback overlay) nearly free on CPU.
	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" {
		if expected, ok := parseIfMatch(inm); ok {
			ver, err := db.GetSiteStateVersionByID(r.Context(), h.database, siteID)
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return
			}
			if err == nil && ver == expected {
				w.Header().Set("ETag", stateETag(ver))
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	state, version, err := db.GetSiteStateByID(r.Context(), h.database, siteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(version))
	w.WriteHeader(http.StatusOK)
	w.Write(state)
}

// stateETag formats a state version as a (strong) ETag value.
func stateETag(version int) string {
	return `"` + strconv.Itoa(version) + `"`
}

// parseIfMatch parses an If-Match header value (e.g. `"7"` or `W/"7"`) into the
// expected version. Returns ok=false if it isn't a simple version token.
func parseIfMatch(v string) (int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "W/")
	v = strings.Trim(v, `"`)
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (h *SiteHandler) putSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}

	// Resolve name -> site_id once; all subsequent state ops key by id.
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	actor, ok := h.visitorWriteOK(w, r, siteID, siteName, writeRouteStatePut, "")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSiteStateSize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	if !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json"})
		return
	}

	state := json.RawMessage(body)

	// Optimistic concurrency is OPT-IN: if the caller sends If-Match, we only
	// write when the stored version matches (compare-and-swap). Without it,
	// behavior is the historical last-write-wins. Either way the document
	// from before goes to the site's history (undo).
	expected := -1
	if ifMatch := r.Header.Get("If-Match"); strings.TrimSpace(ifMatch) != "" {
		v, ok := parseIfMatch(ifMatch)
		if !ok {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid If-Match header"})
			return
		}
		expected = v
	}
	// A replace that grows the site past SAVED_DATA_SITE_MAX_MB is refused
	// (site_full); one that does not grow it always goes through.
	newVersion, err := db.WriteSiteState(r.Context(), h.database, siteID, state, expected, db.OpReplace, h.withAuthorEmail(r.Context(), actor), h.siteMaxBytes())
	h.boundHistory(r, siteID)
	if errors.Is(err, db.ErrSiteFull) {
		h.writeSiteFull(w)
		return
	}
	if errors.Is(err, db.ErrStateVersionConflict) {
		// Hand back the current version so the client can re-read and retry.
		if _, cur, gerr := db.GetSiteStateByID(r.Context(), h.database, siteID); gerr == nil {
			w.Header().Set("ETag", stateETag(cur))
		}
		writeJSON(w, http.StatusPreconditionFailed, errorResponse{Error: "state version conflict — re-read and retry"})
		return
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// The watch: whole-document replaces not made by the owner, and
	// documents that are not an object, before either is refused.
	if isVisitorActor(actor) {
		h.watch(r.Context(), siteID, watchPutByVisitor, 1)
	}
	if t := bytes.TrimSpace(body); len(t) == 0 || t[0] != '{' {
		h.watch(r.Context(), siteID, watchPutNotObject, 1)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(newVersion))
	w.WriteHeader(http.StatusOK)
	w.Write(state)
}

func (h *SiteHandler) createSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_name"})
		return
	}
	// Reserved-name check is create-only: existing sites must always remain
	// re-deployable even if a name later lands on the reserved list.
	if err := validateSiteReserved(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "name_reserved"})
		return
	}

	if !h.newSiteChecks(w, r, user) {
		return
	}

	files, archiveSHA, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitNewSite(w, r, user, siteName, files, archiveSHA)
}

// commitNewSite writes a brand-new site's first version and promotes it after
// the DB commit. Shared by the archive upload (createSite) and the JSON upload
// (createSiteFiles) so both inherit identical versioning, locking, commit-then-
// promote ordering, and deploy-queue behavior.
func (h *SiteHandler) commitNewSite(w http.ResponseWriter, r *http.Request, user *db.User, siteName string, files map[string][]byte, archiveSHA string) {
	// A guest-created users row has a NULL handle until owner-intent. First
	// deploy is owner-intent: assign before building the path-model site URL.
	if user != nil && (!user.Handle.Valid || user.Handle.String == "") {
		assignHandle(r.Context(), h.database, user.ID, user.Username)
		if refetched, err := db.GetUserByUsername(r.Context(), h.database, user.Username); err == nil {
			*user = refetched
		}
	}

	// Serialize all write+promote activity for this site so concurrent uploads
	// cannot race on version numbers or the `current` swap.
	unlock := h.lockSite(user.ID, siteName)
	defer unlock()

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	// Rename and first deploy must agree on the handle before constructing a URL.
	if err := tx.QueryRowContext(r.Context(), "SELECT handle FROM users WHERE id = $1 FOR UPDATE", user.ID).Scan(&user.Handle); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Stored for the record only: the address answered is computed on read
	// (siteURLFor), so it follows PERSON_HOSTS.
	var siteURL string
	if user.Handle.Valid && user.Handle.String != "" {
		siteURL = fmt.Sprintf("https://%s/%s/%s/", h.contentHost, user.Handle.String, siteName)
	} else {
		siteURL = fmt.Sprintf("https://%s.%s", siteName, h.siteDomain)
	}
	site, err := db.CreateSite(r.Context(), tx, user.ID, siteName, siteURL, h.previewExpiry(user))
	if err != nil {
		if isUniqueViolation(err) {
			tx.Rollback()
			if msg, held := h.deletedNameConflict(r.Context(), user.ID, siteName); held {
				writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "code": "recently_deleted", "recently_deleted": true})
				return
			}
			writeJSON(w, http.StatusConflict, errorResponse{Error: "site already exists: use PUT to update it (PUT /v1/sites/" + siteName + " or PUT /v1/sites/" + siteName + "/files); add ?create=1 to a PUT to create or update in one call", Code: "site_exists"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// A renamed site's old name is free for a new site, which wins: links to
	// the name stop following the renamed one.
	if err := db.DropOldSiteName(r.Context(), tx, user.ID, siteName); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	const versionNumber = 1
	diskPath := fmt.Sprintf("by-id/%s/%s/v%d/", user.ID, siteName, versionNumber)

	version, err := db.CreateVersion(r.Context(), tx, site.ID, versionNumber, diskPath, archiveSHA)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Write the new version dir (not yet live) before committing.
	if err := h.disk.WriteFiles(r.Context(), user.ID, siteName, versionNumber, files); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.ActivateVersion(r.Context(), tx, version.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Promote only AFTER the DB is durable: a commit failure leaves `current`
	// untouched (pointing at the last good version) rather than half-swapped.
	if err := h.disk.UpdateCurrent(user.ID, siteName, versionNumber); err != nil {
		log.Printf("createSite: promote %s v%d after commit: %v", siteName, versionNumber, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// Ensure handles/<handle> -> by-id/<user_id> so the content-host path URL
	// (sites.<domain>/<handle>/<site>/) resolves — critical for a new user's
	// first deploy. Non-fatal: the legacy subdomain still works if this fails.
	if user.Handle.Valid && user.Handle.String != "" {
		if err := h.disk.EnsureHandleLink(user.Handle.String, user.ID); err != nil {
			log.Printf("createSite: ensure handle link %s: %v", user.Handle.String, err)
		}
		h.RequestSiteCert(user.Handle.String)
	}

	site.ActiveVersion = versionNumber
	site.OwnerHandle = user.Handle.String

	// Queue for cortex-share registration (processed by deploy-watcher)
	if h.deployScript != "" {
		queueFile := h.disk.DataDir() + "/.deploy-queue"
		if err := appendToFile(queueFile, siteName); err != nil {
			log.Printf("failed to queue deploy for %s: %v", siteName, err)
		}
	}

	writeJSON(w, http.StatusCreated, h.toSiteResponse(site, ""))
}

func (h *SiteHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	// Charset/shape only on update — never the reserved-name denylist, so an
	// existing site is always re-deployable.
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_name"})
		return
	}

	publish, ok := publishParam(w, r)
	if !ok {
		return
	}
	create, ok := h.createIfMissing(w, r, user, siteName)
	if !ok {
		return
	}
	files, archiveSHA, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	if create {
		h.commitNewSite(w, r, user, siteName, files, archiveSHA)
		return
	}
	h.commitSiteUpdate(w, r, user, siteName, files, archiveSHA, publish)
}

// commitSiteUpdate appends a new version to an existing owned site and promotes
// it after commit. Shared by the archive upload (updateSite) and the JSON upload
// (updateSiteFiles).
//
// publish=false stores the version without making it live (see preview.go):
// `current`, active_version and what visitors see stay as they are, and the
// answer names the new version and a preview link for it.
func (h *SiteHandler) commitSiteUpdate(w http.ResponseWriter, r *http.Request, user *db.User, siteName string, files map[string][]byte, archiveSHA string, publish bool) {
	// Serialize write+promote for this site (in-process), and read the site
	// only once the lock is held: a delete or rename that finished while this
	// upload waited must not be undone by a stale copy. The DB row lock below
	// makes version allocation safe across instances and refuses a site that
	// was deleted meanwhile.
	unlock := h.lockSite(user.ID, siteName)
	defer unlock()

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found: create it with POST, or add ?create=1 to this PUT to create it when missing", Code: "not_found"})
			return
		}

		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	if err := db.LockSiteForUpdate(r.Context(), tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	maxVersion, err := db.GetMaxVersionNumber(r.Context(), tx, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	versionNumber := maxVersion + 1
	diskPath := fmt.Sprintf("by-id/%s/%s/v%d/", site.UserID, siteName, versionNumber)

	version, err := db.CreateVersion(r.Context(), tx, site.ID, versionNumber, diskPath, archiveSHA)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Write the new version dir (not yet live) before committing.
	if err := h.disk.WriteFiles(r.Context(), site.UserID, siteName, versionNumber, files); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if !publish {
		if err := db.MarkVersionReady(r.Context(), tx, version.ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if err := tx.Commit(); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		h.pruneVersions(r.Context(), site.ID, site.UserID, siteName, site.ActiveVersion)
		h.writeUnpublished(w, site, versionNumber)
		return
	}

	if err := db.ActivateVersion(r.Context(), tx, version.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, versionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Promote only AFTER commit: if the DB never committed, `current` still
	// points at the previous good version instead of a half-applied swap.
	if err := h.disk.UpdateCurrent(site.UserID, siteName, versionNumber); err != nil {
		log.Printf("updateSite: promote %s v%d after commit: %v", siteName, versionNumber, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if user.Handle.Valid && user.Handle.String != "" {
		if err := h.disk.EnsureHandleLink(user.Handle.String, user.ID); err != nil {
			log.Printf("updateSite: ensure handle link %s: %v", user.Handle.String, err)
		}
		h.RequestSiteCert(user.Handle.String)
	}

	// Retention runs last, on the request context, and only ever removes
	// history. A failure here is logged and nothing else: the deploy is already
	// committed, promoted and live.
	h.pruneVersions(r.Context(), site.ID, site.UserID, siteName, versionNumber)

	site.ActiveVersion = versionNumber
	writeJSON(w, http.StatusOK, h.toSiteResponse(site, ""))
}

// filesRequest is the JSON deploy body: a map of relative path -> file contents.
// This is the LLM-friendly path — a web LLM can emit one copy-paste request with
// the file contents inline, no archiving step. For binary assets or large sites,
// use the archive upload (POST/PUT /v1/sites/{name}).
type filesRequest struct {
	Files       map[string]string `json:"files"`
	FilesBase64 map[string]string `json:"files_base64"`
}

// readJSONFiles decodes a {"files":{path:contents}} body and runs it through the
// SAME path/secret/size guards as archive extraction (tarball.SanitizeFiles +
// ValidateExtensions). Returns the file map and a content digest for the version
// row. On any error it has already written the HTTP response.
func (h *SiteHandler) readJSONFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteArchiveSize)

	var req filesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return nil, "", err
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return nil, "", err
	}
	if len(req.Files) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "files object is required and must be non-empty"})
		return nil, "", errors.New("empty files")
	}

	raw := make(map[string][]byte, len(req.Files)+len(req.FilesBase64))
	for path, contents := range req.Files {
		raw[path] = []byte(contents)
	}
	for path, contents := range req.FilesBase64 {
		if _, exists := req.Files[path]; exists {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("path %q is present in both files and files_base64", path)})
			return nil, "", errors.New("duplicate text and base64 path")
		}
		decoded, err := base64.StdEncoding.DecodeString(contents)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("invalid base64 for %q", path)})
			return nil, "", err
		}
		raw[path] = decoded
	}

	files, err := tarball.SanitizeFiles(raw)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, "", err
	}
	if err := tarball.ValidateExtensions(files); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, "", err
	}

	return files, digestFiles(files), nil
}

// digestFiles produces a stable SHA-256 over the (sorted) path+content stream,
// recorded on the version row for integrity/audit — the JSON analogue of the
// archive's body digest.
func digestFiles(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	sum := sha256.New()
	for _, name := range names {
		sum.Write([]byte(name))
		sum.Write([]byte{0})
		sum.Write(files[name])
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// createSiteFiles is the JSON create path. Mirrors createSite's pre-checks, then
// shares commitNewSite.
func (h *SiteHandler) createSiteFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_name"})
		return
	}
	if err := validateSiteReserved(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "name_reserved"})
		return
	}
	if !h.newSiteChecks(w, r, user) {
		return
	}

	files, digest, err := h.readJSONFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitNewSite(w, r, user, siteName, files, digest)
}

// newSiteChecks is what creating a site checks before reading the upload,
// shared by POST and by PUT ?create=1: publish=false is refused (a first
// version always goes live) and the per-account site quota (admins exempt).
// Updates to existing sites are never gated by the quota. False after writing
// the answer.
func (h *SiteHandler) newSiteChecks(w http.ResponseWriter, r *http.Request, user *db.User) bool {
	if !h.publishOnCreate(w, r) {
		return false
	}
	if !user.IsAdmin {
		// Sites in Recently deleted count: delete-then-create must not
		// get round the cap (their files are still on disk).
		existing, err := db.CountSitesByUser(r.Context(), h.database, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return false
		}
		if existing >= maxSitesPerUser() {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: fmt.Sprintf("site quota reached: an account holds at most %d sites (sites in Recently deleted count until they are removed)", maxSitesPerUser()), Code: "site_quota_reached"})
			return false
		}
	}
	return true
}

// createIfMissing is PUT ?create=1 (one call from CI whether or not the site
// exists yet): when the caller has no live site of that name it runs the
// create checks and reports create=true, so the upload is committed as a new
// site. ok is false after writing the answer.
func (h *SiteHandler) createIfMissing(w http.ResponseWriter, r *http.Request, user *db.User, siteName string) (create, ok bool) {
	switch v := r.URL.Query().Get("create"); v {
	case "", "0", "false":
		return false, true
	case "1", "true":
	default:
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "create must be 1 or 0", Code: "invalid_request"})
		return false, false
	}
	if _, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName); err == nil {
		return false, true
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false, false
	}
	if err := validateSiteReserved(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "name_reserved"})
		return false, false
	}
	if !h.newSiteChecks(w, r, user) {
		return false, false
	}
	return true, true
}

// updateSiteFiles is the JSON update path. Mirrors updateSite's pre-checks, then
// shares commitSiteUpdate.
func (h *SiteHandler) updateSiteFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if err := validateSiteShape(siteName); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), Code: "invalid_name"})
		return
	}

	publish, ok := publishParam(w, r)
	if !ok {
		return
	}
	create, ok := h.createIfMissing(w, r, user, siteName)
	if !ok {
		return
	}
	files, digest, err := h.readJSONFiles(w, r, siteName)
	if err != nil {
		return
	}

	if create {
		h.commitNewSite(w, r, user, siteName, files, digest)
		return
	}
	h.commitSiteUpdate(w, r, user, siteName, files, digest, publish)
}

// listVersions returns the full version history of a site for the
// authenticated owner. Each row includes the version number, status,
// created_at, and a flag for which one is currently active.
func (h *SiteHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	out := make([]versionResponse, len(versions))
	for i, v := range versions {
		out[i] = versionResponse{
			VersionNumber: v.VersionNumber,
			Status:        v.Status,
			CreatedAt:     v.CreatedAt,
			IsActive:      v.VersionNumber == site.ActiveVersion,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// setActiveVersion points the site's `current` symlink at the requested
// version and updates sites.active_version. Used for rollbacks (target
// older) and roll-forwards (target newer). Idempotent if the target is
// already active.
func (h *SiteHandler) setActiveVersion(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	var req struct {
		VersionNumber int `json:"version_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if req.VersionNumber < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "version_number must be >= 1"})
		return
	}

	// Same lock as a deploy, delete or rename of this site: a rollback never
	// re-points `current` of a site that was deleted or renamed meanwhile.
	unlock := h.lockSite(user.ID, siteName)
	defer unlock()

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}

	if site.ActiveVersion == req.VersionNumber {
		writeJSON(w, http.StatusOK, h.toSiteResponse(site, ""))
		return
	}

	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	var found bool
	for _, v := range versions {
		// A version whose upload never finished is not one to go live.
		if v.VersionNumber == req.VersionNumber && v.Status != "uploading" {
			found = true
			break
		}
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()
	if err := db.LockSiteForUpdate(r.Context(), tx, site.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := h.disk.UpdateCurrent(site.UserID, site.Name, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	if err := db.UpdateSiteActiveVersion(r.Context(), tx, site.ID, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// A version stored with publish=false goes live for the first time.
	if err := db.ActivateVersionNumber(r.Context(), tx, site.ID, req.VersionNumber); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	site.ActiveVersion = req.VersionNumber
	writeJSON(w, http.StatusOK, h.toSiteResponse(site, ""))
}

// setVisibility toggles a site's showcase visibility ('public' | 'unlisted').
// Owner-only: the site is resolved via GetSiteByUser, so a user can only change
// their own sites.
func (h *SiteHandler) setVisibility(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}

	var body struct {
		Visibility string `json:"visibility"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	vis := strings.ToLower(strings.TrimSpace(body.Visibility))
	if vis != "public" && vis != "unlisted" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "visibility must be 'public' or 'unlisted'"})
		return
	}

	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}

	if err := db.SetSiteVisibility(r.Context(), h.database, site.ID, vis); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"name": siteName, "visibility": vis})
}

func (h *SiteHandler) listSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	var sites []db.Site
	var err error

	if user.IsAdmin {
		sites, err = db.ListAllSites(r.Context(), h.database)
	} else {
		sites, err = db.ListSitesByUser(r.Context(), h.database, user.ID)
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	flagsFor := user.ID
	if user.IsAdmin {
		flagsFor = ""
	}
	flags, err := db.ListSiteIdleFlags(r.Context(), h.database, flagsFor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	response := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		resp := h.toSiteResponse(site, "")
		if f, ok := flags[site.ID]; ok {
			resp.Keep = f.Keep
			if f.WarnedAt.Valid && !f.Keep {
				t := f.RemovalAt(idleGrace()).UTC()
				resp.IdleRemovalAt = &t
			}
		}
		response = append(response, resp)
	}

	writeJSON(w, http.StatusOK, response)
}

// adminUsers returns every registered user with their sites nested (admin only).
// Powers the /admin dashboard. API keys are never included.
func (h *SiteHandler) adminUsers(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	if !user.IsAdmin {
		// 404, not 403: a signed-in non-admin should not learn this endpoint
		// exists, and the /admin page renders whatever this returns as a plain
		// not-found. Same reasoning as the reserved-handle 404 on the page route.
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	users, err := db.ListAllUsers(r.Context(), h.database)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	sites, err := db.ListAllSites(r.Context(), h.database)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	// Group sites under their owner.
	byUser := make(map[string][]map[string]any, len(users))
	for _, s := range sites {
		byUser[s.UserID] = append(byUser[s.UserID], map[string]any{
			"id":               s.ID,
			"name":             s.Name,
			"site_url":         h.siteURLFor(s),
			"active_version":   s.ActiveVersion,
			"custom_domain":    s.CustomDomain.String,
			"created_at":       s.CreatedAt,
			"suspended":        s.Suspended(),
			"suspended_reason": s.SuspendedReason(),
			"suspended_by":     suspendedBy(s),
		})
	}

	out := make([]map[string]any, 0, len(users))
	for _, u := range users {
		list := byUser[u.ID]
		if list == nil {
			list = []map[string]any{}
		}
		out = append(out, map[string]any{
			"id":               u.ID,
			"username":         u.Username,
			"handle":           u.Handle.String,
			"display_name":     u.DisplayName.String,
			"is_admin":         u.IsAdmin,
			"created_at":       u.CreatedAt,
			"site_count":       len(list),
			"sites":            list,
			"suspended":        u.Suspended,
			"suspended_reason": u.SuspendedReason,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "user_count": len(out), "site_count": len(sites)})
}

// readAndValidateFiles reads the archive body, verifies an optional
// X-Content-Digest header against the SHA-256 of the raw archive bytes,
// extracts and validates the entries, and returns the files plus the hex
// digest (recorded on the version row for integrity/audit).
func (h *SiteHandler) readAndValidateFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	body, err := readLimitedBody(w, r)
	if err != nil {
		return nil, "", err
	}

	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])

	// Optional integrity check: if the client declares a digest, it must match
	// the bytes we received. Compared in constant time.
	if hdr := strings.TrimSpace(r.Header.Get("X-Content-Digest")); hdr != "" {
		want := strings.ToLower(strings.TrimPrefix(hdr, "sha256="))
		if subtle.ConstantTimeCompare([]byte(want), []byte(digest)) != 1 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "archive digest mismatch"})
			return nil, "", errors.New("archive digest mismatch")
		}
	}

	filename := archiveFilename(siteName, body)
	files, err := tarball.Extract(bytes.NewReader(body), filename)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site archive"})
		return nil, "", err
	}

	if err := tarball.ValidateExtensions(files); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return nil, "", err
	}

	return files, digest, nil
}

func readLimitedBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteArchiveSize)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return nil, err
		}

		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return nil, err
	}

	if len(body) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "request body is required"})
		return nil, errors.New("empty request body")
	}

	return body, nil
}

func archiveFilename(siteName string, body []byte) string {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		return siteName + ".zip"
	}

	return siteName + ".tar.gz"
}

func (h *SiteHandler) toSiteResponse(site db.Site, note string) siteResponse {
	visibility := site.Visibility
	if visibility == "" {
		visibility = "unlisted" // never guess "public"
	}
	resp := siteResponse{
		ID:              site.ID,
		UserID:          site.UserID,
		Name:            site.Name,
		ActiveVersion:   site.ActiveVersion,
		SiteURL:         h.siteURLFor(site),
		CreatedAt:       site.CreatedAt,
		UpdatedAt:       site.UpdatedAt,
		CustomDomain:    site.CustomDomain.String,
		DomainStatus:    site.DomainStatus.String,
		Visibility:      visibility,
		OwnerUsername:   site.OwnerUsername,
		Note:            note,
		Suspended:       site.Suspended(),
		Offline:         site.Offline,
		SuspendedReason: site.SuspendedReason(),
	}
	if site.LastDeployedAt.Valid {
		t := site.LastDeployedAt.Time
		resp.DeployedAt = &t
	}
	if !(site.CustomDomain.Valid && site.DomainVerifiedAt.Valid) {
		resp.AddressState = h.siteAddressStateFor(site.OwnerHandle, site.Name)
	}
	if site.CustomDomain.Valid && site.CustomDomain.String != "" {
		if p := h.partnerInfoFor(site.CustomDomain.String, site.ID); p != nil {
			resp.DomainPartner, resp.DomainPartnerDNS, resp.DomainPartnerStatus, resp.DomainPartnerNote = p.Domain, p.DNS, p.Status, p.Note
		}
	}
	if site.CustomDomain.Valid && site.CustomDomain.String != "" && site.DomainStatus.String != "active" {
		resp.DomainLastError = site.DomainLastError.String
		if !h.isPlatformSubdomainHost(site.CustomDomain.String) {
			rec := h.dnsRecordFor(site.CustomDomain.String)
			resp.DomainDNS = &rec
			resp.DomainDNSTXT = proofRecordFor(site.CustomDomain.String, site.DomainToken)
		}
		resp.DomainCertStatus = site.DomainCertStatus
		resp.PreviousDomain = site.PreviousDomain
		// A binding whose DNS already points here (certificate past
		// "pending") does not lapse while it waits.
		if site.DomainBoundAt.Valid && !site.DomainVerifiedAt.Valid && (site.DomainCertStatus == "" || site.DomainCertStatus == "pending") {
			t := site.DomainBoundAt.Time.Add(db.UnprovenDomainTTL())
			resp.DomainExpiresAt = &t
		}
	}
	return resp
}

func appendToFile(path, line string) error {
	// Defense-in-depth: a newline would inject an extra line into the
	// deploy queue that the root deploy-watcher consumes. Site names are
	// already charset-validated upstream, but never rely solely on that.
	if strings.ContainsAny(line, "\n\r") {
		return errors.New("invalid queue entry")
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}
