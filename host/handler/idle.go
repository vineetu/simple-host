package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/app/operations"
	"github.com/vsriram/simple-host/host/app/sites"
	"github.com/vsriram/simple-host/host/auth"
)

// Idle-site cleanup (owner decision 2026-09-27; no emails since 2026-11-19).
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

// SetIdleCleanup turns the cleanup on (IDLE_CLEANUP=on).
func (h *SiteHandler) SetIdleCleanup(on bool) { h.idleCleanup = on }

// SetIdleExempt sets the accounts the cleanup never touches besides the
// built-in rules: handles (IDLE_CLEANUP_EXEMPT_HANDLES) and the plugin
// reviewer account (REVIEW_ACCOUNT_EMAIL).
func (h *SiteHandler) SetIdleExempt(handles []string, reviewerEmail string) {
	ex := sites.IdleExempt{ReviewerEmail: strings.TrimSpace(reviewerEmail), FamilySites: h.limits.IdleExemptFamilySites}
	for _, hd := range handles {
		if hd = strings.ToLower(strings.TrimSpace(hd)); hd != "" {
			ex.Handles = append(ex.Handles, hd)
		}
	}
	h.idleExempt = ex
}

func (h *SiteHandler) idleEvidenceOK(ctx context.Context, now time.Time) (bool, string, time.Time, time.Time, error) {
	return h.reporting.IdleEvidenceStatus(ctx, now, h.ed)
}

// StartIdleCleanup runs the cleanup shortly after boot and then every
// idleRunEvery, while IDLE_CLEANUP is on.
func (h *SiteHandler) StartIdleCleanup(ctx context.Context) {
	o := h.siteLoop("sites.idle_cleanup")
	o.once.Do(func() {
		if o.enabled() {
			h.startIdleCleanup(ctx)
		}
	})
}
func (h *SiteHandler) startIdleCleanup(ctx context.Context) {
	o := h.siteLoop("sites.idle_cleanup")
	o.periodicTicker(ctx, 10*time.Minute, idleRunEvery, func(ctx context.Context) { h.runIdleCleanup(ctx, time.Now()) })
}

func (h *SiteHandler) runIdleCleanup(ctx context.Context, now time.Time) {
	h.reporting.RunIdleCleanup(ctx, now, h.ed, h.idleCleanup, h.idleExempt, h.idleArchiveSvc())
}

// setSiteKeep PUT /v1/sites/{sitename}/keep {"keep": bool} — the Keep flag.
func (h *SiteHandler) setSiteKeep(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	var req struct {
		Keep *bool `json:"keep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Keep == nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `body must be {"keep": true} or {"keep": false}`})
		return
	}
	site, err := h.opsSitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if refuseSuspendedSite(w, site) {
		return
	}
	if err := h.reporting.SetSiteKeep(r.Context(), site.ID, *req.Keep); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": site.Name, "keep": *req.Keep})
}

type idleSiteRow struct {
	Site         string    `json:"site"`
	Owner        string    `json:"owner"`
	LastActivity time.Time `json:"last_activity"`
}

// adminIdleSites GET /v1/admin/idle-sites — the dry run: which sites a run
// would move to Recently deleted, now, whether or not the cleanup is on.
func (h *SiteHandler) adminIdleSites(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	now := time.Now()
	trusted, why, since, last, err := h.idleEvidenceOK(r.Context(), now)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	const show = 500
	remove, err := h.reporting.ListIdleSitesToRemove(r.Context(), h.idleExempt, now.Add(-operations.IdleRemoveAfter(h.ed)), show)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	rows := make([]idleSiteRow, 0, len(remove))
	for _, s := range remove {
		rows = append(rows, idleSiteRow{Site: s.Name, Owner: s.OwnerEmail, LastActivity: s.LastActivity.UTC()})
	}
	resp := map[string]any{
		"enabled":       h.idleCleanup,
		"idle_days":     int(operations.IdleRemoveAfter(h.ed).Hours() / 24),
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
	writeJSON(w, http.StatusOK, resp)
}
