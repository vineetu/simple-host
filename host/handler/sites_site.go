package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/workers"
)

// uploadLimits.Body caps the upload body. Default 100 MB; override with
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
var uploadLimits = sites.UploadLimits{Body: 100 << 20}

func init() {
	if v := os.Getenv("MAX_ARCHIVE_MB"); v != "" {
		if mb, err := strconv.Atoi(v); err == nil && mb > 0 {
			SetSiteLimit(int64(mb) << 20)
		}
	}
}

type SiteHandler struct {
	data            *DataHandler
	access          *AccessHandler
	sitemaps        sitemapCache
	sitemapFilter   sites.SitemapFilter
	httpCaches      httpx.Caches
	workerStatus    func() []workers.Status
	ed              *edition.Edition
	siteWorkersOnce sync.Once
	siteWorkers     *siteWorkerGroup
	// limitSet is ed.Limits(), held once so a request does not copy the
	// whole snapshot on every read (limits.go).
	limitSet
	// assets is the static asset and chrome pipeline for ed (assets.go);
	// read it through pipeline().
	assets            *assetPipeline
	assetsOnce        sync.Once
	createIdempotency *createIdempotencyStore
	mailer            signInMailer
	database          *sql.DB
	disk              SiteDisk
	siteDomain        string
	contentHost       string // shared v3 content host, e.g. sites.simple-host.app
	cnameTarget       string // CNAME target for custom domains, e.g. cname.simple-host.app
	customDomainIP    string // box public IPv4 for APEX custom-domain A records
	// customDomainIPAlso: earlier public IPv4s still accepted, never handed out.
	customDomainIPAlso []string
	deployScript       string

	// uploadLocks serializes write+promote per site name (sitename -> *sync.Mutex).
	uploadLocks sync.Map

	// usage caches the disk measurement behind /v1/admin/usage. Measuring walks
	// every file under the data directory, so it is not done per request.
	usage     operations.UsageCache
	reporting *operations.Reporting

	// netUsage reads the box's network counters (netusage.go); nil when off.
	// trafficOn: the analytics log is read, so traffic figures exist.
	network   *operations.NetworkUsage
	trafficOn bool
	// ownerMarks notes the addresses an account is seen signed in from, so
	// the owner's own visits are filed apart from People (ownermarks.go);
	// nil when the analytics log is off.
	ownerMarks *operations.OwnerMarks

	// uploadLimiter throttles create/update uploads per client IP. See
	// ratelimit.go.
	uploadLimiter      *httpx.RateLimiter
	domainCheckLimiter *httpx.RateLimiter
	// domainCheckUserLimiter: "Check again" per account.
	domainCheckUserLimiter *httpx.RateLimiter

	// previewAccounts (by username/email) get ephemeral sites: a site they create
	// expires after previewTTL and is removed by the background sweep. Empty =off.
	previewAccounts map[string]bool
	previewTTL      time.Duration

	// personHosts is PERSON_HOSTS (personhost.go).
	personHosts personHostMode
	// siteHosts is SITE_HOSTS and siteCertDir SITE_CERT_DIR (sitehost.go).
	siteHosts   siteHostMode
	siteCertDir string
	// teamSiteFlatHosts is TEAM_SITE_FLAT_HOSTS (hack_sites.go): a hosted
	// event's team site at <team>--<event>.<base> instead of
	// <team>.<event>.<base>. Off by default; only read through
	// teamSiteFlatHostsOn, which also requires the hosted-events edition.
	teamSiteFlatHosts bool
	// siteBase is SITE_BASE_DOMAIN, baseMove SITE_BASE_MOVE and baseCertDir
	// SITE_BASE_CERT_DIR (sitebase.go).
	siteBase    string
	baseMove    baseMoveMode
	baseCertDir string
	// domainCertDir is DOMAIN_CERT_DIR (domaincert.go).
	domainCertDir string
	// familyCertDir is ADDRESS_FAMILY_CERT_DIR and families the live
	// address families (familyhost.go); the limiters are "Check again".
	familyCertDir string
	// platformZones are more of the platform's own zones (the public
	// site's host, EVENT_DOMAINS): never a custom domain or a family.
	platformZones          []string
	families               familyIndex
	familyCheckLimiter     *httpx.RateLimiter
	familyCheckUserLimiter *httpx.RateLimiter
	// idleCleanup is IDLE_CLEANUP=on (idle.go).
	idleCleanup bool
	idleExempt  model.IdleExempt

	// exportKey signs short-lived export download links (exportlink.go); per
	// process, used for nothing else. publicBaseURL is the apex they point at.
	exportKey []byte

	publicBaseURL string

	// showcaseLimiter bounds public showcase reads per account and address
	// (60 burst, 30 a second).
	showcaseLimiter *httpx.RateLimiter
	groupHosts      GroupHosts
	groupGate       sites.GroupGate
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

// hackInstanceURL is this instance's own public address, used only when ed is
// the Hack edition (the pipeline's inkBaseURL; "" takes canonicalHackBaseURL).
func NewSiteHandlers(ed *edition.Edition, productInstanceURL string, database *sql.DB, disk SiteDisk, siteDomain, contentHost, cnameTarget, customDomainIP, deployScript, adminAPIKey string, previewAccounts map[string]bool, previewTTL time.Duration, adminUserID string, mailer signInMailer, emailLimiter *httpx.RateLimiter) (*SiteHandler, *AccessHandler, *DataHandler) {
	// Uploads: ~6/min/IP, burst 30. State writes: ~1/s/IP sustained, burst 60
	// (a browser app may persist state on each interaction).
	lim := ed.Limits()
	uploadLimiter := newRateLimiterFor(lim.RateUpload)
	storageIPLimiter := newRateLimiterFor(lim.RateStorageIP)
	storageVisitorLimiter := newRateLimiterFor(lim.RateStorageVisitor)

	visitorAuthLimiter := newRateLimiterFor(lim.RateVisitorAuth)
	domainCheckLimiter := newRateLimiterFor(lim.RateDomainCheck)
	domainCheckUserLimiter := newRateLimiterFor(lim.RateDomainCheckUser)

	h := &SiteHandler{
		ed:                     ed,
		limitSet:               newLimitSet(lim),
		mailer:                 mailer,
		database:               database,
		disk:                   disk,
		siteDomain:             siteDomain,
		contentHost:            contentHost,
		cnameTarget:            cnameTarget,
		customDomainIP:         customDomainIP,
		deployScript:           deployScript,
		uploadLimiter:          uploadLimiter,
		domainCheckLimiter:     domainCheckLimiter,
		domainCheckUserLimiter: domainCheckUserLimiter,
		previewAccounts:        previewAccounts,
		previewTTL:             previewTTL,
		exportKey:              newExportKey(),
		network:                operations.NewNetworkUsage(database, ed),
		reporting:              operations.NewReporting(database),
	}
	h.assets = newAssetPipeline(ed, productInstanceURL)
	h.createIdempotency = newCreateIdempotencyStore(lim)
	h.access = &AccessHandler{site: h, ed: ed, limitSet: h.limitSet, database: database, disk: disk, mailer: mailer, emailLimiter: emailLimiter, visitorAuthLimiter: visitorAuthLimiter,
		siteDomain: siteDomain, contentHost: contentHost, exportKey: h.exportKey, uploadLocks: &h.uploadLocks, adminAPIKey: adminAPIKey, adminUserID: adminUserID}
	h.access.passcode.limitSet = h.limitSet
	h.data = &DataHandler{site: h, access: h.access, ed: ed, limitSet: h.limitSet, database: database, disk: disk, mailer: mailer, siteDomain: siteDomain, contentHost: contentHost,
		exportKey: h.exportKey, createIdempotency: h.createIdempotency, httpCaches: &h.httpCaches, passcode: &h.access.passcode, uploadLocks: &h.uploadLocks,
		sqlite: newSQLiteGate(lim.StorageSQLConcurrency, lim.StorageAcquireWait), storageIPLimiter: storageIPLimiter, storageVisitorLimiter: storageVisitorLimiter,
		spamIPLimiter: newRateLimiterFor(lim.RateSpamCheckedIP), spamSiteLimiter: newRateLimiterFor(lim.RateSpamCheckedSite)}
	h.httpCaches.Add("http.site.idempotency", time.Hour, h.createIdempotency.evictExpired)
	h.showcaseLimiter = httpx.NewRateLimiter(60, 30)
	h.httpCaches.Rate("http.site.showcaseLimiter", h.showcaseLimiter, 10*time.Minute, 30*time.Minute)
	if len(previewAccounts) > 0 {
		ttlHours := int(previewTTL.Hours())
		for u := range previewAccounts {
			if n, err := h.sitesSvc().BackfillPreviewExpiry(context.Background(), u, ttlHours); err != nil {
				log.Printf("preview backfill %q: %v", u, err)
			} else if n > 0 {
				log.Printf("preview backfill: stamped expiry on %d existing site(s) for %q", n, u)
			}
		}
		log.Printf("preview-site expiry enabled: accounts=%d ttl=%s", len(previewAccounts), previewTTL)
	}
	if customDomainIP != "" {
		log.Printf("custom-domain verification enabled: every %s, active re-proved after %s", h.domainCheckInterval(), domainActiveAge)
	}
	h.httpCaches.Rate("http.site.storageIPLimiter", storageIPLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.site.storageVisitorLimiter", storageVisitorLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.site.domainCheckLimiter", domainCheckLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.site.domainCheckUserLimiter", domainCheckUserLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.site.visitorAuthLimiter", visitorAuthLimiter, 10*time.Minute, 30*time.Minute)
	h.httpCaches.Rate("http.site.uploadLimiter", uploadLimiter, 10*time.Minute, 30*time.Minute)
	h.access.data = h.data
	return h, h.access, h.data
}

func (h *SiteHandler) startExpirySweepContext(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.preview_expiry")
	o.periodic(ctx, 30*time.Second, every, func(ctx context.Context) { h.sitesSvc().SweepExpiredSites(ctx) })
}
func (h *SiteHandler) sweepExpiredSites() { h.sitesSvc().SweepExpiredSites(context.Background()) }

// renameSite renames the caller's site oldName to newName (PATCH with
// {"name": ...}, see patchSite).
func (h *SiteHandler) renameSite(w http.ResponseWriter, r *http.Request, oldName, newName string) {
	user := auth.GetUser(r.Context())
	result, err := h.sitesSvc().Rename(r.Context(), sites.RenameInput{UserID: user.ID, OldName: oldName, NewName: newName, Handle: user.Handle.String})
	if err != nil {
		var siteErr *sites.Error
		if errors.As(err, &siteErr) {
			switch siteErr.Code {
			case "invalid_name", "name_reserved":
				writeJSON(w, 400, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
				return
			case "unchanged_name":
				writeJSON(w, 400, errorResponse{Error: siteErr.Message})
				return
			case "site_exists":
				writeJSON(w, http.StatusConflict, errorResponse{Error: "you already have a site with that name", Code: "site_exists"})
				return
			case "recently_deleted":
				writeJSON(w, http.StatusConflict, map[string]any{"error": siteErr.Message, "code": "recently_deleted", "recently_deleted": true})
				return
			case "move_failed":
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "could not move site files"})
				return
			}
		}
		if isUniqueViolation(err) {
			writeJSON(w, http.StatusConflict, errorResponse{Error: "you already have a site with that name", Code: "site_exists"})
			return
		}
		writeSitesError(w, err, oldName)
		return
	}
	site, oldURL, newURL, domain := result.Site, result.OldURL, result.NewURL, result.Domain

	// The main address may change with the name (address families).
	writeJSON(w, http.StatusOK, map[string]any{
		"id": site.ID, "old_name": oldName, "name": newName,
		"old_url": oldURL, "site_url": newURL, "old_url_status": "redirects",
		"message":                 "Site renamed. Links to the old address redirect to the new one until a new site takes the old name.",
		"custom_domain_unchanged": domain != "",
	})
}

