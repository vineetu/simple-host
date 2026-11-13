package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/model"
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
	if e, ok := err.(*appstorage.Error); ok {
		writeTableError(w, e)
	} else {
		storageError(w, 500, "internal_error", "internal server error")
	}
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
	call.Resource.Tables = make(map[string]model.TableRule, len(c.resource.Tables))
	for name, rule := range c.resource.Tables {
		call.Resource.Tables[name] = model.TableRule{Matrix: model.AccessMatrix(rule.Matrix), PresetBase: rule.PresetBase}
	}
	svc := h.storageRequestSvc(r, c)
	result, err := svc.LegacySQL(r.Context(), appstorage.LegacySQLInput{Call: call, Mode: mode, ReadBody: func() ([]byte, error) { return io.ReadAll(io.LimitReader(r.Body, (64<<10)+1)) }, ResultLimit: storageResultLimitBytes()})
	if len(result.Refusals) != 0 {
		writeStorageRefusals(w, result.Refusals)
	}
	if err != nil {
		e := err.(*appstorage.Error)
		if len(e.Refusals) != 0 {
			writeStorageRefusals(w, e.Refusals)
		} else {
			writeLegacySQLError(w, e)
		}
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

func writeLegacySQLError(w http.ResponseWriter, e *appstorage.Error) {
	status := 400
	switch e.Code {
	case "query_too_expensive":
		status = 422
	case "internal_error", "storage_failed":
		status = 500
	case "site_full", "sqlite_full":
		status = 507
	case "resource_changed", "table_preset_rename", "sql_unique_failed":
		status = 409
	case "sql_visitor_rule", "fixed_routes_required", "forbidden", "site_locked", "site_private", "csrf_required":
		status = 403
	case "not_found":
		status = 404
	case "storage_busy":
		if e.Message == "storage is busy; retry shortly" {
			storageBusy(w)
			return
		}
		status = 503
	}
	storageError(w, status, e.Code, e.Message)
}
