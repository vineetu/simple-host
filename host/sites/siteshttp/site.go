package siteshttp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/sites/idempotency"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/host/web"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/data/notify"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/workers"
)

type SiteHandler struct {
	// notify queues the owner's site events; sqliteSites admits SQLite
	// work per site (both shared with the data routes).
	Notify          *notify.Engine
	SqliteSites     *appstorage.SiteSlots
	Sitemaps        sitemapCache
	sitemapFilter   sites.SitemapFilter
	Caches          httpx.Caches
	workerStatus    func() []workers.Status
	Ed              *edition.Edition
	siteWorkersOnce sync.Once
	siteWorkers     *siteWorkerGroup
	// limits is ed.Limits(), held once so a request does not copy the
	// whole snapshot on every read.
	Limits            edition.Limits
	Assets            *web.AssetPipeline
	assetsOnce        sync.Once
	CreateIdempotency *idempotency.Store
	Mailer            email.Sender
	Database          *sql.DB
	Disk              sites.Disk
	SiteDomain        string
	ContentHost       string // shared v3 content host, e.g. sites.simple-host.app
	CNAMETarget       string // CNAME target for custom domains, e.g. cname.simple-host.app
	CustomDomainIP    string // box public IPv4 for APEX custom-domain A records
	DeployScript      string

	// uploadLocks serializes write+promote per site name (sitename -> *sync.Mutex).
	UploadLocks sync.Map

	// usage caches the disk measurement behind /v1/admin/usage. Measuring walks
	// every file under the data directory, so it is not done per request.
	usage     operations.UsageCache
	Reporting *operations.Reporting

	Network   *operations.NetworkUsage
	trafficOn bool
	// ownerMarks files the owner's own visits apart from People (ownermarks.go); nil when the analytics log is off.
	ownerMarks *operations.OwnerMarks

	UploadLimiter          *httpx.RateLimiter
	DomainCheckLimiter     *httpx.RateLimiter
	DomainCheckUserLimiter *httpx.RateLimiter

	// previewAccounts (by username/email) get ephemeral sites: a site they create
	// expires after previewTTL and is removed by the background sweep. Empty =off.
	PreviewAccounts map[string]bool
	PreviewTTL      time.Duration

	Hosts                  *sites.Hosts
	Gate                   http.Handler
	noVisitorSignIn        bool
	familyCheckLimiter     *httpx.RateLimiter
	familyCheckUserLimiter *httpx.RateLimiter
	idleCleanup            bool
	IdleExempt             model.IdleExempt

	// exportKey signs short-lived export download links (exportlink.go); per
	// process, used for nothing else. publicBaseURL is the apex they point at.
	ExportKey []byte

	publicBaseURL string

	ShowcaseLimiter *httpx.RateLimiter
	groupGate       sites.GroupGate
}

// lockSite acquires the per-site upload mutex, keyed by account and name so two people's same-named sites never wait.
func (h *SiteHandler) LockSite(userID, name string) func() {
	return sites.LockSiteName(&h.UploadLocks, userID, name)
}

func (h *SiteHandler) startExpirySweepContext(ctx context.Context, every time.Duration) {
	o := h.siteLoop("sites.preview_expiry")
	o.periodic(ctx, 30*time.Second, every, func(ctx context.Context) { h.SitesSvc().SweepExpiredSites(ctx) })
}
func (h *SiteHandler) SweepExpiredSites() { h.SitesSvc().SweepExpiredSites(context.Background()) }

func (h *SiteHandler) renameSite(w http.ResponseWriter, r *http.Request, oldName, newName string) {
	user := auth.GetUser(r.Context())
	result, err := h.SitesSvc().Rename(r.Context(), sites.RenameInput{UserID: user.ID, OldName: oldName, NewName: newName, Handle: user.Handle.String})
	if err != nil {
		var siteErr *sites.Error
		if errors.As(err, &siteErr) {
			switch siteErr.Code {
			case "invalid_name", "name_reserved":
				httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
				return
			case "unchanged_name":
				httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: siteErr.Message})
				return
			case "site_exists":
				httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "you already have a site with that name", Code: "site_exists"})
				return
			case "recently_deleted":
				httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": siteErr.Message, "code": "recently_deleted", "recently_deleted": true})
				return
			case "move_failed":
				httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: "could not move site files"})
				return
			}
		}
		if sqlkit.IsUniqueViolation(err) {
			httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "you already have a site with that name", Code: "site_exists"})
			return
		}
		writeSitesError(w, err, oldName)
		return
	}
	site, oldURL, newURL, domain := result.Site, result.OldURL, result.NewURL, result.Domain

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id": site.ID, "old_name": oldName, "name": newName,
		"old_url": oldURL, "site_url": newURL, "old_url_status": "redirects",
		"message":                 "Site renamed. Links to the old address redirect to the new one until a new site takes the old name.",
		"custom_domain_unchanged": domain != "",
	})
}