// setAllowedOrigins lets a site owner list extra origins (scheme://host) that may
// call the site's API cross-origin — so a page hosted anywhere
// (external hosting, Netlify, …) can use this site as its backend. Owner-only.
func (h *SiteHandler) setAllowedOrigins(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	site, err := h.sitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		writeSiteNotFound(w, siteName)
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
	if err := h.sitesSvc().SetAllowedOrigins(r.Context(), site.ID, strings.Join(clean, ",")); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "allowed_origins": clean})
}

var errBareSiteName = errors.New("bare site name")

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
	if !writeCreatePreflight(w, h.sitesSvc().CreatePreflight(r.Context(), sites.PreflightInput{
		User: user, Name: siteName, CheckName: true, Publish: r.URL.Query().Get("publish"),
	})) {
		return
	}

	if !h.readArchiveCreateAccess(w, r) {
		return
	}
	files, archiveSHA, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	h.commitCreate(w, r, user, siteName, files, archiveSHA, false)
}

// createOrUpdateSite is the commit for PUT ?create=1: it creates the site, and
// when a concurrent call created it first (two CI jobs racing), commits the
// upload as an update instead of answering site_exists to a caller that asked
// for create-or-update.
func (h *SiteHandler) createOrUpdateSite(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, files map[string][]byte, archiveSHA string, publish bool) {
	h.respondSiteUpdate(w, r, user, siteName, sites.UpdateInput{User: user, Name: siteName, Files: files, ArchiveSHA: archiveSHA, Author: versionAuthor(r), Publish: publish, CreateIfMissing: true}, func(site siteResponse) { writeJSON(w, http.StatusOK, site) })
}

