package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
)

// storageInspection is the catalog surface needed by the visitor-tool transport.
// The storage service owns its connection, deadline, budget and release function.
type storageInspection interface {
	Tables() (map[string]bool, error)
	TableInfo(string) (map[string]string, []string, error)
}

func storageSQLTables(c storageInspection) (map[string]bool, error) { return c.Tables() }
func storageTableInfo(c storageInspection, table string) (map[string]string, []string, error) {
	return c.TableInfo(table)
}

// inspectStorage runs catalog work while the storage service holds admission.
func (h *SiteHandler) inspectStorage(w http.ResponseWriter, r *http.Request, c storageCall, write bool, fn func(storageInspection) error) bool {
	err := h.storageRequestSvc(r, c).Inspect(r.Context(), h.storageServiceCall(c), write, func(i *appstorage.Inspection) error { return fn(i) })
	if err == nil {
		return true
	}
	writeStorageError(w, err, 400)
	return false
}

func (h *SiteHandler) storageRuntimeDir(c storageCall) string {
	return filepath.Join(h.disk.SiteDir(c.ownerID, c.siteName), "runtime")
}
func (h *SiteHandler) storageSQLPath(c storageCall) string {
	return filepath.Join(h.storageRuntimeDir(c), "sqlite", c.resourceName+".sqlite")
}

func (h *SiteHandler) storageSQL(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "sqlite")
	if !ok {
		return
	}
	mode := strings.TrimPrefix(r.URL.Path, "/v1/sites/"+r.PathValue("sitename")+"/storage/sqlite/"+c.resourceName+"/")
	call := h.storageServiceCall(c)
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	svc := h.storageRequestSvc(r, c)
	result, err := svc.LegacySQL(r.Context(), appstorage.LegacySQLInput{Call: call, Mode: mode, ReadBody: func() ([]byte, error) { return io.ReadAll(io.LimitReader(r.Body, (64<<10)+1)) }, ResultLimit: storageResultLimitBytes()})
	if err != nil {
		writeStorageError(w, err, 400)
		return
	}
	var body map[string]any
	if result.Query {
		body = map[string]any{"columns": result.Columns, "rows": result.Rows}
	} else if result.Schema {
		body = map[string]any{"changes": result.Changes}
		if len(result.Notes) > 0 {
			body["notes"] = result.Notes
		}
	} else {
		body = map[string]any{"changes": result.Changes, "last_insert_id": result.LastInsertID}
	}
	writeJSON(w, 200, body)
}

func storageScalar(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return false
	}
	switch value.(type) {
	case nil, string, bool, json.Number:
		return true
	}
	return false
}

type sqliteGate = appstorage.SQLiteGate

func newSQLiteGate(n int, wait time.Duration) *sqliteGate { return appstorage.NewSQLiteGate(n, wait) }

// sqliteGateFor is h's SQLite concurrency gate. NewSiteHandler builds one
// from h.limits up front; a bare &SiteHandler{} (some test fixtures) has no
// gate yet, so this builds one lazily from the same limits snapshot rather
// than requiring every such fixture to call NewSiteHandler.
func (h *SiteHandler) sqliteGateFor() *sqliteGate {
	h.sqliteOnce.Do(func() {
		if h.sqlite == nil {
			h.sqlite = newSQLiteGate(h.limits.StorageSQLConcurrency, h.limits.StorageAcquireWait)
		}
	})
	return h.sqlite
}
