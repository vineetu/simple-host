package siteshttp

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/httpx"
	operations "github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	"github.com/vsriram/simple-host/host/workers"
)

func (h *SiteHandler) SetWorkerStatus(status func() []workers.Status) { h.workerStatus = status }

// adminUsage (GET /v1/admin/usage) reports what this instance is using and what it has left. There is
// deliberately no projection of how many people or sites will fit: sites here have a median size of 25 KB
// against a 100 MB cap, so any figure derived from the cap is wrong by orders of magnitude.
func (h *SiteHandler) adminUsage(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	usage, measuredAt, err := h.usage.Get(h.Disk.DataDir(), h.Disk.Changes())
	if err != nil {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: "could not read this server's disk"})
		return
	}
	accounts := h.Reporting.AccountCount(r.Context())
	out := map[string]any{
		"disk_bytes":      usage.DiskBytes,
		"disk_free_bytes": usage.DiskFreeBytes,
		"disk_used_bytes": usage.DiskUsedBytes,
		"disk_used_pct":   usage.DiskUsedPct,
		"site_bytes":      usage.SiteBytes,
		"sites":           usage.Sites,
		"accounts":        accounts,
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
		"network":     h.Network.Report(r.Context(), time.Now()),
	}
	statuses := []workers.Status{}
	if h.workerStatus != nil {
		statuses = h.workerStatus()
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

func (h *SiteHandler) SetNetworkUsage(iface string)      { h.Network.SetNetworkUsage(iface) }
func (h *SiteHandler) SetTrafficLog(on bool)             { h.trafficOn = on; h.Network.SetTrafficLog(on) }
func (h *SiteHandler) NetworkUsageWorker() workers.Owner { return h.Network.NetworkUsageWorker() }