// commitCreate commits files as a new site. With orUpdate, a site of that name
// that already exists is not an error: nothing is written and it reports true
// so the caller updates it (after this has released the site lock).
func (h *SiteHandler) commitCreate(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, files map[string][]byte, archiveSHA string, orUpdate bool) (exists bool) {
	// Every path that creates a site comes through here (hack_mode.go).
	// A guest-created users row has a NULL handle until owner-intent. First
	// deploy is owner-intent: assign before building the path-model site URL.
	access := creationAccess(r)
	result, err := h.sitesSvc().Create(r.Context(), sites.CreateInput{
		Access: access.Access, Viewers: access.Viewers, NamedViewersRefusal: access.NamedViewersRefusal,
		User: user, Name: siteName,
		ArchiveSHA: archiveSHA, Author: versionAuthor(r), Files: files, OrUpdate: orUpdate,
	})
	return h.respondCreate(w, user, result, err)
}

// respondCreate maps a completed create workflow to its existing HTTP response.
func (h *SiteHandler) respondCreate(w http.ResponseWriter, user *model.User, result sites.CreateResult, err error) bool {
	site, exists := result.Site, result.Exists
	if exists {
		return true
	}
	if err != nil {
		var gateErr *sites.GateError
		var siteErr *sites.Error
		switch {
		case errors.As(err, &siteErr) && (siteErr.Code == "named_viewers_unavailable" || siteErr.Code == "sign_in_unavailable" || siteErr.Code == "named_viewers_need_own_address" || siteErr.Code == "access_differs"):
			writeJSON(w, 409, errorResponse{Code: siteErr.Code, Error: siteErr.Message})
		case errors.As(err, &siteErr) && siteErr.Code == "too_many_viewers":
			writeJSON(w, 400, errorResponse{Code: siteErr.Code, Error: siteErr.Message})
		case errors.As(err, &siteErr) && siteErr.Code == "access_marker":
			writeJSON(w, 500, errorResponse{Code: siteErr.Code, Error: siteErr.Message})
		case errors.As(err, &siteErr) && siteErr.Code == "redirects_invalid":
			writeJSON(w, 400, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
		case errors.As(err, &siteErr) && siteErr.Code == "unauthorized":
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		case errors.As(err, &gateErr):
			writeRollbackGateError(w, gateErr)
		case errors.As(err, &siteErr) && siteErr.Code == "no_personal_sites":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
		case errors.As(err, &siteErr) && siteErr.Code == "recently_deleted":
			writeJSON(w, http.StatusConflict, map[string]any{"error": siteErr.Message, "code": "recently_deleted", "recently_deleted": true})
		case errors.As(err, &siteErr) && siteErr.Code == "site_exists":
			writeJSON(w, http.StatusConflict, errorResponse{Error: siteErr.Message, Code: "site_exists"})
		case errors.As(err, &siteErr) && (siteErr.Code == "site_total_too_large" || siteErr.Code == "account_storage_full"):
			writeJSON(w, 413, errorResponse{Code: siteErr.Code, Error: siteErr.Message, Hint: storageTips})
		case errors.As(err, &siteErr) && (siteErr.Code == "site_measure_failed" || siteErr.Code == "account_measure_failed"):
			writeJSON(w, 500, errorResponse{Error: siteErr.Message})
		default:
			writeInternal(w)
		}
		return false
	}
	state := result.ResponseState.(siteResponseState)
	state.fileBytes = result.FileBytes
	resp := h.siteResponseFromState(user, site, state)
	writeJSON(w, http.StatusCreated, resp)
	return false
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
	preflight, err := h.sitesSvc().UpdatePreflight(r.Context(), sites.UpdatePreflightInput{
		User: user, Name: siteName, Publish: r.URL.Query().Get("publish"), Create: r.URL.Query().Get("create"),
	})
	if !writeCreatePreflight(w, err) {
		return
	}
	publish, create := preflight.Publish, preflight.Create

	if !h.readArchiveCreateAccess(w, r) {
		return
	}
	files, archiveSHA, err := h.readAndValidateFiles(w, r, siteName)
	if err != nil {
		return
	}

	if create {
		h.createOrUpdateSite(w, r, user, siteName, files, archiveSHA, publish)
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
func (h *SiteHandler) commitSiteUpdate(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, files map[string][]byte, archiveSHA string, publish bool) {
	h.respondSiteUpdate(w, r, user, siteName, sites.UpdateInput{User: user, Name: siteName, Files: files, ArchiveSHA: archiveSHA, Author: versionAuthor(r), Publish: publish}, func(site siteResponse) { writeJSON(w, http.StatusOK, site) })
}

func (h *SiteHandler) respondSiteUpdate(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, input sites.UpdateInput, respond func(siteResponse)) {
	access := creationAccess(r)
	input.Access, input.Viewers, input.NamedViewersRefusal = access.Access, access.Viewers, access.NamedViewersRefusal
	input.ExpectedID, input.CheckID = r.Context().Value(adminSiteKey{}).(string)
	ifMatch, ok := h.ifMatchVersion(w, r)
	if !ok {
		return
	}
	input.IfMatch = ifMatch
	result, err := h.sitesSvc().Update(r.Context(), input)
	if result.Created != nil {
		h.respondCreate(w, user, *result.Created, err)
		return
	}
	if err != nil {
		h.writeSiteUpdateError(w, r, siteName, input, err)
		return
	}
	if !result.Published {
		h.writeUnpublishedState(w, user, result.Site, result.Version, result.ResponseState.(siteResponseState))
		return
	}
	respond(h.siteResponseFromState(user, result.Site, result.ResponseState.(siteResponseState)))
}

func (h *SiteHandler) writeSiteUpdateError(w http.ResponseWriter, r *http.Request, siteName string, input sites.UpdateInput, err error) {
	var siteErr *sites.Error
	var gateErr *sites.GateError
	switch {
	case errors.As(err, &gateErr):
		writeRollbackGateError(w, gateErr)
	case errors.As(err, &siteErr) && (siteErr.Code == "named_viewers_unavailable" || siteErr.Code == "sign_in_unavailable" || siteErr.Code == "named_viewers_need_own_address" || siteErr.Code == "access_differs"):
		writeJSON(w, 409, errorResponse{Code: siteErr.Code, Error: siteErr.Message})
	case errors.As(err, &siteErr) && siteErr.Code == "too_many_viewers":
		writeJSON(w, 400, errorResponse{Code: siteErr.Code, Error: siteErr.Message})
	case errors.As(err, &siteErr) && siteErr.Code == "access_marker":
		writeJSON(w, 500, errorResponse{Code: siteErr.Code, Error: siteErr.Message})
	case errors.As(err, &siteErr) && siteErr.Code == "redirects_invalid":
		writeJSON(w, 400, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
	case errors.As(err, &siteErr) && siteErr.Code == "deploy_site_not_found":
		writeDeploySiteNotFound(w, r, siteName)
	case errors.As(err, &siteErr) && siteErr.Code == "patch_too_large":
		writeTooLarge(w, input.Limit, siteErr.Message)
	case errors.As(err, &siteErr) && siteErr.Code == "patch_invalid":
		writeJSON(w, 400, errorResponse{Error: siteErr.Message})
	case errors.As(err, new(*sites.UploadError)):
		h.writePreparationError(w, err, input.Limit, false)
	case errors.As(err, &siteErr) && (siteErr.Code == "site_total_too_large" || siteErr.Code == "account_storage_full"):
		writeJSON(w, 413, errorResponse{Code: siteErr.Code, Error: siteErr.Message, Hint: storageTips})
	case errors.As(err, &siteErr) && (siteErr.Code == "site_measure_failed" || siteErr.Code == "account_measure_failed"):
		writeJSON(w, 500, errorResponse{Error: siteErr.Message})
	default:
		writeSitesError(w, err, siteName)
	}
}

// writeCreatePreflight maps the upload eligibility decision to its HTTP response.
func writeCreatePreflight(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		switch siteErr.Code {
		case "site_quota_reached", "no_personal_sites":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return false
		case "name_reserved", "invalid_name", "invalid_request":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return false
		}
	}
	writeInternal(w)
	return false
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

	site, err := h.siteForCaller(r, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		writeInternal(w)
		return
	}

	versions, err := h.sitesSvc().ListVersions(r.Context(), site.ID)
	if err != nil {
		writeInternal(w)
		return
	}

	// ?pending=1: only versions stored with publish=false that nobody has
	// made live yet, the ones discardVersion can remove.
	pending := pendingParam(r)
	out := make([]versionResponse, 0, len(versions))
	for _, v := range versions {
		isActive := v.VersionNumber == site.ActiveVersion
		if pending && (v.Status != "ready" || isActive) {
			continue
		}
		out = append(out, versionResponse{
			VersionNumber: v.VersionNumber,
			Status:        v.Status,
			Author:        v.Author,
			CreatedAt:     v.CreatedAt,
			IsActive:      isActive,
		})
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
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	if req.VersionNumber < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "version_number must be >= 1"})
		return
	}

	expectedID, checkID := r.Context().Value(adminSiteKey{}).(string)
	ifMatch, ok := h.ifMatchVersion(w, r)
	if !ok {
		return
	}
	result, err := h.sitesSvc().Rollback(r.Context(), sites.RollbackInput{User: user, Name: siteName, ExpectedID: expectedID, CheckID: checkID, Version: req.VersionNumber, IfMatch: ifMatch})
	if err != nil {
		var gateErr *sites.GateError
		if errors.As(err, &gateErr) {
			writeRollbackGateError(w, gateErr)
		} else {
			writeSitesError(w, err, siteName)
		}
		return
	}
	writeJSON(w, http.StatusOK, h.siteResponseFromState(user, result.Site, result.ResponseState.(siteResponseState)))
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
	if !httpx.DecodeJSON(w, r, 64<<10, &body) {
		return
	}
	vis := strings.ToLower(strings.TrimSpace(body.Visibility))
	if vis != "public" && vis != "unlisted" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "visibility must be 'public' or 'unlisted'"})
		return
	}

	if err := h.sitesSvc().SetVisibility(r.Context(), sites.VisibilityInput{UserID: user.ID, Name: siteName, Visibility: vis}); err != nil {
		writeSitesError(w, err, siteName)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"name": siteName, "visibility": vis})
}

