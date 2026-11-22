package usage

import (
	"context"
	"net/http"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/httpx"
)

// storageUsage is the owner's storage usage report.
func storageUsage(s *data.Service, ctx context.Context, call data.Call) (map[string]any, error) {
	if !call.Owner {
		return nil, &data.Error{Code: "forbidden", Message: "owner credential required"}
	}
	u, err := s.MeasureSiteStorage(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return nil, err
	}
	limit := data.SiteLimitBytes(s.Deps.SiteLimit, s.Deps.CombinedPool)
	used := s.BudgetUsed(u)
	remaining := max(int64(0), limit-used)
	return map[string]any{"used_bytes": used, "limit_bytes": limit, "remaining_bytes": remaining, "files_used_bytes": u.Files, "files_limit_bytes": s.FilesLimitBytes(), "files_remaining_bytes": s.FilesRemaining(u), "breakdown": map[string]int64{"kv_bytes": u.KV, "sqlite_bytes": u.SQLite, "files_bytes": u.Files}}, nil
}

// Register maps GET .../storage/usage behind caller (Access's StorageCaller).
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/sites/{sitename}/storage/usage", h.StorageRouteFor(caller, getStorageUsage))
}

func getStorageUsage(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	if !data.StorageOwner(w, c) {
		return
	}
	result, err := storageUsage(h.StorageSvc(), r.Context(), c)
	if err != nil {
		data.WriteStorageError(w, err, 0)
		return
	}
	httpx.WriteJSON(w, 200, result)
}
