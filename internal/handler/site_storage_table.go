package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/storage"
)

// The SQLite table routes. Visitors send an action and values, never SQL;
// the server builds every statement from the table's matrix
// (site_storage_access.go):
//
//	GET    .../tables/{t}/rows            read (list): equality filters where.{col}=, order, desc, limit, after
//	GET    .../tables/{t}/rows/{id}       read (one row)
//	POST   .../tables/{t}/rows            add
//	PATCH  .../tables/{t}/rows/{id}       edit
//	DELETE .../tables/{t}/rows/{id}       delete
//
// When an action's value is own, the statement itself carries
// visitor_id = caller, so another person's row answers 404 like a missing
// one. id, visitor_id, created_at and updated_at belong to the server.

const storageWhereMax = 3

// storageServerColumn: a column only the server writes.
func storageServerColumn(name string) bool {
	switch strings.ToLower(name) {
	case "visitor_id", "created_at", "updated_at", "rowid", "_rowid_", "oid":
		return true
	}
	return false
}

func (h *SiteHandler) storageRows(w http.ResponseWriter, r *http.Request) {
	var after func()
	defer func() {
		if after != nil {
			after()
		}
	}()
	c, ok := h.storageResourceFor(w, r, "sqlite")
	if !ok {
		return
	}
	table := r.PathValue("table")
	idRaw := r.PathValue("id")
	action := map[string]string{http.MethodGet: "read", http.MethodHead: "read", http.MethodPost: "add", http.MethodPatch: "edit", http.MethodDelete: "delete"}[r.Method]
	if action == "" || (action == "edit" || action == "delete") && idRaw == "" {
		storageError(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var rowID int64
	if idRaw != "" {
		n, err := strconv.ParseInt(idRaw, 10, 64)
		if err != nil || n < 1 || action == "add" {
			storageError(w, 400, "invalid_rows", "row id is a positive whole number")
			return
		}
		rowID = n
	}
	if !storageTableNameRE.MatchString(table) {
		storageError(w, 400, "invalid_rows", "invalid row request")
		return
	}
	if action == "read" {
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		var result appstorage.RowResult
		var err error
		if rowID != 0 {
			result, err = h.storageRequestSvc(r, c).GetRow(r.Context(), appstorage.RowInput{Call: call, Table: table, ID: rowID})
		} else {
			result, err = h.storageRequestSvc(r, c).ListRows(r.Context(), appstorage.ListRowsInput{Call: call, Table: table, Query: r.URL.Query()})
		}
		if auth := result.Authorization; auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, 403, auth.Code, auth.Message)
				return
			}
			writeStorageRefusals(w, auth.Refusals)
			if !auth.Allowed {
				return
			}
		}
		if err != nil {
			e := err.(*appstorage.Error)
			if e.Code == "storage_busy" {
				storageBusy(w)
				return
			}
			status := 400
			if e.Code == "row_not_found" {
				status = 404
			}
			if e.Code == "internal_error" {
				status = 500
			}
			storageError(w, status, e.Code, e.Message)
			return
		}
		writeJSON(w, 200, result.Body)
		return
	}
	if !h.storageGate(w, r, c, action != "read") {
		return
	}
	c.currentMatrix = c.resource.tableMatrix(table)
	filter, ok := h.storageAllow(w, r, &c, action, c.currentMatrix.value(action))
	if !ok {
		return
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var values map[string]json.RawMessage
		if action == "add" || action == "edit" {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
			if err != nil {
				storageError(w, 400, "invalid_rows", "send a JSON object of column values")
				return
			}
			if c.checkedPost && action == "add" && !storagePostLimits(w, body, h.limits) {
				return
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			if dec.Decode(&values) != nil || values == nil || len(values) > 100 || dec.Decode(new(any)) != io.EOF || action == "edit" && len(values) == 0 {
				storageError(w, 400, "invalid_rows", "send a JSON object of column values")
				return
			}
			for _, raw := range values {
				if !storageScalar(raw) {
					storageError(w, 400, "invalid_rows", "values are strings, numbers, true, false, or null")
					return
				}
			}
		}
		write := action != "read"
		if write {
			unlock, ok := h.storageWriteLock(w, r, c, true)
			if !ok {
				return
			}
			defer unlock()
		}
		conn, done, ok := h.openStorageTableConn(w, r, c, write)
		if !ok {
			return
		}
		defer done()
		cols, order, err := storageTableInfo(conn, table)
		if err != nil {
			storageError(w, 400, "invalid_rows", "invalid row request")
			return
		}
		if !c.owner && len(cols) > 100 {
			storageError(w, 400, "invalid_rows", "too many columns")
			return
		}
		if filter != "" && cols["visitor_id"] != "TEXT" {
			storageError(w, 400, "visitor_id_required", "own needs the visitor_id TEXT column; set the policy again so the server adds it")
			return
		}
		rowid, err := storageRowID(conn, table, cols)
		if err != nil {
			storageError(w, 400, "invalid_rows", "invalid row request")
			return
		}
		t := storageTableCall{h: h, w: w, r: r, c: c, conn: conn, table: table, cols: cols, order: order, rowid: rowid, filter: filter, hold: c.resource.holds(table), after: &after}
		if t.hold && cols["approved_at"] != "TEXT" {
			storageError(w, 400, "approved_at_required", "approve_first needs the approved_at TEXT column; set the policy again so the server adds it")
			return
		}
		switch action {
		case "add":
			t.add(values)
		case "edit":
			t.edit(rowID, values)
		case "delete":
			t.remove(rowID)
		}
	})
	account := c.accountID
	if !c.owner {
		account = c.visitorID
	}
	h.createIdempotency.wrapAccount(account, c.siteID, next).ServeHTTP(w, r)
}

