package handler

import (
	"encoding/json"
	"errors"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	"io"
	"net/http"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

// Keep the previous 4096-page (256 MiB) per-connection memory ceiling after
// moving from the wazero interpreter to the compiled-Go SQLite engine.
const storageSQLiteMaxMemory = 4096 * 65536

func storageSQLTables(c *sqlite3.Conn) (map[string]bool, error) { return storage.SQLTables(c) }

func storageTableInfo(conn *sqlite3.Conn, table string) (map[string]string, []string, error) {
	return storage.TableInfo(conn, table)
}

// openStorageTableConn opens the database for one table route.
func (h *SiteHandler) openStorageTableConn(w http.ResponseWriter, r *http.Request, c storageCall, write bool) (*sqlite3.Conn, func(), bool) {
	timeout := h.limits.StorageOwnerTimeout
	if !c.owner {
		timeout = h.limits.StorageVisitorQueryTimeout
		if write {
			timeout = h.limits.StorageVisitorWriteTimeout
		}
	}
	conn, done, err := storage.OpenTableConn(r.Context(), h.storageSQLPath(c), write, h.sqliteGateFor(), timeout, storageSQLiteMaxMemory, func() (int64, error) {
		usage, err := h.measureSiteStorage(r.Context(), c)
		return storageSiteLimitBytes(h.ed.Storage().CombinedPool) - h.storageBudgetUsed(usage), err
	})
	switch err {
	case nil:
		return conn, done, true
	case storage.ErrUnknownTable:
		storageError(w, 400, "invalid_rows", "unknown table")
	case storage.ErrTableFull:
		storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
	case storage.ErrSQLiteBusy:
		storageBusy(w)
	default:
		storageError(w, 500, "internal_error", "internal server error")
	}
	return nil, nil, false
}

func (h *SiteHandler) storageRuntimeDir(c storageCall) string {
	return storage.RuntimeDir(h.disk, c.ownerID, c.siteName)
}
func (h *SiteHandler) storageSQLPath(c storageCall) string {
	return storage.RuntimeSQLitePath(h.disk, c.ownerID, c.siteName, c.resourceName)
}

// stageStorageRuntime moves bytes out of the live resource name before the
// PostgreSQL declaration is deleted. The caller restores on DB failure and
// removes the staging directory only after commit.
func (h *SiteHandler) stageStorageRuntime(c storageCall) (string, func(), error) {
	return storage.StageRuntime(h.disk, c.ownerID, c.siteName, c.resourceName, c.resource.Kind)
}

func (h *SiteHandler) storageSQL(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "sqlite")
	if !ok {
		return
	}
	mode := strings.TrimPrefix(r.URL.Path, "/v1/sites/"+r.PathValue("sitename")+"/storage/sqlite/"+c.resourceName+"/")
	call := h.storageServiceCall(c)
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	call.Resource.Tables = make(map[string]rules.TableRule, len(c.resource.Tables))
	for name, rule := range c.resource.Tables {
		call.Resource.Tables[name] = rules.TableRule{Matrix: rules.AccessMatrix(rule.Matrix), PresetBase: rule.PresetBase}
	}
	svc := h.storageRequestSvc(r, c)
	auth := svc.AuthorizeLegacySQL(r.Context(), call, mode)
	if auth.Error != nil {
		if len(auth.Error.Refusals) != 0 {
			writeStorageRefusals(w, auth.Error.Refusals)
		} else {
			writeLegacySQLError(w, auth.Error)
		}
	}
	if !auth.Allowed() {
		return
	}
	var req struct {
		SQL    string            `json:"sql"`
		Params []json.RawMessage `json:"params"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := dec.Decode(&req); err != nil || dec.Decode(new(any)) != io.EOF {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			storageError(w, 400, "sql_request", "the request body is over 64 KB: send a shorter statement, or fewer or smaller params")
			return
		}
		storageError(w, 400, "sql_request", `the body must be one JSON object: {"sql": "one statement", "params": [values for each ?]}`)
		return
	}
	if e := appstorage.ValidateLegacySQL(req.SQL, req.Params); e != nil {
		storageError(w, 400, e.Code, e.Message)
		return
	}
	result, err := svc.LegacySQL(r.Context(), appstorage.LegacySQLInput{Authorization: auth, SQL: req.SQL, Params: req.Params, ResultLimit: storageResultLimitBytes()})
	if err != nil {
		writeLegacySQLError(w, err.(*appstorage.Error))
		return
	}
	writeJSON(w, 200, result)
}

func bindStorageParam(stmt *sqlite3.Stmt, i int, raw json.RawMessage) error {
	return storage.BindStorageParam(stmt, i, raw)
}
func storageColumn(stmt *sqlite3.Stmt, i int) any { return storage.StorageColumn(stmt, i) }
func storageScalar(raw json.RawMessage) bool      { return storage.StorageScalar(raw) }

var errStorageBusy = storage.ErrSQLiteBusy

type sqliteGate = storage.SQLiteGate

func newSQLiteGate(n int, wait time.Duration) *sqliteGate { return storage.NewSQLiteGate(n, wait) }

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

func createIfNotExists(sql string) bool { return storage.CreateIfNotExists(sql) }

func writeLegacySQLError(w http.ResponseWriter, e *appstorage.Error) {
	status := 400
	switch e.Code {
	case "internal_error":
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
