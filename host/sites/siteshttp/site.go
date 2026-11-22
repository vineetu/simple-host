package siteshttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/sites/idempotency"
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
	var req struct {
		Origins []string `json:"origins"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body (expected {\"origins\":[\"https://you.github.io\"]})"})
		return
	}
	clean, err := h.SitesSvc().SetAllowedOrigins(r.Context(), user.ID, siteName, req.Origins)
	var siteErr *sites.Error
	if errors.As(err, &siteErr) && (siteErr.Code == "invalid_origin" || siteErr.Code == "too_many_origins") {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: siteErr.Message})
		return
	}
	if err != nil {
		writeSitesError(w, err, siteName)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"site": siteName, "allowed_origins": clean})
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

	expectedID, checkID := r.Context().Value(web.AdminSiteKey{}).(string)
	// ?pending=1: only versions stored with publish=false that nobody has
	// made live yet, the ones discardVersion can remove.
	versions, err := h.SitesSvc().ListVersions(r.Context(), sites.VersionListInput{UserID: user.ID, Name: siteName, ExpectedID: expectedID, CheckID: checkID, Pending: pendingParam(r)})
	if err != nil {
		writeSitesError(w, err, siteName)
		return
	}
	out := make([]versionResponse, 0, len(versions))
	for _, v := range versions {
		out = append(out, versionResponse{VersionNumber: v.VersionNumber, Status: v.Status, Author: v.Author, CreatedAt: v.CreatedAt, IsActive: v.Active})
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
	result, err := h.SitesSvc().Rollback(r.Context(), sites.RollbackInput{User: user, Name: siteName, ExpectedID: expectedID, CheckID: checkID, Version: req.VersionNumber, IfMatch: r.Header.Get("If-Match")})
	if err != nil {
		var gateErr *sites.GateError
		if errors.As(err, &gateErr) {
			writeRollbackGateError(w, gateErr)
		} else {
			writeSitesError(w, err, siteName)
		}
		return
	}
	if result.Moved {
		h.emitDeployed(result.Site.ID, result.Site.ActiveVersion, result.PreviousVersion, "active_version")
	}
	httpx.WriteJSON(w, http.StatusOK, h.siteResponse(result.Site, result.FileBytes))
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

	rows, err := h.SitesSvc().ListSites(r.Context(), sites.SiteListInput{User: user, All: allAccounts, Params: r.URL.Query(), Views: h.trafficOn})
	var siteErr *sites.Error
	switch {
	case errors.As(err, &siteErr) && siteErr.Code == "measure_failed":
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: siteErr.Message})
		return
	case errors.As(err, &siteErr):
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	response := make([]SiteResponse, 0, len(rows))
	for _, row := range rows {
		resp := h.siteResponse(row.Site, row.FileBytes)
		resp.Views, resp.Pinned, resp.Order, resp.Keep = row.Views, row.Pinned, row.Order, row.Keep
		resp.Owner, resp.AccessRole = row.Owner, row.Access
		if row.Site.Name == r.URL.Query().Get("files") {
			resp.Files = liveVersionFiles(r.Context(), h.Disk, row.Site)
		}
		response = append(response, resp)
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}

// adminUsers returns every registered user with their sites nested (admin only). API keys are never included.
func (h *SiteHandler) adminUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	report, err := h.siteReporting().AdminAccounts(r.Context(), h.Limits)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(report.Accounts))
	present := map[string]bool{}
	for _, a := range report.Accounts {
		group := web.SignupGroupOf(a.User.Signup.Source)
		present[group] = true
		out = append(out, h.adminUserRow(a, group))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": out, "user_count": report.OwnerCount, "site_count": report.SiteCount, "signup_groups": web.SignupGroups(present)})
}

// adminUserRow is one account on the admin Users page, its sites nested.
func (h *SiteHandler) adminUserRow(a operations.AdminAccount, group string) map[string]any {
	u := a.User
	list := make([]map[string]any, 0, len(a.Sites))
	for _, s := range a.Sites {
		list = append(list, h.adminSiteRow(s))
	}
	return map[string]any{
		"id":               u.ID,
		"username":         u.Username,
		"login":            a.Login,
		"handle":           u.Handle.String,
		"page_url":         h.PersonPageURL(u.Handle.String),
		"display_name":     u.DisplayName.String,
		"is_admin":         u.IsAdmin,
		"created_at":       u.CreatedAt,
		"site_count":       len(list),
		"max_sites":        a.MaxSites,
		"max_site_mb":      a.MaxSiteMB,
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
	}
}

func (h *SiteHandler) HTTPCaches() workers.Owner { return &h.Caches }