// setAllowedOrigins lets a site owner list extra origins that may call the site's API cross-origin, so a page
// hosted anywhere can use this site as its backend. Owner-only.
func (h *SiteHandler) setAllowedOrigins(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		writeSiteNotFound(w, siteName)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}

	var req struct {
		Origins []string `json:"origins"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body (expected {\"origins\":[\"https://you.github.io\"]})"})
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
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "each origin must be scheme://host with no path, e.g. https://you.github.io"})
			return
		}
		clean = append(clean, u.Scheme+"://"+u.Host)
	}
	if len(clean) > 20 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "too many origins (max 20)"})
		return
	}
	if err := h.SitesSvc().SetAllowedOrigins(r.Context(), site.ID, strings.Join(clean, ",")); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"site": siteName, "allowed_origins": clean})
}

func (h *SiteHandler) createSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	if !writeCreatePreflight(w, h.SitesSvc().CreatePreflight(r.Context(), sites.PreflightInput{
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

// createOrUpdateSite is the commit for PUT ?create=1: when a concurrent call created the site first
// (two CI jobs racing), the upload is committed as an update instead of answering site_exists.
func (h *SiteHandler) createOrUpdateSite(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, files map[string][]byte, archiveSHA string, publish bool) {
	h.respondSiteUpdate(w, r, user, siteName, sites.UpdateInput{User: user, Name: siteName, Files: files, ArchiveSHA: archiveSHA, Author: versionAuthor(r), Publish: publish, CreateIfMissing: true}, func(site SiteResponse) { httpx.WriteJSON(w, http.StatusOK, site) })
}

// commitCreate commits files as a new site. With orUpdate, a site of that name
// that already exists is not an error: nothing is written and it reports true
// so the caller updates it (after this has released the site lock).
func (h *SiteHandler) commitCreate(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, files map[string][]byte, archiveSHA string, orUpdate bool) (exists bool) {
	// Every path that creates a site comes through here (hack_mode.go).
	// A guest-created users row has a NULL handle until owner-intent. First
	// deploy is owner-intent: assign before building the path-model site URL.
	access := creationAccess(r)
	result, err := h.SitesSvc().Create(r.Context(), sites.CreateInput{
		Access: access.Access, Viewers: access.Viewers, NamedViewersRefusal: access.NamedViewersRefusal,
		User: user, Name: siteName,
		ArchiveSHA: archiveSHA, Author: versionAuthor(r), Files: files, OrUpdate: orUpdate,
	})
	return h.respondCreate(w, user, result, err)
}

func (h *SiteHandler) respondCreate(w http.ResponseWriter, user *model.User, result sites.CreateResult, err error) bool {
	if result.Exists {
		return true
	}
	if err != nil {
		writeCreateError(w, err)
		return false
	}
	state := result.ResponseState.(siteResponseState)
	state.fileBytes = result.FileBytes
	httpx.WriteJSON(w, http.StatusCreated, h.siteResponseFromState(result.Site, state))
	return false
}

func writeCreateError(w http.ResponseWriter, err error) {
	if writePublishRefusal(w, err) {
		return
	}
	var gateErr *sites.GateError
	if errors.As(err, &gateErr) {
		writeRollbackGateError(w, gateErr)
		return
	}
	var siteErr *sites.Error
	if !errors.As(err, &siteErr) {
		httpx.WriteInternal(w)
		return
	}
	switch siteErr.Code {
	case "unauthorized":
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
	case "no_personal_sites":
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
	case "recently_deleted":
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": siteErr.Message, "code": "recently_deleted", "recently_deleted": true})
	case "site_exists":
		httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: siteErr.Message, Code: "site_exists"})
	default:
		httpx.WriteInternal(w)
	}
}

var publishStatus = map[string]int{
	"named_viewers_unavailable": 409, "sign_in_unavailable": 409, "named_viewers_need_own_address": 409, "access_differs": 409,
	"too_many_viewers": 400, "redirects_invalid": 400, "access_marker": 500,
}

func writePublishRefusal(w http.ResponseWriter, err error) bool {
	var siteErr *sites.Error
	if !errors.As(err, &siteErr) {
		return false
	}
	if status, ok := publishStatus[siteErr.Code]; ok {
		httpx.WriteJSON(w, status, httpx.ErrorResponse{Code: siteErr.Code, Error: siteErr.Message})
		return true
	}
	switch siteErr.Code {
	case "site_total_too_large", "account_storage_full":
		httpx.WriteJSON(w, 413, httpx.ErrorResponse{Code: siteErr.Code, Error: siteErr.Message, Hint: sites.StorageTips})
	case "site_measure_failed", "account_measure_failed":
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: siteErr.Message})
	default:
		return false
	}
	return true
}

func (h *SiteHandler) updateSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	// Charset/shape only on update — never the reserved-name denylist, so an
	// existing site is always re-deployable.
	preflight, err := h.SitesSvc().UpdatePreflight(r.Context(), sites.UpdatePreflightInput{
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

// commitSiteUpdate appends a new version to an owned site and promotes it after commit (archive and JSON uploads).
// publish=false stores the version without making it live (see preview.go): `current`, active_version and
// what visitors see stay as they are, and the answer names the new version and a preview link.
func (h *SiteHandler) commitSiteUpdate(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, files map[string][]byte, archiveSHA string, publish bool) {
	h.respondSiteUpdate(w, r, user, siteName, sites.UpdateInput{User: user, Name: siteName, Files: files, ArchiveSHA: archiveSHA, Author: versionAuthor(r), Publish: publish}, func(site SiteResponse) { httpx.WriteJSON(w, http.StatusOK, site) })
}

func (h *SiteHandler) respondSiteUpdate(w http.ResponseWriter, r *http.Request, user *model.User, siteName string, input sites.UpdateInput, respond func(SiteResponse)) {
	access := creationAccess(r)
	input.Access, input.Viewers, input.NamedViewersRefusal = access.Access, access.Viewers, access.NamedViewersRefusal
	input.ExpectedID, input.CheckID = r.Context().Value(web.AdminSiteKey{}).(string)
	ifMatch, ok := h.ifMatchVersion(w, r)
	if !ok {
		return
	}
	input.IfMatch = ifMatch
	result, err := h.SitesSvc().Update(r.Context(), input)
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
	respond(h.siteResponseFromState(result.Site, result.ResponseState.(siteResponseState)))
}

func (h *SiteHandler) writeSiteUpdateError(w http.ResponseWriter, r *http.Request, siteName string, input sites.UpdateInput, err error) {
	var siteErr *sites.Error
	var gateErr *sites.GateError
	switch {
	case errors.As(err, &gateErr):
		writeRollbackGateError(w, gateErr)
	case writePublishRefusal(w, err):
	case errors.As(err, new(*sites.UploadError)):
		h.writePreparationError(w, err, input.Limit, false)
	case !errors.As(err, &siteErr):
		writeSitesError(w, err, siteName)
	case siteErr.Code == "deploy_site_not_found":
		writeDeploySiteNotFound(w, r, siteName)
	case siteErr.Code == "patch_too_large":
		writeTooLarge(w, input.Limit, siteErr.Message)
	case siteErr.Code == "patch_invalid":
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: siteErr.Message})
	default:
		writeSitesError(w, err, siteName)
	}
}

func writeCreatePreflight(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	var siteErr *sites.Error
	if errors.As(err, &siteErr) {
		switch siteErr.Code {
		case "site_quota_reached", "no_personal_sites":
			httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return false
		case "name_reserved", "invalid_name", "invalid_request":
			httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
			return false
		}
	}
	httpx.WriteInternal(w)
	return false
}

func (h *SiteHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	site, err := h.SiteForCaller(r, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		httpx.WriteInternal(w)
		return
	}

	versions, err := h.SitesSvc().ListVersionsBySite(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
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
	httpx.WriteJSON(w, http.StatusOK, out)
}

// setActiveVersion points `current` at the requested version and updates sites.active_version (rollback or
// roll-forward). Idempotent if the target is already active.
func (h *SiteHandler) setActiveVersion(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	var req struct {
		VersionNumber int `json:"version_number"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &req) {
		return
	}
	if req.VersionNumber < 1 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "version_number must be >= 1"})
		return
	}

	expectedID, checkID := r.Context().Value(web.AdminSiteKey{}).(string)
	ifMatch, ok := h.ifMatchVersion(w, r)
	if !ok {
		return
	}
	result, err := h.SitesSvc().Rollback(r.Context(), sites.RollbackInput{User: user, Name: siteName, ExpectedID: expectedID, CheckID: checkID, Version: req.VersionNumber, IfMatch: ifMatch})
	if err != nil {
		var gateErr *sites.GateError
		if errors.As(err, &gateErr) {
			writeRollbackGateError(w, gateErr)
		} else {
			writeSitesError(w, err, siteName)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.siteResponseFromState(result.Site, result.ResponseState.(siteResponseState)))
}

