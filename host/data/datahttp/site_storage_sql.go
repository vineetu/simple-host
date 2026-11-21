package datahttp

import (
	"net/http"
	"path/filepath"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
)

func (h *DataHandler) StorageRuntimeDir(c StorageCall) string {
	return filepath.Join(h.Disk.SiteDir(c.OwnerID, c.SiteName), "runtime")
}
func (h *DataHandler) StorageSQLPath(c StorageCall) string {
	return filepath.Join(h.StorageRuntimeDir(c), "sqlite", c.ResourceName+".sqlite")
}

// storageSQL serves POST .../sqlite/{name}/query, execute and schema.
func (h *DataHandler) storageSQL(w http.ResponseWriter, r *http.Request, c StorageCall) {
	c, ok := h.storageResourceFor(w, r, c, "sqlite")
	if !ok {
		return
	}
	mode := strings.TrimPrefix(r.URL.Path, "/v1/sites/"+r.PathValue("sitename")+"/storage/sqlite/"+c.ResourceName+"/")
	result, err := h.StorageFor(c).LegacySQL(r.Context(), appstorage.LegacySQLInput{Call: c, Mode: mode, Body: r.Body, ResultLimit: storageResultLimitBytes()})
	if err != nil {
		WriteStorageError(w, err, 400)
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

type sqliteGate = appstorage.SQLiteGate

func NewSQLiteGate(n int, wait time.Duration) *sqliteGate { return appstorage.NewSQLiteGate(n, wait) }
