package datahttp

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
)

// StorageInspection is the catalog surface needed by the visitor-tool transport.
// The storage service owns its connection, deadline, budget and Release function.
type StorageInspection interface {
	Tables() (map[string]bool, error)
	TableInfo(string) (map[string]string, []string, error)
}

func StorageSQLTables(c StorageInspection) (map[string]bool, error) { return c.Tables() }
func StorageTableInfo(c StorageInspection, table string) (map[string]string, []string, error) {
	return c.TableInfo(table)
}

// InspectStorage runs catalog work while the storage service holds admission.
func (h *DataHandler) InspectStorage(w http.ResponseWriter, r *http.Request, c StorageCall, write bool, fn func(StorageInspection) error) bool {
	err := h.StorageFor(c).Inspect(r.Context(), c, write, func(i *appstorage.Inspection) error { return fn(i) })
	if err == nil {
		return true
	}
	WriteStorageError(w, err, 400)
	return false
}

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
	readBody := func() ([]byte, error) { return io.ReadAll(io.LimitReader(r.Body, (64<<10)+1)) }
	result, err := h.StorageFor(c).LegacySQL(r.Context(), appstorage.LegacySQLInput{Call: c, Mode: mode, ReadBody: readBody, ResultLimit: storageResultLimitBytes()})
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