// listSites serves GET /v1/sites?[query, created_*, updated_*, limit, offset,
// order, all]. Every caller, including the admin, sees only their own
// sites by default, newest edited first; ?all=1 is an admin-only switch to
// every account's sites (SIM-329).
func (h *SiteHandler) listSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}

	// ?all=1 widens an admin's own list to every account's sites. Silently
	// narrowing a non-admin's all=1 to their own sites would be a lie about
	// what was returned; refuse it instead (same rule as /v1/analytics/sites).
	allAccounts := r.URL.Query().Get("all") == "1"
	if allAccounts && !user.IsAdmin {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "admin only"})
		return
	}
	h.noteOwnerBrowser(r, user)

	var sites []model.Site
	var err error

	if allAccounts {
		sites, err = h.siteReporting().SiteListing(r.Context())
	} else {
		sites, err = h.sitesSvc().ListSitesByUser(r.Context(), user.ID)
	}
	if err == nil && user.Member != nil && user.Member.Edits != nil {
		// An editor sees only the sites they edit.
		kept := sites[:0]
		for _, s := range sites {
			if slices.Contains(user.Member.Edits, strings.ToLower(s.Name)) {
				kept = append(kept, s)
			}
		}
		sites = kept
	}
	if err != nil {
		writeInternal(w)
		return
	}

	sites, filterErr := filterSiteList(sites, r.URL.Query())
	if filterErr != nil {
		writeJSON(w, http.StatusBadRequest, *filterErr)
		return
	}

	flagsFor := user.ID
	if allAccounts {
		flagsFor = ""
	}
	kept, err := h.sitesSvc().ListSiteKeep(r.Context(), flagsFor)
	if err != nil {
		writeInternal(w)
		return
	}
	prefs, err := h.sitesSvc().ShowcasePreferences(r.Context(), flagsFor)
	if err != nil {
		writeInternal(w)
		return
	}
	// Views exist only where the analytics log is read; elsewhere the field
	// is left out rather than showing zeros nobody measured.
	var views map[string]operations.SiteViews
	if h.trafficOn {
		views, err = h.reporting.PersonViewsBySite(r.Context(), flagsFor, allAccounts)
		if err != nil {
			writeInternal(w)
			return
		}
	}
	response := make([]siteResponse, 0, len(sites))
	for _, site := range sites {
		resp := h.toSiteResponse(site, "")
		if views != nil {
			v := views[site.ID] // zero when the site had no people views
			resp.Views = &v
		}
		footprint, err := h.disk.SiteFootprint(site.UserID, site.Name)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "could not measure website files"})
			return
		}
		resp.FileBytes = footprint.Total()
		resp.Pinned = prefs[site.ID].Pinned
		resp.Order = prefs[site.ID].Order
		if user.Member != nil {
			resp.Owner, resp.AccessRole = user.Handle.String, "member"
			if user.Member.Edits != nil {
				resp.AccessRole = "editor"
			}
		}
		resp.Keep = kept[site.ID]
		response = append(response, resp)
	}

	writeJSON(w, http.StatusOK, response)
}

