package handler

import (
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
	"sort"
	"strconv"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/internal/config"
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
	if !h.storageGate(w, r, c, action != "read") {
		return
	}
	c.currentMatrix = c.resource.tableMatrix(table)
	filter, ok := h.storageAllow(w, r, &c, action, c.currentMatrix.value(action))
	if !ok {
		return
	}
	var values map[string]json.RawMessage
	if action == "add" || action == "edit" {
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
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
	t := storageTableCall{h: h, w: w, r: r, c: c, conn: conn, table: table, cols: cols, order: order, rowid: rowid, filter: filter}
	switch action {
	case "read":
		if rowID != 0 {
			t.getOne(rowID)
		} else {
			t.list()
		}
	case "add":
		t.add(values)
	case "edit":
		t.edit(rowID, values)
	case "delete":
		t.remove(rowID)
	}
}

type storageTableCall struct {
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
}

// storageTableInfo is a table's columns (name to declared type) and their
// order.
func storageTableInfo(conn *sqlite3.Conn, table string) (map[string]string, []string, error) {
	cols, err := storageTableColumns(conn, table)
	if err != nil {
		return nil, nil, err
	}
	stmt, _, err := conn.Prepare("SELECT * FROM " + storageQuote(table) + " LIMIT 0")
	if err != nil {
		return nil, nil, err
	}
	defer stmt.Close()
	order := make([]string, stmt.ColumnCount())
	for i := range order {
		order[i] = stmt.ColumnName(i)
	}
	return cols, order, nil
}

// seesAuthors: the caller sees every row's visitor_id (the owner's tools,
// and the owner signed in on the site). Visitors see only their own.
func (t *storageTableCall) seesAuthors() bool { return t.c.owner || t.c.ownerOnSite }

// visitorAuthorizer confines a visitor's statement to the target table:
// one top-level write of the expected kind, no schema change, no trigger
// writing elsewhere.
func (t *storageTableCall) visitorAuthorizer(write sqlite3.AuthorizerActionCode) error {
	if t.c.owner {
		return t.conn.SetAuthorizer(nil)
	}
	return t.conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if schema != "" && schema != "main" {
			return sqlite3.AUTH_DENY
		}
		switch a {
		case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_RECURSIVE:
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
			if a != write || n3 != t.table || inner != "" {
				return sqlite3.AUTH_DENY
			}
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_FUNCTION:
			fn := strings.ToLower(n4)
			if fn == "load_extension" || fn == "readfile" || fn == "writefile" {
				return sqlite3.AUTH_DENY
			}
			return sqlite3.AUTH_OK
		}
		return sqlite3.AUTH_DENY
	})
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

