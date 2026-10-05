package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	site, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	unlock := h.lockSite(site.ownerID, site.siteName)
	defer unlock()
	c, ok := h.storageResourceFor(w, r, "sqlite")
	if !ok {
		return
	}
	mode := strings.TrimPrefix(r.URL.Path, "/v1/sites/"+r.PathValue("sitename")+"/storage/sqlite/"+c.resourceName+"/")
	fixed := r.PathValue("table") != ""
	if fixed {
		if r.Method == http.MethodGet {
			mode = "query"
		} else {
			mode = "execute"
		}
	}
	if !fixed && !c.owner && (c.resource.WriteMode == "add" || c.resource.Read == "own") {
		storageError(w, 403, "fixed_routes_required", "use the table rows routes; raw SQL is owner only on add or own databases")
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
	} else if !h.storageAccess(w, r, c, mode == "execute") {
		return
	}
	var req struct {
		SQL    string            `json:"sql"`
		Params []json.RawMessage `json:"params"`
	}
	if !fixed && (json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req) != nil || strings.TrimSpace(req.SQL) == "" || len(req.SQL) > 32768 || len(req.Params) > 100) {
		storageError(w, 400, "invalid_sql", "invalid SQL request")
		return
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
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), uri)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
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
	ownInsert := fixed && mode == "execute" && c.addOnly() && c.resource.Read == "own"
	if ownInsert {
		if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
			storageError(w, 400, "invalid_rows", "cannot start insert transaction")
			return
		}
		defer func() { _ = conn.SetAuthorizer(nil); _ = conn.Exec("ROLLBACK") }()
	}
	var page *storageRowPage
	if fixed {
		var e error
		req.SQL, req.Params, page, e = storageRowsStatement(w, r, c, conn)
		if e != nil {
			storageError(w, 400, "invalid_rows", e.Error())
			return
		}
	}
	var before map[string]bool
	ownSchema := mode == "schema" && c.resource.Read == "own"
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
			if !fixed && mode == "execute" && c.resource.Read != "anyone" && !c.owner {
				if c.resource.Read == "owner" {
					return sqlite3.AUTH_DENY
				}
				if _, ok := h.strictVisitorSession(r, c.siteID); !ok {
					return sqlite3.AUTH_DENY
				}
			}
		case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
			// The fixed INSERT must not turn an owner-defined trigger into
			// visitor permission to update/delete rows or insert elsewhere.
			if fixed && c.addOnly() && (a != sqlite3.AUTH_INSERT || n3 != r.PathValue("table") || inner != "") {
				return sqlite3.AUTH_DENY
			}
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
	if mode == "query" && (!stmt.ReadOnly() || didWrite || didSchema) || mode == "execute" && (!didWrite || didSchema) || mode == "schema" && !didSchema {
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
		next := ""
		hasMore := false
		if page != nil {
			cols = cols[:len(cols)-1]
		}
		resultBytes := 0
		for stmt.Step() {
			if page != nil && len(rows) >= page.limit {
				hasMore = true
				break
			}
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
			if page != nil {
				next = page.cursor(row, stmt.ColumnInt64(len(cols)))
			}
		}
		if stmt.Err() != nil {
			storageError(w, 400, "invalid_sql", "query failed")
			return
		}
		result := map[string]any{"columns": cols, "rows": rows}
		if page != nil {
			if !hasMore {
				next = ""
			}
			result["next_after"] = next
		}
		writeJSON(w, 200, result)
		return
	}
	if err = stmt.Exec(); err != nil {
		if ownInsert && errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY) {
			storageError(w, 404, "invalid_reference", "referenced row not found")
			return
		}
		if errors.Is(err, sqlite3.FULL) {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return
		}
		storageError(w, 400, "invalid_sql", "SQL execution failed")
		return
	}
	if ownInsert {
		if err = stmt.Close(); err == nil {
			err = conn.SetAuthorizer(nil)
		}
		if err != nil {
			storageError(w, 400, "invalid_rows", "cannot check references")
			return
		}
		if status, e := storageCheckOwnReferences(conn, r.PathValue("table"), c.visitorID); e != nil {
			storageError(w, status, "invalid_reference", e.Error())
			return
		}
		if err = conn.Exec("COMMIT"); err != nil {
			storageError(w, 400, "invalid_rows", "cannot commit insert")
			return
		}
	}
	if ownSchema {
		if err = stmt.Close(); err == nil {
			err = conn.SetAuthorizer(nil)
		}
		if err == nil {
			err = storageEnsureOwnTables(conn, before)
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
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		storageError(w, 500, "internal_error", "SQLite checkpoint failed")
		return
	}
	h.disk.MarkChanged()
	if mode == "schema" {
		writeJSON(w, 200, map[string]any{"changes": conn.Changes()})
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