type storageTableCall struct {
	after  *func()
	h      *SiteHandler
	w      http.ResponseWriter
	r      *http.Request
	c      storageCall
	conn   *sqlite3.Conn
	table  string
	cols   map[string]string
	order  []string
	rowid  string
	filter string
	// hold: posts wait for the owner's approval here (approve_first);
	// visitors see and reach approved rows only.
	hold bool
}

// hidesPending: this caller sees approved rows only.
func (t *storageTableCall) hidesPending() bool { return t.hold && !t.seesAuthors() }

// storageTableInfo is a table's columns (name to declared type) and their
// order.
func storageTableInfo(conn *sqlite3.Conn, table string) (map[string]string, []string, error) {
	return storage.TableInfo(conn, table)
}

// seesAuthors: the caller sees every row's visitor_id (the owner's tools,
// and the owner signed in on the site). Visitors see only their own.
func (t *storageTableCall) seesAuthors() bool { return t.c.owner || t.c.ownerOnSite }

// visitorAuthorizer confines a visitor's statement to the target table:
// one top-level write of the expected kind, no schema change, no trigger
// writing elsewhere.
func (t *storageTableCall) visitorAuthorizer(write sqlite3.AuthorizerActionCode) error {
	return appstorage.SetRowAuthorizer(t.conn, t.table, t.c.owner, write)
}

func (t *storageTableCall) bindAll(stmt *sqlite3.Stmt, params []json.RawMessage) error {
	if len(params) != stmt.BindCount() {
		return fmt.Errorf("parameter count")
	}
	for i, raw := range params {
		if err := bindStorageParam(stmt, i+1, raw); err != nil {
			return err
		}
	}
	return nil
}

func storageJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// resultRow reads one row of SELECT *, rowid and hides other people's
// visitor_id from visitors. mine: the caller added it.
func (t *storageTableCall) resultRow(stmt *sqlite3.Stmt) ([]any, int64, bool) {
	n := len(t.order)
	row := make([]any, n)
	mine := false
	for i := 0; i < n; i++ {
		row[i] = storageColumn(stmt, i)
		if t.order[i] == "visitor_id" {
			id, _ := row[i].(string)
			mine = id != "" && t.c.visitorID != "" && id == t.c.visitorID
			if !t.seesAuthors() && !mine {
				row[i] = nil
			}
		}
	}
	return row, stmt.ColumnInt64(n), mine
}

// begin starts the write transaction the statement and its reference check
// share; rollback runs unless commit succeeded.
func (t *storageTableCall) begin() (func() error, func(), bool) {
	commit, rollback, err := storage.BeginRowWrite(t.conn)
	if err != nil {
		storageError(t.w, 503, "storage_busy", "storage is busy; retry shortly")
		return nil, nil, false
	}
	return commit, rollback, true
}