func (t *storageTableCall) list() {
	q := t.r.URL.Query()
	_, after, limit, ok := storagePage(t.r)
	if !ok {
		storageError(t.w, 400, "invalid_rows", "invalid limit")
		return
	}
	order := q.Get("order")
	if order == "" {
		order = t.rowid
	}
	if _, ok := t.cols[order]; !ok && order != t.rowid || order == "visitor_id" && !t.seesAuthors() {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	desc := q.Get("desc")
	if desc != "" && desc != "0" && desc != "1" {
		storageError(t.w, 400, "invalid_rows", "desc is 0 or 1")
		return
	}
	page := &storageRowPage{limit: limit, order: order, desc: desc == "1", orderIndex: -1}
	for i, name := range t.order {
		if name == order {
			page.orderIndex = i
		}
	}
	where := []string{}
	params := []json.RawMessage{}
	bind := func(v any) { params = append(params, storageJSON(v)) }
	if t.filter != "" {
		where = append(where, "visitor_id = ?")
		bind(t.filter)
	}
	// Equality filters on real columns only, bound as values.
	keys := []string{}
	for k := range q {
		if strings.HasPrefix(k, "where.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > storageWhereMax {
		storageError(t.w, 400, "invalid_rows", "at most 3 where filters")
		return
	}
	for _, k := range keys {
		col := strings.TrimPrefix(k, "where.")
		if _, ok := t.cols[col]; !ok || col == "visitor_id" && !t.seesAuthors() {
			storageError(t.w, 400, "invalid_rows", "invalid row request")
			return
		}
		where = append(where, storageQuote(col)+" = ?")
		bind(q.Get(k))
	}
	direction, op := " ASC", ">"
	if page.desc {
		direction, op = " DESC", "<"
	}
	rowid := t.rowid
	if after != "" {
		b, e := base64.RawURLEncoding.DecodeString(after)
		var cur storageRowCursor
		if e != nil || json.Unmarshal(b, &cur) != nil || cur.Order != order || cur.Desc != page.desc || cur.Value == nil || !storageScalar(cur.Value) {
			storageError(t.w, 400, "invalid_rows", "invalid after")
			return
		}
		// SQLite sorts NULL first in ascending order; include them when descending.
		qo := storageQuote(order)
		if order == t.rowid {
			qo = rowid
		}
		if string(cur.Value) == "null" {
			if page.desc {
				where = append(where, "("+qo+" IS NULL AND "+rowid+op+"?)")
			} else {
				where = append(where, "("+qo+" IS NOT NULL OR ("+qo+" IS NULL AND "+rowid+op+"?))")
			}
			bind(cur.ID)
		} else {
			nulls := ""
			if page.desc {
				nulls = qo + " IS NULL OR "
			}
			where = append(where, "("+nulls+qo+op+"? OR ("+qo+" = ? AND "+rowid+op+"?))")
			params = append(params, cur.Value, cur.Value)
			bind(cur.ID)
		}
	}
	query := "SELECT *, " + rowid + " FROM " + storageQuote(t.table)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	orderSQL := storageQuote(order)
	if order == t.rowid {
		orderSQL = rowid
	}
	query += " ORDER BY " + orderSQL + direction + ", " + rowid + direction + " LIMIT ?"
	bind(limit + 1)
	if err := t.visitorAuthorizer(sqlite3.AUTH_SELECT); err != nil {
		storageError(t.w, 500, "internal_error", "internal server error")
		return
	}
	stmt, _, err := t.conn.Prepare(query)
	if err != nil || stmt == nil {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	defer stmt.Close()
	if err = t.bindAll(stmt, params); err != nil {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	rows := make([][]any, 0)
	mine := make([]bool, 0)
	next := ""
	hasMore := false
	cols := t.order
	encodedCols, _ := json.Marshal(cols)
	resultBytes := len(encodedCols) + 64
	for stmt.Step() {
		if len(rows) >= page.limit {
			hasMore = true
			break
		}
		raw := make([]any, len(cols))
		for i := range cols {
			raw[i] = storageColumn(stmt, i)
		}
		cursor := page.cursor(raw, stmt.ColumnInt64(len(cols)))
		row, _, m := t.resultRow(stmt)
		encoded, e := json.Marshal(row)
		if e != nil || resultBytes+len(encoded) > storageResultLimitBytes() {
			storageError(t.w, 400, "result_too_large", "query result is too large")
			return
		}
		resultBytes += len(encoded)
		rows = append(rows, row)
		mine = append(mine, m)
		next = cursor
	}
	if stmt.Err() != nil {
		storageError(t.w, 400, "invalid_rows", "query failed")
		return
	}
	if !hasMore {
		next = ""
	}
	result := map[string]any{"columns": cols, "rows": rows, "next_after": next}
	if !t.seesAuthors() {
		result["mine"] = mine
	}
	writeJSON(t.w, 200, result)
}

func (t *storageTableCall) getOne(id int64) {
	query := "SELECT *, " + t.rowid + " FROM " + storageQuote(t.table) + " WHERE " + t.rowid + " = ?"
	params := []json.RawMessage{storageJSON(id)}
	if t.filter != "" {
		query += " AND visitor_id = ?"
		params = append(params, storageJSON(t.filter))
	}
	if err := t.visitorAuthorizer(sqlite3.AUTH_SELECT); err != nil {
		storageError(t.w, 500, "internal_error", "internal server error")
		return
	}
	stmt, _, err := t.conn.Prepare(query)
	if err != nil || stmt == nil {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	defer stmt.Close()
	if err = t.bindAll(stmt, params); err != nil {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return
	}
	if !stmt.Step() {
		if stmt.Err() != nil {
			storageError(t.w, 400, "invalid_rows", "query failed")
			return
		}
		storageError(t.w, 404, "row_not_found", "row not found")
		return
	}
	row, _, mine := t.resultRow(stmt)
	result := map[string]any{"columns": t.order, "row": row}
	if !t.seesAuthors() {
		result["mine"] = mine
	}
	writeJSON(t.w, 200, result)
}

// begin starts the write transaction the statement and its reference check
// share; rollback runs unless commit succeeded.
func (t *storageTableCall) begin() (func() error, func(), bool) {
	if err := t.conn.Exec("BEGIN IMMEDIATE"); err != nil {
		storageError(t.w, 503, "storage_busy", "storage is busy; retry shortly")
		return nil, nil, false
	}
	done := false
	commit := func() error {
		err := t.conn.Exec("COMMIT")
		done = err == nil
		return err
	}
	rollback := func() {
		_ = t.conn.SetAuthorizer(nil)
		if !done {
			_ = t.conn.Exec("ROLLBACK")
		}
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
	stmt, tail, err := t.conn.Prepare(query)
	if err != nil || stmt == nil || strings.TrimSpace(tail) != "" {
		// A trigger or foreign-key action that writes elsewhere is refused
		// while the statement is prepared.
		storageError(t.w, 403, "forbidden", "this change would touch other tables; pages cannot make it, only the owner's tools")
		return 0, false
	}
	defer stmt.Close()
	if err = t.bindAll(stmt, params); err != nil {
		storageError(t.w, 400, "invalid_rows", "invalid row request")
		return 0, false
	}
	var id int64
	if returning {
		if stmt.Step() {
			id = stmt.ColumnInt64(0)
			stmt.Step()
		}
	} else {
		stmt.Step()
	}
	if err = stmt.Err(); err == nil {
		err = stmt.Close()
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

func (t *storageTableCall) finish(commit func() error, body map[string]any) {
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
	t.h.disk.MarkChanged()
	writeJSON(t.w, 200, body)
}

func (t *storageTableCall) add(values map[string]json.RawMessage) {
	if !t.c.owner {
		info, _, err := t.conn.Prepare("PRAGMA table_info(" + storageQuote(t.table) + ")")
		if err != nil {
			storageError(t.w, 400, "invalid_rows", "invalid row request")
			return
		}
		for info.Step() {
			if _, supplied := values[info.ColumnText(1)]; supplied && info.ColumnInt(5) > 0 {
				info.Close()
				storageError(t.w, 400, "invalid_rows", "the server assigns ids")
				return
			}
		}
		info.Close()
		for name := range values {
			if l := strings.ToLower(name); l == "rowid" || l == "_rowid_" || l == "oid" {
				storageError(t.w, 400, "invalid_rows", "the server assigns ids")
				return
			}
		}
		// Server-owned: anything the page sends for these is replaced.
		delete(values, "visitor_id")
		delete(values, "created_at")
		delete(values, "updated_at")
		if _, ok := t.cols["visitor_id"]; ok {
			values["visitor_id"] = storageJSON(t.c.stampID())
			if t.c.stampID() == "" {
				values["visitor_id"] = json.RawMessage("null")
			}
		}
		if _, ok := t.cols["created_at"]; ok {
			values["created_at"] = storageJSON(time.Now().UTC().Format(time.RFC3339Nano))
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		if _, ok := t.cols[name]; !ok {
			storageError(t.w, 400, "invalid_rows", "invalid row request")
			return
		}
		names = append(names, name)
	}
	sort.Strings(names)
	// OR ABORT overrides a table's ON CONFLICT REPLACE, which would let a
	// visitor's insert delete someone else's row.
	query := "INSERT INTO " + storageQuote(t.table)
	if !t.c.owner {
		query = "INSERT OR ABORT INTO " + storageQuote(t.table)
	}
	params := []json.RawMessage{}
	if len(names) == 0 {
		query += " DEFAULT VALUES"
	} else {
		quoted, marks := []string{}, []string{}
		for _, name := range names {
			quoted = append(quoted, storageQuote(name))
			marks = append(marks, "?")
			params = append(params, values[name])
		}
		query += " (" + strings.Join(quoted, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	}
	query += " RETURNING " + t.rowid
	commit, rollback, ok := t.begin()
	if !ok {
		return
	}
	defer rollback()
	id, ok := t.run(sqlite3.AUTH_INSERT, query, params, true)
	if !ok || !t.checkReferences(id) {
		return
	}
	t.finish(commit, map[string]any{"changes": 1, "last_insert_id": id})
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
		if storageServerColumn(name) || pk[name] {
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
		params = append(params, storageJSON(time.Now().UTC().Format(time.RFC3339Nano)))
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
	t.finish(commit, map[string]any{"changes": 1})
}

func (t *storageTableCall) remove(id int64) {
	query := "DELETE FROM " + storageQuote(t.table) + " WHERE " + t.rowid + " = ?"
	params := []json.RawMessage{storageJSON(id)}
	if t.filter != "" {
		query += " AND visitor_id = ?"
		params = append(params, storageJSON(t.filter))
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
	t.finish(commit, map[string]any{"deleted": true})
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

// openStorageTableConn opens the database for one table route: read-only
// for reads, and for writes with the site's allowance as the page cap.
func (h *SiteHandler) openStorageTableConn(w http.ResponseWriter, r *http.Request, c storageCall, write bool) (*sqlite3.Conn, func(), bool) {
	dbPath := h.storageSQLPath(c)
	uri := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}).String()
	var usage siteStorageUsage
	var currentDBBytes int64
	if fi, err := os.Stat(dbPath); os.IsNotExist(err) {
		if write {
			// No tables yet, so no row to add or change.
			storageError(w, 400, "invalid_rows", "unknown table")
			return nil, nil, false
		}
		uri = ":memory:"
	} else if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return nil, nil, false
	} else {
		currentDBBytes = fi.Size()
	}
	if write {
		var err error
		if usage, err = h.measureSiteStorage(r.Context(), c); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return nil, nil, false
		}
		uri = dbPath
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return nil, nil, false
		}
	}
	release, err := acquireStorageSQLite(r.Context())
	if err != nil {
		storageBusy(w)
		return nil, nil, false
	}
	timeout := config.Active().StorageOwnerTimeout
	if !c.owner {
		timeout = config.Active().StorageVisitorQueryTimeout
		if write {
			timeout = config.Active().StorageVisitorWriteTimeout
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), uri)
	if err != nil {
		cancel()
		release()
		storageError(w, 500, "internal_error", "internal server error")
		return nil, nil, false
	}
	done := func() { conn.Close(); cancel(); release() }
	conn.SetInterrupt(ctx)
	fail := func() (*sqlite3.Conn, func(), bool) {
		done()
		storageError(w, 500, "internal_error", "internal server error")
		return nil, nil, false
	}
	if conn.Exec("PRAGMA foreign_keys=ON") != nil {
		return fail()
	}
	if _, err = conn.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		return fail()
	}
	_ = conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
	_ = conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	if write {
		if conn.Exec("PRAGMA journal_mode=WAL") != nil {
			return fail()
		}
		page, _, e := conn.Prepare("PRAGMA page_size")
		if e != nil || !page.Step() {
			if page != nil {
				page.Close()
			}
			return fail()
		}
		pageSize := page.ColumnInt64(0)
		page.Close()
		if pageSize <= 0 {
			return fail()
		}
		maxPages := (storageSiteLimitBytes() - (h.storageBudgetUsed(usage) - currentDBBytes)) / pageSize
		if maxPages < 1 {
			done()
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return nil, nil, false
		}
		if conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)) != nil {
			return fail()
		}
	}
	return conn, done, true
}
