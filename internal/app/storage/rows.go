package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// RowInput identifies a table row and its caller.
type RowInput struct {
	Call  Call
	Table string
	ID    int64
}

// RowResult contains the row and any ordered authorization responses.
type RowResult struct {
	Body          map[string]any
	Authorization *Error
}

// GetRow authorizes and reads one SQLite table row.
func (s *Service) GetRow(ctx context.Context, in RowInput) (RowResult, error) {
	var out RowResult
	c := in.Call
	c.Context = ctx
	if err := s.Gate(c, false); err != nil {
		out.Authorization = err.(*Error)
		return out, nil
	}
	c.CurrentMatrix = c.Resource.TableMatrix(in.Table)
	filter, err := s.Allow(&c, "read", c.CurrentMatrix.Read)
	if err != nil {
		out.Authorization = err.(*Error)
		if !out.Authorization.Allowed {
			return out, nil
		}
	}
	conn, done, err := runtime.OpenTableConn(ctx, s.deps.SQLitePath, false, s.deps.SQLiteGate, s.deps.SQLiteTimeout, s.deps.SQLiteMaxMemory, nil)
	if err != nil {
		if err == runtime.ErrSQLiteBusy {
			return out, &Error{Code: "storage_busy"}
		}
		return out, &Error{Code: "internal_error", Message: "internal server error"}
	}
	defer done()
	invalid := func() (RowResult, error) { return out, &Error{Code: "invalid_rows", Message: "invalid row request"} }
	cols, err := runtime.TableColumns(conn, in.Table)
	if err != nil {
		return invalid()
	}
	stmt, _, err := conn.Prepare("SELECT * FROM " + runtime.Quote(in.Table) + " LIMIT 0")
	if err != nil {
		return invalid()
	}
	order := make([]string, stmt.ColumnCount())
	for i := range order {
		order[i] = stmt.ColumnName(i)
	}
	stmt.Close()
	if !c.Owner && len(cols) > 100 {
		return out, &Error{Code: "invalid_rows", Message: "too many columns"}
	}
	if filter != "" && cols["visitor_id"] != "TEXT" {
		return out, &Error{Code: "visitor_id_required", Message: "own needs the visitor_id TEXT column; set the policy again so the server adds it"}
	}
	rowid, err := runtime.RowID(conn, in.Table, cols)
	if err != nil {
		return invalid()
	}
	hold := c.Resource.Holds(in.Table)
	if hold && cols["approved_at"] != "TEXT" {
		return out, &Error{Code: "approved_at_required", Message: "approve_first needs the approved_at TEXT column; set the policy again so the server adds it"}
	}
	query := "SELECT *, " + rowid + " FROM " + runtime.Quote(in.Table) + " WHERE " + rowid + " = ?"
	params := []any{in.ID}
	if filter != "" {
		query += " AND visitor_id = ?"
		params = append(params, filter)
	}
	if hold && !c.Owner && !c.OwnerOnSite {
		query += " AND approved_at IS NOT NULL"
	}
	if !c.Owner {
		err = conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
			if schema != "" && schema != "main" {
				return sqlite3.AUTH_DENY
			}
			switch a {
			case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_RECURSIVE:
				return sqlite3.AUTH_OK
			case sqlite3.AUTH_FUNCTION:
				n4 = strings.ToLower(n4)
				if n4 == "load_extension" || n4 == "readfile" || n4 == "writefile" {
					return sqlite3.AUTH_DENY
				}
				return sqlite3.AUTH_OK
			}
			return sqlite3.AUTH_DENY
		})
	} else {
		err = conn.SetAuthorizer(nil)
	}
	if err != nil {
		return out, &Error{Code: "internal_error", Message: "internal server error"}
	}
	stmt, _, err = conn.Prepare(query)
	if err != nil || stmt == nil {
		return invalid()
	}
	defer stmt.Close()
	if len(params) != stmt.BindCount() {
		return invalid()
	}
	for i, v := range params {
		raw, _ := json.Marshal(v)
		if runtime.BindStorageParam(stmt, i+1, raw) != nil {
			return invalid()
		}
	}
	if !stmt.Step() {
		if stmt.Err() != nil {
			return out, &Error{Code: "invalid_rows", Message: "query failed"}
		}
		return out, &Error{Code: "row_not_found", Message: "row not found"}
	}
	row := make([]any, len(order))
	mine := false
	for i, name := range order {
		row[i] = runtime.StorageColumn(stmt, i)
		if name == "visitor_id" {
			id, _ := row[i].(string)
			mine = id != "" && c.VisitorID != "" && id == c.VisitorID
			if !c.Owner && !c.OwnerOnSite && !mine {
				row[i] = nil
			}
		}
	}
	out.Body = map[string]any{"columns": order, "row": row}
	if !c.Owner && !c.OwnerOnSite {
		out.Body["mine"] = mine
	}
	return out, nil
}