// run executes one write statement under the visitor authorizer. It
// answers the error itself.
func (t *storageTableCall) run(kind sqlite3.AuthorizerActionCode, query string, params []json.RawMessage, returning bool) (int64, bool) {
	if err := t.visitorAuthorizer(kind); err != nil {
		storageError(t.w, 500, "internal_error", "internal server error")
		return 0, false
	}
	id, stage, err := storage.RunRowStatement(t.conn, query, params, returning)
	if stage == storage.RowStatementPrepare {
		// A trigger or foreign-key action that writes elsewhere is refused
		// while the statement is prepared.
		storageError(t.w, 403, "forbidden", "this change would touch other tables; pages cannot make it, only the owner's tools")
		return 0, false
	}
	if stage == storage.RowStatementBind {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return 0, false
	}
	if err != nil {
		switch {
		case errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY):
			storageError(t.w, 404, "invalid_reference", "referenced row not found")
		case errors.Is(err, sqlite3.AUTH):
			storageError(t.w, 403, "forbidden", "this change would touch other tables; pages cannot make it, only the owner's tools")
		case errors.Is(err, sqlite3.CONSTRAINT):
			storageError(t.w, 409, "row_conflict", "row cannot be saved")
		case errors.Is(err, sqlite3.FULL):
			storageError(t.w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
		default:
			storageError(t.w, 400, "invalid_rows", "the change failed")
		}
		return 0, false
	}
	if returning && id == 0 {
		storageError(t.w, 409, "row_conflict", "row cannot be saved")
		return 0, false
	}
	return id, true
}

func (t *storageTableCall) finish(commit func() error, body map[string]any, notify func()) {
	// COMMIT is not a write to the table, so the visitor authorizer would
	// refuse it.
	if err := t.conn.SetAuthorizer(nil); err != nil {
		storageError(t.w, 500, "internal_error", "internal server error")
		return
	}
	if err := commit(); err != nil {
		if errors.Is(err, sqlite3.CONSTRAINT_FOREIGNKEY) {
			storageError(t.w, 404, "invalid_reference", "referenced row not found")
		} else {
			storageError(t.w, 400, "invalid_rows", "cannot commit the change")
		}
		return
	}
	// The change is committed; a failed checkpoint must not read as a
	// failed save (a retried add would be a duplicate).
	if _, _, err := t.conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		log.Printf("storage: site %s %s: checkpoint after commit: %v", t.c.siteID, t.c.resourceName, err)
	}
	paperHackWrite(t.r)
	t.h.disk.MarkChanged()
	*t.after = notify
	writeJSON(t.w, 200, body)
}