// setVisibility toggles a site's showcase visibility ('public' | 'unlisted'); owner-only via GetSiteByUser.
func (h *SiteHandler) setVisibility(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
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
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "visibility must be 'public' or 'unlisted'"})
		return
	}

	if err := h.SitesSvc().SetVisibility(r.Context(), sites.VisibilityInput{UserID: user.ID, Name: siteName, Visibility: vis}); err != nil {
		writeSitesError(w, err, siteName)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"name": siteName, "visibility": vis})
}

// listSites serves GET /v1/sites. Every caller, the admin included, sees only their own sites by default;
// ?all=1 is an admin-only switch to every account's sites.
func (h *SiteHandler) listSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	// ?all=1 widens an admin's own list to every account's sites. Silently
	// narrowing a non-admin's all=1 to their own sites would be a lie about
	// what was returned; refuse it instead (same rule as /v1/analytics/sites).
	allAccounts := r.URL.Query().Get("all") == "1"
	if allAccounts && !user.IsAdmin {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "admin only"})
		return
	}
	h.noteOwnerBrowser(r, user)

	var sites []model.Site
	var err error

	if allAccounts {
		sites, err = h.siteReporting().SiteListing(r.Context())
	} else {
		sites, err = h.SitesSvc().ListSitesByUser(r.Context(), user.ID)
	}
	if err == nil && user.Member != nil && user.Member.Edits != nil {
		kept := sites[:0]
		for _, s := range sites {
			if slices.Contains(user.Member.Edits, strings.ToLower(s.Name)) {
				kept = append(kept, s)
			}
		}
		sites = kept
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	sites, filterErr := filterSiteList(sites, r.URL.Query())
	if filterErr != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, *filterErr)
		return
	}

	flagsFor := user.ID
	if allAccounts {
		flagsFor = ""
	}
	kept, err := h.SitesSvc().ListSiteKeep(r.Context(), flagsFor)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	prefs, err := h.SitesSvc().ShowcasePreferences(r.Context(), flagsFor)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// Views exist only where the analytics log is read; elsewhere the field
	// is left out rather than showing zeros nobody measured.
	var views map[string]operations.SiteViews
	if h.trafficOn {
		views, err = h.Reporting.PersonViewsBySite(r.Context(), flagsFor, allAccounts)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	response := make([]SiteResponse, 0, len(sites))
	for _, site := range sites {
		resp := h.ToSiteResponse(site, "")
		if views != nil {
			v := views[site.ID] // zero when the site had no people views
			resp.Views = &v
		}
		footprint, err := h.Disk.SiteFootprint(site.UserID, site.Name)
		if err != nil {
			httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not measure website files"})
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

	httpx.WriteJSON(w, http.StatusOK, response)
}

// adminUsers returns every registered user with their sites nested (admin only). API keys are never included.
func (h *SiteHandler) adminUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}

	users, err := h.siteReporting().SiteAccounts(r.Context())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	listing, err := h.siteReporting().SiteListing(r.Context())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// A self sign-up's users.username is a synthetic, never-shown value
	// (CreatePasswordAccount): the admin page shows each password
	// account's own login instead.
	logins, err := h.siteReporting().SiteAccountLogins(r.Context())
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	byUser := make(map[string][]map[string]any, len(users))
	for _, s := range listing {
		m := h.adminSiteRow(s)
		byUser[s.UserID] = append(byUser[s.UserID], m)
	}

	// Earlier handles, only needed to match MAX_SITES_OVERRIDES and
	// MAX_ARCHIVE_MB_OVERRIDES.
	var aliases map[string][]string
	if l := h.Limits; l.MaxSitesOverrides != "" || l.MaxArchiveOverrides != "" {
		if aliases, err = h.siteReporting().SiteAccountAliases(r.Context()); err != nil {
			httpx.WriteInternal(w)
			return
		}
	}

	out := make([]map[string]any, 0, len(users))
	present := map[string]bool{}
	for _, u := range users {
		group := web.SignupGroupOf(u.Signup.Source)
		present[group] = true
		list := byUser[u.ID]
		if list == nil {
			list = []map[string]any{}
		}
		out = append(out, map[string]any{
			"id":       u.ID,
			"username": u.Username,
			// Login is the account's own login (account_passwords.login), "" with no password row. A self sign-up's
			// Username above is synthetic, so the page must show Login whenever present.
			"login":            logins[u.ID],
			"handle":           u.Handle.String,
			"page_url":         h.PersonPageURL(u.Handle.String),
			"display_name":     u.DisplayName.String,
			"is_admin":         u.IsAdmin,
			"created_at":       u.CreatedAt,
			"site_count":       len(list),
			"max_sites":        accounts.MaxSitesOf(h.Limits, u, aliases[u.ID]),
			"max_site_mb":      sites.SiteLimitMB(h.Limits, append([]string{u.Handle.String}, aliases[u.ID]...)...),
			"sites":            list,
			"suspended":        u.Suspended,
			"suspended_reason": u.SuspendedReason,
			// Where the account came from (users.signup_*); "" for accounts from before it was recorded.
			"signup_source":   u.Signup.Source,
			"signup_agent":    u.Signup.Agent,
			"signup_method":   u.Signup.Method,
			"signup_inferred": u.Signup.Inferred,
			"signup_group":    group,
			"signup_detail":   u.Signup.Detail,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": out, "user_count": len(byUser), "site_count": len(listing), "signup_groups": web.SignupGroups(present)})
}

// readAndValidateFiles reads the archive body, verifies an optional X-Content-Digest against its SHA-256,
// validates the entries, and returns the files plus the hex digest (recorded on the version row).
func (h *SiteHandler) readAndValidateFiles(w http.ResponseWriter, r *http.Request, siteName string) (map[string][]byte, string, error) {
	limit, override := accounts.New(h.Database).SiteLimit(r.Context(), auth.GetUser(r.Context()), h.Limits, sites.Upload.Body)
	body, err := ReadLimitedBody(w, r, limit)
	if err != nil {
		return nil, "", err
	}

	files, digest, err := h.SitesSvc().PrepareArchive(sites.ArchivePreparationInput{
		Body: body, Filename: archiveFilename(siteName, body), ContentDigest: r.Header.Get("X-Content-Digest"),
		Limit: limit, Override: override,
	})
	if err != nil {
		h.writePreparationError(w, err, limit, true)
		return nil, "", err
	}
	return files, digest, nil
}

func ReadLimitedBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeTooLarge(w, limit, "")
			return nil, err
		}

		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid request body"})
		return nil, err
	}

	if len(body) == 0 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "request body is required"})
		return nil, errors.New("empty request body")
	}

	return body, nil
}

