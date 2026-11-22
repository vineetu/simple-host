package idle

import (
	"context"
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// routes are the idle cleanup's handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts the operator's dry run, GET /v1/admin/idle-sites.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/admin/idle-sites", authMiddleware(http.HandlerFunc(routes{h}.adminIdleSites)))
}

// Run is one idle-cleanup pass (host/ops RunIdleCleanup) on the instance's
// settings; the site workers call it every idleRunEvery.
func Run(ctx context.Context, h *sitesroute.Handler, now time.Time) {
	h.Reporting.RunIdleCleanup(ctx, now, h.Ed, h.IdleCleanup, h.IdleExempt, archiveSvc(h))
}

type idleSiteRow struct {
	Site         string    `json:"site"`
	Owner        string    `json:"owner"`
	LastActivity time.Time `json:"last_activity"`
}

// adminIdleSites is the dry run: what a run would move to Recently deleted, whether or not cleanup is on.
func (h routes) adminIdleSites(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	const show = 500
	run, err := h.Reporting.IdleCleanupDryRun(r.Context(), time.Now(), h.Ed, h.IdleExempt, show)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	rows := make([]idleSiteRow, 0, len(run.WouldRemove))
	for _, s := range run.WouldRemove {
		rows = append(rows, idleSiteRow{Site: s.Name, Owner: s.OwnerEmail, LastActivity: s.LastActivity.UTC()})
	}
	trusted, why, since, last := run.Trusted, run.Why, run.Since, run.LastRead
	resp := map[string]any{
		"enabled":       h.IdleCleanup,
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

// archiveSvc is the service an idle removal runs on: the locks, the
// Recently deleted window and the idle rule.
func archiveSvc(h *sitesroute.Handler) *sites.Service {
	return sites.New(h.Database, h.Disk, nil, 0, sites.Deps{
		Locks: &h.UploadLocks, DeletedRetention: h.Limits.DeletedRetention,
		IdleArchive: sites.IdleArchiveDeps{
			Exempt: sites.IdleExempt{Handles: h.IdleExempt.Handles, ReviewerEmail: h.IdleExempt.ReviewerEmail, FamilySites: h.IdleExempt.FamilySites},
			After:  operations.IdleRemoveAfter(h.Ed),
		},
	})
}