func (t *storageTableCall) add(values map[string]json.RawMessage) {
	query, params, err := appstorage.PrepareInsert(t.conn, t.table, t.rowid, t.cols, values, appstorage.InsertActor{Owner: t.c.owner, OwnerOnSite: t.c.ownerOnSite, Hold: t.hold, StampID: t.c.stampID()})
	if err != nil {
		if errors.Is(err, appstorage.ErrInsertSuppliesID) {
			storageError(t.w, 400, "invalid_rows", "the server assigns ids")
			return
		}
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	commit, rollback, ok := t.begin()
	if !ok {
		return
	}
	defer rollback()
	id, ok := t.run(sqlite3.AUTH_INSERT, query, params, true)
	if !ok || !t.checkReferences(id) {
		return
	}
	t.finish(commit, map[string]any{"changes": 1, "last_insert_id": id}, func() { t.notifyRow("record.added", "", id, values) })
}

func (t *storageTableCall) edit(id int64, values map[string]json.RawMessage) {
	pk := map[string]bool{}
	info, _, err := t.conn.Prepare("PRAGMA table_info(" + storageQuote(t.table) + ")")
	if err != nil {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	for info.Step() {
		if info.ColumnInt(5) > 0 {
			pk[info.ColumnText(1)] = true
		}
	}
	info.Close()
	names := make([]string, 0, len(values))
	for name := range values {
		if storageServerColumn(name) || pk[name] || t.hold && !t.seesAuthors() && strings.EqualFold(name, "approved_at") {
			storageError(t.w, 400, "server_column", "id, visitor_id, created_at, and updated_at are set by the server")
			return
		}
		if _, ok := t.cols[name]; !ok {
			storageError(t.w, 400, "invalid_rows", "invalid row request")
			return
		}
		names = append(names, name)
	}
	sort.Strings(names)
	sets := []string{}
	params := []json.RawMessage{}
	for _, name := range names {
		sets = append(sets, storageQuote(name)+" = ?")
		params = append(params, values[name])
	}
	if _, ok := t.cols["updated_at"]; ok {
		sets = append(sets, `"updated_at" = ?`)
		values["updated_at"] = storageJSON(time.Now().UTC().Format(time.RFC3339Nano))
		params = append(params, values["updated_at"])
	}
	query := "UPDATE OR ABORT " + storageQuote(t.table) + " SET " + strings.Join(sets, ", ") + " WHERE " + t.rowid + " = ?"
	if t.c.owner {
		query = "UPDATE " + storageQuote(t.table) + " SET " + strings.Join(sets, ", ") + " WHERE " + t.rowid + " = ?"
	}
	params = append(params, storageJSON(id))
	if t.filter != "" {
		query += " AND visitor_id = ?"
		params = append(params, storageJSON(t.filter))
	}
	if t.hidesPending() {
		query += " AND approved_at IS NOT NULL"
	}
	commit, rollback, ok := t.begin()
	if !ok {
		return
	}
	defer rollback()
	before := t.conn.TotalChanges()
	if _, ok := t.run(sqlite3.AUTH_UPDATE, query, params, false); !ok {
		return
	}
	if t.conn.Changes() == 0 {
		storageError(t.w, 404, "row_not_found", "row not found")
		return
	}
	if !t.oneRow(before) {
		return
	}
	if !t.checkReferences(id) {
		return
	}
	t.finish(commit, map[string]any{"changes": 1}, func() { t.notifyRow("record.changed", "edited", id, values) })
}

func (t *storageTableCall) remove(id int64) {
	query := "DELETE FROM " + storageQuote(t.table) + " WHERE " + t.rowid + " = ?"
	params := []json.RawMessage{storageJSON(id)}
	if t.filter != "" {
		query += " AND visitor_id = ?"
		params = append(params, storageJSON(t.filter))
	}
	if t.hidesPending() {
		query += " AND approved_at IS NOT NULL"
	}
	commit, rollback, ok := t.begin()
	if !ok {
		return
	}
	defer rollback()
	before := t.conn.TotalChanges()
	if _, ok := t.run(sqlite3.AUTH_DELETE, query, params, false); !ok {
		return
	}
	if t.conn.Changes() == 0 {
		storageError(t.w, 404, "row_not_found", "row not found")
		return
	}
	if !t.oneRow(before) {
		return
	}
	t.finish(commit, map[string]any{"deleted": true}, func() { t.notifyRow("record.changed", "deleted", id, nil) })
}

// oneRow: a visitor's edit or delete changed exactly its one row. A
// foreign-key action within the same table (a reply that cascades with its
// parent) would otherwise reach rows the caller may not change; the
// transaction is rolled back.
func (t *storageTableCall) oneRow(before int64) bool {
	if t.c.owner || t.conn.TotalChanges()-before == 1 {
		return true
	}
	storageError(t.w, 403, "forbidden", "this change would touch other rows; pages cannot make it, only the owner's tools")
	return false
}

// checkReferences: every row a visitor adds or edits may reference only
// parent rows that visitor may read under the parent table's read value.
// Otherwise 404 invalid_reference, the same as a missing parent, so a
// reference cannot test whether a private row exists.
func (t *storageTableCall) checkReferences(id int64) bool {
	if t.c.owner {
		return true
	}
	_ = t.conn.SetAuthorizer(nil)
	if status, err := storageCheckReferences(t.conn, t.c, t.table, t.rowid, id); err != nil {
		if status == 404 {
			storageError(t.w, 404, "invalid_reference", "referenced row not found")
		} else {
			storageError(t.w, 400, "invalid_rows", "cannot check references")
		}
		return false
	}
	return true
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
