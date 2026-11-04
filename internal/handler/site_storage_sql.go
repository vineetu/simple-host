package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
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
	// Visitors never send SQL, except to a legacy full-mode database saved
	// before presets (storageResource.legacyVisitorSQL), until its owner
	// sets a preset on it.
	if !c.owner && !c.resource.legacyVisitorSQL() {
		storageError(w, 403, "fixed_routes_required", "pages use the table rows routes (table().list, get, add, edit, delete); SQL is for the owner's tools")
		return
	}
	if mode != "query" && mode != "execute" && mode != "schema" {
		storageError(w, 404, "not_found", "not found")
		return
	}
	if mode == "schema" {
		if !h.storageOwner(w, c) {
			return
		}
	} else if !h.storageAccess(w, r, &c, map[bool]string{true: "add", false: "read"}[mode == "execute"]) {
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
	var unlock func()
	if mode != "query" {
		var ok bool
		unlock, ok = h.storageWriteLock(w, r, c, true)
		if !ok {
			return
		}
		defer unlock()
	}
	dbPath := h.storageSQLPath(c)
	var usage siteStorageUsage
	var currentDBBytes int64
	newDB := false
	successful := false
	if mode != "query" {
		var e error
		usage, e = h.measureSiteStorage(r.Context(), c)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		currentDBBytes, newDB, e = storage.LegacySQLiteSize(dbPath)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if currentDBBytes == 0 && h.storageBudgetUsed(usage)+8192 > storageSiteLimitBytes(h.ed.Storage().CombinedPool) {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return
		}
		defer func() {
			if newDB && !successful {
				storage.RemoveNewSQLite(dbPath)
			}
		}()
		if err := storage.EnsureSQLiteDirectory(dbPath); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
	}
	uri := dbPath
	if mode == "query" {
		var err error
		uri, err = storage.LegacySQLiteQueryURI(dbPath)
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
	}
	release, err := h.sqliteGateFor().Acquire(r.Context())
	if err != nil {
		storageBusy(w)
		return
	}
	defer release()
	timeout := h.limits.StorageOwnerTimeout
	if !c.owner {
		timeout = h.limits.StorageVisitorQueryTimeout
		if mode != "query" {
			timeout = h.limits.StorageVisitorWriteTimeout
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	conn, err := storage.OpenLegacySQLite(ctx, uri, storageSQLiteMaxMemory, mode != "query")
	if conn != nil {
		defer conn.Close()
	}
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if mode != "query" {
		err = storage.SetLegacySQLiteLimit(conn, storageSiteLimitBytes(h.ed.Storage().CombinedPool)-(h.storageBudgetUsed(usage)-currentDBBytes))
		if errors.Is(err, storage.ErrTableFull) {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return
		}
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
	}
	var before map[string]bool
	// Every table that uses own (all of a legacy read-own database) keeps
	// the server-owned visitor_id column.
	ownSchema := mode == "schema" && (c.resource.Read == "own" || c.resource.Access != nil)
	if ownSchema {
		var e error
		before, e = storageSQLTables(conn)
		if e != nil || conn.Exec("BEGIN IMMEDIATE") != nil {
			storageError(w, 503, "storage_busy", "could not start the schema change because the database is busy; call once more in a second")
			return
		}
		defer func() { _ = conn.SetAuthorizer(nil); _ = conn.Exec("ROLLBACK") }()
	}
	var authorizerState storage.LegacyAuthorizerState
	// denial keeps the first rule the authorizer applied, so a refused
	// statement can say which one; writeTable names the table a write
	// targets, for a foreign key failure.
	var denial *sqlRefusal
	deny := func(why *sqlRefusal) sqlite3.AuthorizerReturnCode {
		if denial == nil {
			denial = why
		}
		return sqlite3.AUTH_DENY
	}
	err = storage.LegacyAuthorizer(conn, mode, c.resource.Read, c.visitorID, c.owner, &authorizerState, deny, sqlAuthorizerDenial, sqlVisitorReadDenial)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	fail := func(err error) {
		sqlFailure(ctx, conn, err, c.owner, req.SQL, authorizerState.WriteTable, authorizerState.WriteAction, timeout).write(w)
	}
	stmt, tail, err := conn.Prepare(req.SQL)
	if err != nil || stmt == nil {
		switch {
		case denial != nil:
			denial.write(w)
		case err != nil:
			fail(err)
		default:
			storageError(w, 400, "sql_request", "sql holds no statement (only blank space or comments): send one SQL statement")
		}
		return
	}
	defer stmt.Close()
	if second := sqlSecondStatement(tail); second != "" {
		sqlMultipleStatements(second).write(w)
		return
	}
	if len(req.Params) != stmt.BindCount() {
		sqlParamCount(stmt.BindCount(), len(req.Params)).write(w)
		return
	}
	// CREATE TRIGGER IF NOT EXISTS on a trigger that already exists compiles to
	// a read-only no-op, so the authorizer never sees a schema change (tables
	// and views still report theirs; an existing index compiles as a write and
	// stays refused); the owner's setup is meant to be rerunnable, so that
	// no-op passes the schema route.
	noopCreate := mode == "schema" && !authorizerState.DidSchema && !authorizerState.DidWrite && stmt.ReadOnly() && createIfNotExists(req.SQL)
	if refusal := sqlRouteMismatch(mode, req.SQL, stmt.ReadOnly(), authorizerState.DidWrite, authorizerState.DidSchema, noopCreate); refusal != nil {
		refusal.write(w)
		return
	}
	for i, raw := range req.Params {
		if err = bindStorageParam(stmt, i+1, raw); err != nil {
			storageError(w, 400, "sql_params", fmt.Sprintf("params[%d] could not be used: each param must be a string, a number that fits in 64 bits, true, false or null", i))
			return
		}
	}
	if mode == "query" {
		result, err := storage.LegacyQueryResult(stmt, storageResultLimitBytes())
		switch err {
		case storage.ErrLegacyQueryRows:
			storageError(w, 400, "result_too_large", "query returned more than 500 rows")
			return
		case storage.ErrLegacyQuerySize:
			storageError(w, 400, "result_too_large", "query result is too large")
			return
		case nil:
		default:
			fail(err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	err = stmt.Exec()
	if err != nil {
		if errors.Is(err, sqlite3.FULL) {
			storageError(w, 507, "sqlite_full", "SQLite cannot allocate more storage or row IDs")
			return
		}
		fail(err)
		return
	}
	var notes []string
	if ownSchema {
		if err = stmt.Close(); err == nil {
			err = conn.SetAuthorizer(nil)
		}
		// A table with its own preset must not lose it to a rename: the new
		// name would fall back to the database's preset, which may be wider.
		var after map[string]bool
		if err == nil && c.resource.Access != nil {
			if after, err = storageSQLTables(conn); err == nil {
				renamed, added := storage.SchemaTableChanges(before, after, func(t string) bool {
					_, ruled := c.resource.Tables[strings.ToLower(t)]
					return ruled
				})
				if renamed && len(added) > 0 {
					storageError(w, 409, "table_preset_rename", "this table has its own preset; give the new name its preset first (storage_set_resource tables), then rename")
					return
				}
				for _, t := range added {
					m := c.resource.tableMatrix(t)
					if _, ruled := c.resource.Tables[strings.ToLower(t)]; ruled {
						notes = append(notes, "new table "+t+" uses the preset saved for it ("+storagePresetName(m)+")")
					} else if m != (storageAccessMatrix{"owner", "owner", "owner", "owner"}) {
						notes = append(notes, "new table "+t+" uses the database's preset ("+storagePresetName(m)+"); give it its own with storage_set_resource tables if it should be narrower")
					}
				}
			}
		}
		if err == nil {
			err = storage.EnsureOwnTables(conn, before, c.resource.ownTables())
		}
		if err == nil {
			err = storage.EnsureHoldTables(conn, c.resource.holds)
		}
		if err != nil {
			storageError(w, 400, "visitor_id_required", "every table whose preset uses own or lets visitors add must keep its visitor_id TEXT column; only new tables receive it automatically, so do not drop or rename it")
			return
		}
		if err = conn.Exec("COMMIT"); err != nil {
			fail(err)
			return
		}
	}
	if authorizerState.DidWrite || authorizerState.DidSchema {
		paperHackWrite(r)
	}
	successful = true
	if err = stmt.Close(); err != nil {
		storageError(w, 500, "internal_error", "SQL commit failed")
		return
	}
	// Committed already: a failed checkpoint must not read as a failed
	// change (a retried INSERT would be a duplicate).
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", c.siteID, c.resourceName, err)
	}
	h.disk.MarkChanged()
	unlock()
	if mode == "schema" {
		resp := map[string]any{"changes": conn.Changes()}
		if len(notes) > 0 {
			resp["notes"] = notes
		}
		writeJSON(w, 200, resp)
	} else {
		writeJSON(w, 200, map[string]any{"changes": conn.Changes(), "last_insert_id": conn.LastInsertRowID()})
	}
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