func writeTooLarge(w http.ResponseWriter, limit int64, detail string) {
	e := sites.TooLarge(limit, detail)
	httpx.WriteJSON(w, http.StatusRequestEntityTooLarge, httpx.ErrorResponse{Error: e.Message, Code: e.Code, Hint: sites.StorageTips})
}

func archiveFilename(siteName string, body []byte) string {
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		return siteName + ".zip"
	}

	return siteName + ".tar.gz"
}

// ifMatchVersion reads the optional If-Match header of a deploy, update or rollback: the active version the
// caller last read, "3" or 3. Agents send it on sites other people also publish so two members never
// overwrite each other unknowingly. 0 when absent or *; a malformed value is refused (400 invalid_if_match).
// Read only where teams are (the Simple Host program).
func (h *SiteHandler) ifMatchVersion(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" || raw == "*" || !h.Ed.Identity().PersonalSites {
		return 0, true
	}
	n, err := strconv.Atoi(strings.Trim(strings.TrimPrefix(raw, "W/"), `"`))
	if err != nil || n < 1 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `If-Match must be the site's active version in quotes, e.g. If-Match: "3"`, Code: "invalid_if_match"})
		return 0, false
	}
	return n, true
}

func (h *SiteHandler) HTTPCaches() workers.Owner { return &h.Caches }
