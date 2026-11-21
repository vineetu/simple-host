package siteshttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// Idle-site cleanup (no emails are sent).
//
// A site with no visits by people, no new version and no saved-data or list
// write for idleAfter + idleGrace (90 + 30 days by default) moves to Recently
// deleted (the usual 7-day restore window), unless its owner marked it Keep
// (owner app, PUT /v1/sites/{site}/keep, MCP keep_site). Nobody is emailed.
// Exempt: sites with a custom domain or a claimed name, admin, reviewer and
// event accounts' sites, handles in IDLE_CLEANUP_EXEMPT_HANDLES, taken-down
// sites and suspended accounts, sites their owner took offline, preview sites
// (repository/sites/ops_idle.go has the rule). Activity is a person's visit, a
// new version, a saved-data or list write, or a restore.
//
// Off unless IDLE_CLEANUP=on. The admin page's dry run (GET
// /v1/admin/idle-sites) lists what a run would do either way. A removal
// re-checks every condition in the transaction that moves the site, so a Keep,
// deploy or domain that lands mid-run wins. Nothing runs while visit data
// cannot be trusted: fewer than idleAfter + idleGrace days of analytics on
// record, or an ingester that has not run for six hours.

const idleRunEvery = 6 * time.Hour

func (h *SiteHandler) SetIdleCleanup(on bool) { h.idleCleanup = on }

// SetIdleExempt sets the accounts the cleanup never touches besides the
// built-in rules: handles (IDLE_CLEANUP_EXEMPT_HANDLES) and the plugin
// reviewer account (REVIEW_ACCOUNT_EMAIL).
func (h *SiteHandler) SetIdleExempt(handles []string, reviewerEmail string) {
	ex := sites.IdleExempt{ReviewerEmail: strings.TrimSpace(reviewerEmail), FamilySites: h.Limits.IdleExemptFamilySites}
	for _, hd := range handles {
		if hd = strings.ToLower(strings.TrimSpace(hd)); hd != "" {
			ex.Handles = append(ex.Handles, hd)
		}
	}
	h.IdleExempt = ex
}

func (h *SiteHandler) startIdleCleanup(ctx context.Context) {
	o := h.siteLoop("sites.idle_cleanup")
	o.periodicTicker(ctx, 10*time.Minute, idleRunEvery, func(ctx context.Context) { h.RunIdleCleanup(ctx, time.Now()) })
}

func (h *SiteHandler) RunIdleCleanup(ctx context.Context, now time.Time) {
	h.Reporting.RunIdleCleanup(ctx, now, h.Ed, h.idleCleanup, h.IdleExempt, h.idleArchiveSvc())
}

func (h *SiteHandler) setSiteKeep(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	var req struct {
		Keep *bool `json:"keep"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Keep == nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `body must be {"keep": true} or {"keep": false}`})
		return
	}
	site, err := h.opsSitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	if web.RefuseSuspendedSite(w, site) {
		return
	}
	if err := h.Reporting.SetSiteKeep(r.Context(), site.ID, *req.Keep); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": site.Name, "keep": *req.Keep})
}

type idleSiteRow struct {
	Site         string    `json:"site"`
	Owner        string    `json:"owner"`
	LastActivity time.Time `json:"last_activity"`
}

// adminIdleSites is the dry run: what a run would move to Recently deleted, whether or not cleanup is on.
func (h *SiteHandler) adminIdleSites(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	now := time.Now()
	trusted, why, since, last, err := h.Reporting.IdleEvidenceStatus(r.Context(), now, h.Ed)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	const show = 500
	remove, err := h.Reporting.ListIdleSitesToRemove(r.Context(), h.IdleExempt, now.Add(-operations.IdleRemoveAfter(h.Ed)), show)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	rows := make([]idleSiteRow, 0, len(remove))
	for _, s := range remove {
		rows = append(rows, idleSiteRow{Site: s.Name, Owner: s.OwnerEmail, LastActivity: s.LastActivity.UTC()})
	}
	resp := map[string]any{
		"enabled":       h.idleCleanup,
		"idle_days":     int(operations.IdleRemoveAfter(h.Ed).Hours() / 24),
		"visit_data_ok": trusted,
		"would_remove":  rows,
	}
	if !trusted {
		resp["visit_data_note"] = why
	}
	if !since.IsZero() {
		resp["visits_recorded_since"] = since.UTC()
	}
	if !last.IsZero() {
		resp["visits_last_read"] = last.UTC()
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (h *SiteHandler) idleArchiveSvc() *sites.Service {
	return sites.New(h.Database, h.Disk, nil, 0, sites.Deps{
		Locks: &h.UploadLocks, DeletedRetention: h.Limits.DeletedRetention,
		IdleArchive: sites.IdleArchiveDeps{
			Exempt: sites.IdleExempt{Handles: h.IdleExempt.Handles, ReviewerEmail: h.IdleExempt.ReviewerEmail, FamilySites: h.IdleExempt.FamilySites},
			After:  operations.IdleRemoveAfter(h.Ed),
		},
	})
}
