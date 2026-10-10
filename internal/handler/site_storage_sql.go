package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/vsriram/simple-host/internal/config"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// Keep the previous 4096-page (256 MiB) per-connection memory ceiling after
// moving from the wazero interpreter to the compiled-Go SQLite engine.
const storageSQLiteMaxMemory = 4096 * 65536

func (h *SiteHandler) storageRuntimeDir(c storageCall) string {
	return filepath.Join(h.disk.SiteDir(c.ownerID, c.siteName), "runtime")
}
func (h *SiteHandler) storageSQLPath(c storageCall) string {
	return filepath.Join(h.storageRuntimeDir(c), "sqlite", c.resourceName+".sqlite")
}

// stageStorageRuntime moves bytes out of the live resource name before the
// PostgreSQL declaration is deleted. The caller restores on DB failure and
// removes the staging directory only after commit.
func (h *SiteHandler) stageStorageRuntime(c storageCall) (staged string, restore func(), err error) {
	if c.resource.Kind == "kv" {
		return "", func() {}, nil
	}
	root := h.storageRuntimeDir(c)
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", nil, err
	}
	staged, err = os.MkdirTemp(root, ".deleting-")
	if err != nil {
		return "", nil, err
	}
	type move struct{ from, to string }
	moved := []move{}
	restore = func() {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.Rename(moved[i].to, moved[i].from)
		}
		_ = os.RemoveAll(staged)
	}
	var paths []string
	if c.resource.Kind == "sqlite" {
		p := h.storageSQLPath(c)
		paths = []string{p, p + "-wal", p + "-shm"}
	} else {
		paths = []string{h.storageFilesDir(c)}
	}
	for i, p := range paths {
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			continue
		} else if e != nil {
			restore()
			return "", nil, e
		}
		dst := filepath.Join(staged, fmt.Sprintf("%d", i))
		if e := os.Rename(p, dst); e != nil {
			restore()
			return "", nil, e
		}
		moved = append(moved, move{p, dst})
	}
	return staged, restore, nil
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
	if dec.Decode(&req) != nil || strings.TrimSpace(req.SQL) == "" || len(req.SQL) > 32768 || len(req.Params) > 100 || dec.Decode(new(any)) != io.EOF {
		storageError(w, 400, "invalid_sql", "invalid SQL request")
		return
	}
	for _, raw := range req.Params {
		if !storageScalar(raw) {
			storageError(w, 400, "invalid_params", "parameters must be JSON scalars")
			return
		}
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
		if fi, e := os.Stat(dbPath); e == nil {
			currentDBBytes = fi.Size()
		} else if !os.IsNotExist(e) {
			storageError(w, 500, "internal_error", "internal server error")
			return
		} else {
			newDB = true
		}
		if currentDBBytes == 0 && h.storageBudgetUsed(usage)+8192 > storageSiteLimitBytes() {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return
		}
		defer func() {
			if newDB && !successful {
				for _, s := range []string{"", "-wal", "-shm"} {
					_ = os.Remove(dbPath + s)
				}
			}
		}()
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
	}
	uri := dbPath
	if mode == "query" {
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			uri = ":memory:"
		} else if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		} else {
			uri = (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}).String()
		}
	}
	release, err := acquireStorageSQLite(r.Context())
	if err != nil {
		storageBusy(w)
		return
	}
	defer release()
	timeout := config.Active().StorageOwnerTimeout
	if !c.owner {
		timeout = config.Active().StorageVisitorQueryTimeout
		if mode != "query" {
			timeout = config.Active().StorageVisitorWriteTimeout
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), uri)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
	if err = conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if _, err = conn.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	_ = conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
	_ = conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	if mode != "query" {
		if err = conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
	}
	if mode != "query" {
		pageSizeStmt, _, e := conn.Prepare("PRAGMA page_size")
		if e != nil || !pageSizeStmt.Step() {
			if pageSizeStmt != nil {
				pageSizeStmt.Close()
			}
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		pageSize := pageSizeStmt.ColumnInt64(0)
		pageSizeStmt.Close()
		if pageSize <= 0 {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		maxPages := (storageSiteLimitBytes() - (h.storageBudgetUsed(usage) - currentDBBytes)) / pageSize
		if maxPages < 1 {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return
		}
		if err = conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		capStmt, _, e := conn.Prepare("PRAGMA max_page_count")
		if e != nil || !capStmt.Step() {
			if capStmt != nil {
				capStmt.Close()
			}
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		actualPages := capStmt.ColumnInt64(0)
		capStmt.Close()
		if actualPages > maxPages {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
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
			storageError(w, 400, "invalid_schema", "cannot inspect schema")
			return
		}
		defer func() { _ = conn.SetAuthorizer(nil); _ = conn.Exec("ROLLBACK") }()
	}
	var didRead, didWrite, didSchema bool
	err = conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if schema != "" && schema != "main" {
			return sqlite3.AUTH_DENY
		}
		switch a {
		case sqlite3.AUTH_ATTACH, sqlite3.AUTH_DETACH, sqlite3.AUTH_PRAGMA, sqlite3.AUTH_CREATE_VTABLE, sqlite3.AUTH_DROP_VTABLE, sqlite3.AUTH_CREATE_TEMP_TABLE, sqlite3.AUTH_CREATE_TEMP_INDEX, sqlite3.AUTH_CREATE_TEMP_TRIGGER, sqlite3.AUTH_CREATE_TEMP_VIEW, sqlite3.AUTH_DROP_TEMP_TABLE, sqlite3.AUTH_DROP_TEMP_INDEX, sqlite3.AUTH_DROP_TEMP_TRIGGER, sqlite3.AUTH_DROP_TEMP_VIEW, sqlite3.AUTH_TRANSACTION, sqlite3.AUTH_SAVEPOINT:
			return sqlite3.AUTH_DENY
		case sqlite3.AUTH_READ:
			didRead = true
			if mode == "execute" && c.resource.Read != "anyone" && !c.owner {
				if c.resource.Read == "owner" {
					return sqlite3.AUTH_DENY
				}
				if c.visitorID == "" {
					return sqlite3.AUTH_DENY
				}
			}
		case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
			didWrite = true
			if mode != "execute" && mode != "schema" {
				return sqlite3.AUTH_DENY
			}
		case sqlite3.AUTH_CREATE_INDEX, sqlite3.AUTH_CREATE_TABLE, sqlite3.AUTH_CREATE_TRIGGER, sqlite3.AUTH_CREATE_VIEW, sqlite3.AUTH_DROP_INDEX, sqlite3.AUTH_DROP_TABLE, sqlite3.AUTH_DROP_TRIGGER, sqlite3.AUTH_DROP_VIEW, sqlite3.AUTH_ALTER_TABLE, sqlite3.AUTH_REINDEX, sqlite3.AUTH_ANALYZE:
			didSchema = true
			if mode != "schema" {
				return sqlite3.AUTH_DENY
			}
		case sqlite3.AUTH_FUNCTION:
			fn := strings.ToLower(n4)
			if fn == "load_extension" || fn == "readfile" || fn == "writefile" {
				return sqlite3.AUTH_DENY
			}
		}
		return sqlite3.AUTH_OK
	})
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	stmt, tail, err := conn.Prepare(req.SQL)
	if err != nil || stmt == nil {
		storageError(w, 400, "invalid_sql", "SQL denied or invalid")
		return
	}
	defer stmt.Close()
	if strings.TrimSpace(tail) != "" || len(req.Params) != stmt.BindCount() {
		storageError(w, 400, "invalid_sql", "one statement and matching parameters required")
		return
	}
	// CREATE TRIGGER IF NOT EXISTS on a trigger that already exists compiles to
	// a read-only no-op, so the authorizer never sees a schema change (tables
	// and views still report theirs; an existing index compiles as a write and
	// stays refused); the owner's setup is meant to be rerunnable, so that
	// no-op passes the schema route.
	noopCreate := mode == "schema" && !didSchema && !didWrite && stmt.ReadOnly() && createIfNotExists(req.SQL)
	if mode == "query" && (!stmt.ReadOnly() || didWrite || didSchema) || mode == "execute" && (!didWrite || didSchema) || mode == "schema" && !didSchema && !noopCreate {
		storageError(w, 403, "forbidden", "SQL operation is not allowed on this route")
		return
	}
	for i, raw := range req.Params {
		if err = bindStorageParam(stmt, i+1, raw); err != nil {
			storageError(w, 400, "invalid_params", "parameters must be JSON scalars")
			return
		}
	}
	if mode == "query" {
		cols := make([]string, stmt.ColumnCount())
		for i := range cols {
			cols[i] = stmt.ColumnName(i)
		}
		rows := make([][]any, 0)
		encodedCols, _ := json.Marshal(cols)
		resultBytes := len(encodedCols) + 64
		for stmt.Step() {
			if len(rows) >= 500 {
				storageError(w, 400, "result_too_large", "query returned more than 500 rows")
				return
			}
			row := make([]any, len(cols))
			for i := range cols {
				row[i] = storageColumn(stmt, i)
			}
			encoded, e := json.Marshal(row)
			if e != nil || resultBytes+len(encoded) > storageResultLimitBytes() {
				storageError(w, 400, "result_too_large", "query result is too large")
				return
			}
			resultBytes += len(encoded)
			rows = append(rows, row)
		}
		if stmt.Err() != nil {
			storageError(w, 400, "invalid_sql", "query failed")
			return
		}
		result := map[string]any{"columns": cols, "rows": rows}
		encoded, e := json.Marshal(result)
		if e != nil || len(encoded) > storageResultLimitBytes() {
			storageError(w, 400, "result_too_large", "query result is too large")
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
		storageError(w, 400, "invalid_sql", "SQL execution failed")
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
				renamed, added := false, []string{}
				for t := range before {
					if _, ruled := c.resource.Tables[strings.ToLower(t)]; ruled && !after[t] {
						renamed = true
					}
				}
				for t := range after {
					if !before[t] {
						added = append(added, t)
					}
				}
				if renamed && len(added) > 0 {
					storageError(w, 409, "table_preset_rename", "this table has its own preset; give the new name its preset first (storage_set_resource tables), then rename")
					return
				}
				sort.Strings(added)
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
			err = storageEnsureOwnTables(conn, before, c.resource.ownTables())
		}
		if err == nil {
			err = conn.Exec("COMMIT")
		}
		if err != nil {
			storageError(w, 400, "visitor_id_required", "every table must retain visitor_id TEXT; only new tables receive it automatically")
			return
		}
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
	_ = didRead
}

func bindStorageParam(stmt *sqlite3.Stmt, i int, raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	switch x := v.(type) {
	case nil:
		return stmt.BindNull(i)
	case string:
		return stmt.BindText(i, x)
	case bool:
		return stmt.BindBool(i, x)
	case json.Number:
		if n, e := x.Int64(); e == nil {
			return stmt.BindInt64(i, n)
		}
		if f, e := x.Float64(); e == nil {
			return stmt.BindFloat(i, f)
		}
		return fmt.Errorf("invalid number")
	default:
		return fmt.Errorf("invalid type")
	}
}
func storageColumn(stmt *sqlite3.Stmt, i int) any {
	switch stmt.ColumnType(i) {
	case sqlite3.NULL:
		return nil
	case sqlite3.INTEGER:
		return stmt.ColumnInt64(i)
	case sqlite3.FLOAT:
		return stmt.ColumnFloat(i)
	case sqlite3.BLOB:
		return map[string]string{"base64": base64.StdEncoding.EncodeToString(stmt.ColumnBlob(i, nil))}
	default:
		return stmt.ColumnText(i)
	}
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

var errStorageBusy = errors.New("storage is busy")

// Shared by every SiteHandler and by owner policy validation. Writers acquire
// the site lock first, then a SQLite slot; readers never acquire the site lock.
var storageSQLiteGate struct {
	once  sync.Once
	slots chan struct{}
}

func acquireStorageSQLite(ctx context.Context) (func(), error) {
	storageSQLiteGate.once.Do(func() {
		n := config.Active().StorageSQLConcurrency
		if n == 0 {
			n = runtime.NumCPU()
		}
		storageSQLiteGate.slots = make(chan struct{}, n)
	})
	wait, cancel := context.WithTimeout(ctx, config.Active().StorageAcquireWait)
	defer cancel()
	select {
	case storageSQLiteGate.slots <- struct{}{}:
		return func() { <-storageSQLiteGate.slots }, nil
	case <-wait.Done():
		return nil, errStorageBusy
	}
}

// createIfNotExists reports whether sql is a CREATE ... IF NOT EXISTS statement.
func createIfNotExists(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	return strings.HasPrefix(upper, "CREATE") && strings.Contains(upper, "IF NOT EXISTS")
}
