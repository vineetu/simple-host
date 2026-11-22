package admin

import (
	"net/http"
	"strconv"
	"time"

	operations "github.com/vsriram/simple-host/host/ops"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

// routes are the operator's report handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts the operator's site and account lists and the usage
// report. siteOpLimiter is the shared per-address limiter; it sits outside
// auth on /v1/admin/users so wrong keys typed at /admin count too.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
	a := routes{h}
	mux.Handle("GET /v1/admin/sites", authMiddleware(http.HandlerFunc(a.adminSites)))
	mux.Handle("GET /v1/admin/users", httpx.RateLimitByIP(siteOpLimiter, authMiddleware(http.HandlerFunc(a.adminUsers))))
	mux.Handle("GET /v1/admin/usage", authMiddleware(http.HandlerFunc(a.adminUsage)))
}

func (h routes) adminSites(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	q := r.URL.Query()
	key := q.Get("sort")
	if key == "" {
		key = "updated"
	}
	switch key {
	case "created", "updated", "size", "views", "live", "name", "owner":
	default:
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid sort"})
		return
	}
	page, err := strconv.Atoi(q.Get("page"))
	if q.Get("page") == "" {
		page, err = 0, nil
	}
	limit, ok := httpx.Limit(q, 50, 200)
	if err != nil || page < 0 || page > 1000000 || !ok {
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid pagination: page from 0, limit 1 to 200"})
		return
	}
	filter := q.Get("filter")
	switch filter {
	case "", "all", "down", "domain", "unlisted":
	default:
		httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: "invalid filter"})
		return
	}
	rows, total, measured, err := h.SiteReporting().AdminSites(r.Context(), h.Hosts, h.Disk, &h.Usage, operations.AdminSiteQuery{
		Search: q.Get("q"), Filter: filter, Sort: key, Ascending: q.Get("dir") == "asc", Page: page, Limit: limit,
	})
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, entry := range rows {
		m := h.AdminSiteRow(entry.Site)
		m["user_id"] = entry.Site.UserID
		m["size"] = entry.Usage.LiveBytes
		m["disk"] = entry.Usage.Bytes
		m["views"] = entry.Views
		out = append(out, m)
	}
	httpx.WriteJSON(w, 200, map[string]any{"sites": out, "total": total, "page": page, "limit": limit, "views_days": operations.AdminSiteViewDays, "measured_at": measured})
}

// adminUsers returns every registered user with their sites nested (admin only). API keys are never included.
func (h routes) adminUsers(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	report, err := h.SiteReporting().AdminAccounts(r.Context(), h.Limits)
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
func (h routes) adminUserRow(a operations.AdminAccount, group string) map[string]any {
	u := a.User
	list := make([]map[string]any, 0, len(a.Sites))
	for _, s := range a.Sites {
		list = append(list, h.AdminSiteRow(s))
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

// adminUsage (GET /v1/admin/usage) reports what this instance is using and what it has left. There is
// deliberately no projection of how many people or sites will fit: sites here have a median size of 25 KB
// against a 100 MB cap, so any figure derived from the cap is wrong by orders of magnitude.
func (h routes) adminUsage(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	report, err := h.Reporting.InstanceUsage(r.Context(), h.Disk, &h.Usage, h.Network)
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not read this server's disk"})
		return
	}
	usage, measuredAt := report.Disk, report.MeasuredAt
	out := map[string]any{
		"disk_bytes":      usage.DiskBytes,
		"disk_free_bytes": usage.DiskFreeBytes,
		"disk_used_bytes": usage.DiskUsedBytes,
		"disk_used_pct":   usage.DiskUsedPct,
		"site_bytes":      usage.SiteBytes,
		"sites":           usage.Sites,
		"accounts":        report.Accounts,
		"largest":         usage.Largest,
		"site_limit_mb":   sites.Upload.Body >> 20,
		// MAX_ARCHIVE_MB_OVERRIDES: handle -> MB, for the accounts that
		// have their own; null when none do.
		"site_limit_overrides": h.Limits.ArchiveOverrides(),
		// Settings and build in force, so "which Release is this box on" and
		// "how many deploys does it keep" are answered without a shell.
		"keep_versions": web.DefaultKeepVersions,
		"version":       buildinfo.Version,
		"commit":        buildinfo.Commit,
		"status":        usage.Status,
		"message":       usage.Message,
		// When the walk behind these figures ran. A served-stale reading is
		// better than a page that hangs, but only if the page can say so.
		"measured_at": measuredAt.UTC().Format(time.RFC3339),
		"network":     report.Network,
	}
	statuses := []workers.Status{}
	if h.WorkerStatus != nil {
		statuses = h.WorkerStatus()
	}
	out["workers"] = statuses
	if r.URL.Query().Get("sizes") == "1" {
		sizes := usage.All
		if sizes == nil {
			sizes = []operations.SiteUsage{}
		}
		out["site_sizes"] = sizes
	}
	httpx.WriteJSON(w, 200, out)
}
