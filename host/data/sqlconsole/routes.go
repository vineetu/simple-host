package sqlconsole

import (
	"net/http"
	"os"
	"strings"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/httpx"
)

func storageResultLimitBytes() int {
	return data.ResultLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES"))
}

// storageSQL serves POST .../sqlite/{name}/query, execute and schema.
func storageSQL(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	c, ok := h.StorageResourceFor(w, r, c, "sqlite")
	if !ok {
		return
	}
	mode := strings.TrimPrefix(r.URL.Path, "/v1/sites/"+r.PathValue("sitename")+"/storage/sqlite/"+c.ResourceName+"/")
	result, err := LegacySQL(h.StorageFor(c), r.Context(), LegacySQLInput{Call: c, Mode: mode, Body: r.Body, ResultLimit: storageResultLimitBytes()})
	if err != nil {
		data.WriteStorageError(w, err, 400)
		return
	}
	switch {
	case result.Query:
		httpx.WriteJSON(w, 200, map[string]any{"columns": result.Columns, "rows": result.Rows})
	case result.Schema && len(result.Notes) > 0:
		httpx.WriteJSON(w, 200, map[string]any{"changes": result.Changes, "notes": result.Notes})
	case result.Schema:
		httpx.WriteJSON(w, 200, map[string]any{"changes": result.Changes})
	default:
		httpx.WriteJSON(w, 200, map[string]any{"changes": result.Changes, "last_insert_id": result.LastInsertID})
	}
}

// Register maps the SQL console routes behind caller (Access's StorageCaller).
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/query", h.StorageRouteFor(caller, storageSQL))
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/execute", h.StorageRouteFor(caller, storageSQL))
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/schema", h.StorageRouteFor(caller, storageSQL))
}