// ListRowsInput contains the table and unmodified URL query values.
type ListRowsInput struct {
	Call  Call
	Table string
	Query url.Values
}

// ListRows authorizes and lists SQLite table rows.
func (s *Service) ListRows(ctx context.Context, in ListRowsInput) (RowResult, error) {
	var out RowResult
	c := in.Call
	c.Context = ctx
	if err := s.Gate(c, false); err != nil {
		out.Authorization = err.(*Error)
		return out, nil
	}
	c.CurrentMatrix = c.Resource.TableMatrix(in.Table)
	filter, err := s.Allow(&c, "read", c.CurrentMatrix.Read)
	if err != nil {
		out.Authorization = err.(*Error)
		if !out.Authorization.Allowed {
			return out, nil
		}
	}
	conn, done, err := runtime.OpenTableConn(ctx, s.deps.SQLitePath, false, s.deps.SQLiteGate, s.deps.SQLiteTimeout, s.deps.SQLiteMaxMemory, nil)
	if err != nil {
		if err == runtime.ErrSQLiteBusy {
			return out, &Error{Code: "storage_busy"}
		}
		return out, &Error{Code: "internal_error", Message: "internal server error"}
	}
	defer done()
	invalid := func() (RowResult, error) { return out, &Error{Code: "invalid_rows", Message: "invalid row request"} }
	cols, err := runtime.TableColumns(conn, in.Table)
	if err != nil {
		return invalid()
	}
	stmt, _, err := conn.Prepare("SELECT * FROM " + runtime.Quote(in.Table) + " LIMIT 0")
	if err != nil {
		return invalid()
	}
	orderCols := make([]string, stmt.ColumnCount())
	for i := range orderCols {
		orderCols[i] = stmt.ColumnName(i)
	}
	stmt.Close()
	if !c.Owner && len(cols) > 100 {
		return out, &Error{Code: "invalid_rows", Message: "too many columns"}
	}
	if filter != "" && cols["visitor_id"] != "TEXT" {
		return out, &Error{Code: "visitor_id_required", Message: "own needs the visitor_id TEXT column; set the policy again so the server adds it"}
	}
	rowid, err := runtime.RowID(conn, in.Table, cols)
	if err != nil {
		return invalid()
	}
	hold := c.Resource.Holds(in.Table)
	if hold && cols["approved_at"] != "TEXT" {
		return out, &Error{Code: "approved_at_required", Message: "approve_first needs the approved_at TEXT column; set the policy again so the server adds it"}
	}
	q := in.Query
	after := q.Get("after")
	limit := 100
	ok := true
	if v := q.Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 500 {
			ok = false
		} else {
			limit = n
		}
	}
	if !ok {
		return out, &Error{Code: "invalid_rows", Message: "invalid limit"}
	}
	order := q.Get("order")
	if order == "" {
		order = rowid
	}
	if _, ok := cols[order]; !ok && order != rowid || order == "visitor_id" && !(c.Owner || c.OwnerOnSite) {
		return out, &Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	desc := q.Get("desc")
	if desc != "" && desc != "0" && desc != "1" {
		return out, &Error{Code: "invalid_rows", Message: "desc is 0 or 1"}
	}
	page := &rowPage{limit: limit, order: order, desc: desc == "1", orderIndex: -1}
	for i, name := range orderCols {
		if name == order {
			page.orderIndex = i
		}
	}
	where := []string{}
	params := []json.RawMessage{}
	bind := func(v any) { params = append(params, rowJSON(v)) }
	if filter != "" {
		where = append(where, "visitor_id = ?")
		bind(filter)
	}
	if hold && !c.Owner && !c.OwnerOnSite {
		where = append(where, "approved_at IS NOT NULL")
	}
	// Equality filters on real columns only, bound as values.
	keys := []string{}
	for k := range q {
		if strings.HasPrefix(k, "where.") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) > 3 {
		return out, &Error{Code: "invalid_rows", Message: "at most 3 where filters"}
	}
	for _, k := range keys {
		col := strings.TrimPrefix(k, "where.")
		if _, ok := cols[col]; !ok || col == "visitor_id" && !(c.Owner || c.OwnerOnSite) {
			return out, &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		where = append(where, runtime.Quote(col)+" = ?")
		bind(q.Get(k))
	}
	direction, op := " ASC", ">"
	if page.desc {
		direction, op = " DESC", "<"
	}

	if after != "" {
		b, e := base64.RawURLEncoding.DecodeString(after)
		var cur rowCursor
		if e != nil || json.Unmarshal(b, &cur) != nil || cur.Order != order || cur.Desc != page.desc || cur.Value == nil || !runtime.StorageScalar(cur.Value) {
			return out, &Error{Code: "invalid_rows", Message: "invalid after"}
		}
		// SQLite sorts NULL first in ascending order; include them when descending.
		qo := runtime.Quote(order)
		if order == rowid {
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
	query := "SELECT *, " + rowid + " FROM " + runtime.Quote(in.Table)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	orderSQL := runtime.Quote(order)
	if order == rowid {
		orderSQL = rowid
	}
	query += " ORDER BY " + orderSQL + direction + ", " + rowid + direction + " LIMIT ?"
	bind(limit + 1)
	if err := rowReadAuthorizer(conn, c.Owner); err != nil {
		return out, &Error{Code: "internal_error", Message: "internal server error"}
	}
	stmt, _, err = conn.Prepare(query)
	if err != nil || stmt == nil {
		return out, &Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	defer stmt.Close()
	if err = rowBindAll(stmt, params); err != nil {
		return out, &Error{Code: "invalid_rows", Message: "invalid row request"}
	}
	rows := make([][]any, 0)
	mine := make([]bool, 0)
	next := ""
	hasMore := false
	resultCols := orderCols
	encodedCols, _ := json.Marshal(resultCols)
	resultBytes := len(encodedCols) + 64
	for stmt.Step() {
		if len(rows) >= page.limit {
			hasMore = true
			break
		}
		raw := make([]any, len(resultCols))
		for i := range resultCols {
			raw[i] = runtime.StorageColumn(stmt, i)
		}
		cursor := page.cursor(raw, stmt.ColumnInt64(len(resultCols)))
		row := make([]any, len(orderCols))
		m := false
		for i, name := range orderCols {
			row[i] = runtime.StorageColumn(stmt, i)
			if name == "visitor_id" {
				id, _ := row[i].(string)
				m = id != "" && c.VisitorID != "" && id == c.VisitorID
				if !c.Owner && !c.OwnerOnSite && !m {
					row[i] = nil
				}
			}
		}
		encoded, e := json.Marshal(row)
		if e != nil || resultBytes+len(encoded) > ResultLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES")) {
			return out, &Error{Code: "result_too_large", Message: "query result is too large"}
		}
		resultBytes += len(encoded)
		rows = append(rows, row)
		mine = append(mine, m)
		next = cursor
	}
	if stmt.Err() != nil {
		return out, &Error{Code: "invalid_rows", Message: "query failed"}
	}
	if !hasMore {
		next = ""
	}
	result := map[string]any{"columns": resultCols, "rows": rows, "next_after": next}
	if !(c.Owner || c.OwnerOnSite) {
		result["mine"] = mine
	}
	out.Body = result
	return out, nil
}

type rowPage struct {
	limit      int
	order      string
	desc       bool
	orderIndex int
}
type rowCursor struct {
	Order string          `json:"order"`
	Desc  bool            `json:"desc"`
	Value json.RawMessage `json:"value"`
	ID    int64           `json:"id"`
}

func (p *rowPage) cursor(row []any, id int64) string {
	value := any(id)
	if p.orderIndex >= 0 {
		value = row[p.orderIndex]
	}
	raw, _ := json.Marshal(value)
	b, _ := json.Marshal(rowCursor{p.order, p.desc, raw, id})
	return base64.RawURLEncoding.EncodeToString(b)
}
func rowJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func rowBindAll(stmt *sqlite3.Stmt, params []json.RawMessage) error {
	if len(params) != stmt.BindCount() {
		return fmt.Errorf("parameter count")
	}
	for i, raw := range params {
		if err := runtime.BindStorageParam(stmt, i+1, raw); err != nil {
			return err
		}
	}
	return nil
}
func rowReadAuthorizer(conn *sqlite3.Conn, owner bool) error {
	if owner {
		return conn.SetAuthorizer(nil)
	}
	return conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if schema != "" && schema != "main" {
			return sqlite3.AUTH_DENY
		}
		switch a {
		case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_RECURSIVE:
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_FUNCTION:
			n4 = strings.ToLower(n4)
			if n4 != "load_extension" && n4 != "readfile" && n4 != "writefile" {
				return sqlite3.AUTH_OK
			}
		}
		return sqlite3.AUTH_DENY
	})
}