// adminUsers returns every registered user with their sites nested (admin only).
// Powers the /admin dashboard. API keys are never included.
func (h *SiteHandler) adminUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}

	users, err := h.siteReporting().SiteAccounts(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	sites, err := h.siteReporting().SiteListing(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}
	// A self sign-up's users.username is a synthetic, never-shown value
	// (CreatePasswordAccount): the admin page shows each password
	// account's own login instead.
	logins, err := h.siteReporting().SiteAccountLogins(r.Context())
	if err != nil {
		writeInternal(w)
		return
	}

	// Group sites under their owner.
	byUser := make(map[string][]map[string]any, len(users))
	for _, s := range sites {
		m := h.adminSiteRow(s)
		byUser[s.UserID] = append(byUser[s.UserID], m)
	}

	// Earlier handles, only needed to match MAX_SITES_OVERRIDES and
	// MAX_ARCHIVE_MB_OVERRIDES.
	var aliases map[string][]string
	if l := h.limits; l.MaxSitesOverrides != "" || l.MaxArchiveOverrides != "" {
		if aliases, err = h.siteReporting().SiteAccountAliases(r.Context()); err != nil {
			writeInternal(w)
			return
		}
	}

	out := make([]map[string]any, 0, len(users))
	present := map[string]bool{}
	for _, u := range users {
		group := signupGroupOf(u.Signup.Source)
		present[group] = true
		list := byUser[u.ID]
		if list == nil {
			list = []map[string]any{}
		}
		out = append(out, map[string]any{
			"id":       u.ID,
			"username": u.Username,
			// Login is this account's own login (account_passwords.login),
			// "" for an account with no password row. A self sign-up's
			// Username above is a synthetic value, never what it actually
			// signs in with: the page must show Login instead whenever it
			// is present.
			"login":            logins[u.ID],
			"handle":           u.Handle.String,
			"page_url":         h.PersonPageURL(u.Handle.String),
			"display_name":     u.DisplayName.String,
			"is_admin":         u.IsAdmin,
			"created_at":       u.CreatedAt,
			"site_count":       len(list),
			"max_sites":        h.maxSitesOf(u, aliases[u.ID]),
			"max_site_mb":      h.siteLimitMBOf(u, aliases[u.ID]),
			"sites":            list,
			"suspended":        u.Suspended,
			"suspended_reason": u.SuspendedReason,
			// Where the account came from (users.signup_*): "" for accounts
			// from before it was recorded.
			"signup_source":   u.Signup.Source,
			"signup_agent":    u.Signup.Agent,
			"signup_method":   u.Signup.Method,
			"signup_inferred": u.Signup.Inferred,
			// The group the admin page counts it under, and the outside
			// link it is credited to (null when none).
			"signup_group":  group,
			"signup_detail": u.Signup.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "user_count": len(byUser), "site_count": len(sites), "signup_groups": signupGroups(present)})
}

// ReadAndValidateFilesForTest is readAndValidateFiles, exported as a test
// hook (cleanup 3.2 slice 3): hack_uploads_test.go (hack-prefixed, but this
// one test exercises this Host-only validation path directly, with no
// HackHandler involved) moved to host/hackhttp along with the rest of
// its file.
func (h *SiteHandler) ReadAndValidateFilesForTest(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	return h.readAndValidateFiles(w, r, siteName)
}

// readAndValidateFiles reads the archive body, verifies an optional
// X-Content-Digest header against the SHA-256 of the raw archive bytes,
// extracts and validates the entries, and returns the files plus the hex
// digest (recorded on the version row for integrity/audit).
func (h *SiteHandler) readAndValidateFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	limit, override := h.siteLimitFor(r.Context(), h.database, auth.GetUser(r.Context()))
	body, err := readLimitedBody(w, r, limit)
	if err != nil {
		return nil, "", err
	}

	files, digest, err := h.sitesSvc().PrepareArchive(sites.ArchivePreparationInput{
		Body: body, Filename: archiveFilename(siteName, body), ContentDigest: r.Header.Get("X-Content-Digest"),
		Limit: limit, Override: override,
	})
	if err != nil {
		h.writePreparationError(w, err, limit, true)
		return nil, "", err
	}
	return files, digest, nil
}

func readLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeTooLarge(w, limit, "")
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

// writeTooLarge answers 413 for an upload over this account's per-site limit
// (siteLimitFor), naming the limit; detail is what exceeded it, when known.
func writeTooLarge(w http.ResponseWriter, limit int64, detail string) {
	msg := "request body too large"
	if detail != "" {
		msg = detail
	}
	writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{
		Error: fmt.Sprintf("%s: a site on this account may be at most %d MB", msg, statedSiteMB(limit)),
		Code:  "site_too_large",
		Hint:  storageTips,
	})
}

func archiveFilename(siteName string, body []byte) string {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		return siteName + ".zip"
	}

	return siteName + ".tar.gz"
}

// ifMatchVersion reads the optional If-Match header of a deploy, update or
// rollback: the active version the caller last read, as "3" (or 3). It is
// optional everywhere; agents send it on sites other people also publish (a
// team's), so two members never overwrite each other unknowingly. 0 when
// absent or *. A malformed value is refused (400 invalid_if_match). Read
// only where teams are (the Simple Host program); elsewhere it is ignored as
// before.
func (h *SiteHandler) ifMatchVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" || raw == "*" || !h.ed.Identity().PersonalSites {
		return 0, true
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || n < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `If-Match must be the site's active version in quotes, e.g. If-Match: "3"`, Code: "invalid_if_match"})
		return 0, false
	}
	return n, true
}
