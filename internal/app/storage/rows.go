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
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

// TableInput contains the decoded facts for a table operation.
type TableInput struct {
	Call       Call
	Table      string
	ID         int64
	Values     map[string]json.RawMessage
	Query      url.Values
	Authorized TableAuthorization
}

// TableAuthorization carries the decisions made before idempotency replay.
type TableAuthorization struct {
	call          Call
	filter        string
	Authorization *Error
	allowed       bool
	verified      bool
	table, action string
}

// Allowed reports whether the table write was authorized.
func (a TableAuthorization) Allowed() bool { return a.verified && a.allowed }

// CheckedPost reports whether checked-post limits apply.
func (a TableAuthorization) CheckedPost() bool { return a.call.CheckedPost }

// AuthorizeTableWrite checks the gates before the idempotency boundary.
func (s *Service) AuthorizeTableWrite(ctx context.Context, in TableInput, action string) TableAuthorization {
	c, filter, auth, allowed := s.authorizeTable(ctx, in.Call, in.Table, action)
	return TableAuthorization{call: c, filter: filter, Authorization: auth, allowed: allowed, verified: true, table: in.Table, action: action}
}

// TableResult contains the response and any ordered authorization responses.
type TableResult struct {
	Body          map[string]any
	Authorization *Error
	Notify        func()
}

// openTableConn opens the per-site database and translates runtime errors.
// Write callers pass their bounded lock context so quota measurement and the
// connection share the same deadline as the rest of the mutation.
func (s *Service) openTableConn(ctx context.Context, c Call, write bool, transitionalGated ...bool) (*sqlite3.Conn, func(), error) {
	timeout := s.deps.SQLiteTimeout
	var remaining func() (int64, error)
	if write {
		timeout = s.deps.WriteTimeout
		remaining = func() (int64, error) {
			u, err := s.MeasureSiteStorage(ctx, c.SiteID, c.RuntimeDir)
			return SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool) - s.BudgetUsed(u), err
		}
	}
	var conn *sqlite3.Conn
	var done func()
	var err error
	if len(transitionalGated) != 0 && transitionalGated[0] {
		conn, done, err = runtime.OpenTableConnGated(ctx, s.deps.SQLitePath, write, s.deps.SQLiteGate, timeout, s.deps.SQLiteMaxMemory, remaining)
	} else {
		conn, done, err = runtime.OpenTableConn(ctx, s.deps.SQLitePath, write, timeout, s.deps.SQLiteMaxMemory, remaining)
	}
	if e := sqliteFailure(err); e != err {
		return nil, nil, e
	}
	switch err {
	case nil:
		return conn, done, nil
	case runtime.ErrSQLiteBusy:
		return nil, nil, &Error{Code: "storage_busy", Message: "storage is busy; retry shortly"}
	case runtime.ErrUnknownTable:
		return nil, nil, &Error{Code: "invalid_rows", Message: "unknown table"}
	case runtime.ErrTableFull:
		return nil, nil, &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	default:
		return nil, nil, &Error{Code: "internal_error", Message: "internal server error"}
	}
}

// tableMetadata applies the checks shared by table reads and writes, in the
// same order as the original handler workflow.
type rowMetadata struct {
	cols  map[string]string
	order []string
	rowid string
	hold  bool
}

func tableMetadata(conn *sqlite3.Conn, c Call, table, filter string) (rowMetadata, error) {
	var m rowMetadata
	var err error
	m.cols, m.order, err = runtime.TableInfo(conn, table)
	if err != nil {
		return m, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	if !c.Owner && len(m.cols) > 100 {
		return m, &Error{Code: "invalid_rows", Message: "too many columns"}
	}
	if filter != "" && m.cols["visitor_id"] != "TEXT" {
		return m, &Error{Code: "visitor_id_required", Message: "own needs the visitor_id TEXT column; set the policy again so the server adds it"}
	}
	if c.OwnerFor != "" {
		if m.cols["visitor_id"] != "TEXT" {
			return m, &Error{Code: "visitor_id_required", Message: "adding for someone needs the visitor_id TEXT column; set the policy again so the server adds it"}
		}
		if m.cols["written_by"] != "TEXT" {
			return m, &Error{Code: "written_by_required", Message: "adding for someone needs the written_by TEXT column; set the policy again so the server adds it"}
		}
	}
	m.rowid, err = runtime.RowID(conn, table, m.cols)
	if err != nil {
		return m, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	m.hold = c.Resource.Holds(table)
	if m.hold && m.cols["approved_at"] != "TEXT" {
		return m, &Error{Code: "approved_at_required", Message: "approve_first needs the approved_at TEXT column; set the policy again so the server adds it"}
	}
	return m, nil
}

// authorizeTable preserves the gate and matrix authorization order, including
// ordered nonfatal refusals.
func (s *Service) authorizeTable(ctx context.Context, c Call, table, action string) (Call, string, *Error, bool) {
	c.Context = ctx
	if err := s.Gate(c, action != "read"); err != nil {
		return c, "", err.(*Error), false
	}
	c.CurrentMatrix = c.Resource.TableMatrix(table)
	// for (ResolveOwnerFor) only applies to a table whose read is own
	// (records, personal): anywhere wider, the row the owner "adds for"
	// someone would be visible, or attributed, beyond that one visitor,
	// which is exactly what own exists to prevent. Checked here, on the
	// table's actual matrix, not on whether a visitor_id/written_by column
	// happens to already exist.
	if c.OwnerFor != "" && c.CurrentMatrix.Read != rules.WhoOwn {
		return c, "", &Error{Code: "for_requires_own", Message: "for only adds a row on a table whose read is own (the records or personal presets); this table's read is " + c.CurrentMatrix.Read}, false
	}
	filter, err := s.Allow(&c, action, c.CurrentMatrix.Value(action))
	if err != nil {
		return c, filter, err.(*Error), err.(*Error).Allowed
	}
	return c, filter, nil, true
}

// TableGet authorizes and reads one SQLite table row.
func (s *Service) TableGet(ctx context.Context, in TableInput) (TableResult, error) {
	var out TableResult
	c, filter, auth, allowed := s.authorizeTable(ctx, in.Call, in.Table, "read")
	out.Authorization = auth
	if !allowed {
		return out, nil
	}
	err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, a *Admission) error {
		conn, done, err := s.openTableConn(ctx, c, false)
		if err != nil {
			return err
		}
		defer closeSQLite(func() { defer s.sqliteStage("closed"); defer done(); s.sqliteStage("close") })
		s.sqliteStage("after_open")
		invalid := func(err error) error {
			return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
		}
		meta, err := tableMetadata(conn, c, in.Table, filter)
		if err != nil {
			return err
		}
		order, rowid, hold := meta.order, meta.rowid, meta.hold
		query := "SELECT *, " + rowid + " FROM " + runtime.Quote(in.Table) + " WHERE " + rowid + " = ?"
		params := []any{in.ID}
		if filter != "" {
			query += " AND visitor_id = ?"
			params = append(params, filter)
		}
		if hold && !c.Owner && !c.OwnerOnSite {
			query += " AND approved_at IS NOT NULL"
		}
		err = rowReadAuthorizer(conn, c.Owner)
		if err != nil {
			return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
		}
		stmt, _, err := conn.Prepare(query)
		if err != nil || stmt == nil {
			return invalid(err)
		}
		defer stmt.Close()
		s.sqliteStage("after_prepare")
		if len(params) != stmt.BindCount() {
			return invalid(err)
		}
		for i, v := range params {
			raw, _ := json.Marshal(v)
			if err := runtime.BindStorageParam(stmt, i+1, raw); err != nil {
				return invalid(err)
			}
		}
		if !stmt.Step() {
			if stmt.Err() != nil {
				return sqliteRowFailure(stmt.Err(), &Error{Code: "invalid_rows", Message: "query failed"})
			}
			return &Error{Code: "row_not_found", Message: "row not found"}
		}
		s.sqliteStage("after_step")
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
		return nil
	})
	return out, err
}

// TableList authorizes and lists SQLite table rows.
func (s *Service) TableList(ctx context.Context, in TableInput) (TableResult, error) {
	var out TableResult
	c, filter, auth, allowed := s.authorizeTable(ctx, in.Call, in.Table, "read")
	out.Authorization = auth
	if !allowed {
		return out, nil
	}
	err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, a *Admission) error {
		conn, done, err := s.openTableConn(ctx, c, false)
		if err != nil {
			return err
		}
		defer closeSQLite(func() { defer s.sqliteStage("closed"); defer done(); s.sqliteStage("close") })
		s.sqliteStage("after_open")
		meta, err := tableMetadata(conn, c, in.Table, filter)
		if err != nil {
			return err
		}
		cols, orderCols, rowid, hold := meta.cols, meta.order, meta.rowid, meta.hold
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
			return &Error{Code: "invalid_rows", Message: "invalid limit"}
		}
		order := q.Get("order")
		if order == "" {
			order = rowid
		}
		if _, ok := cols[order]; !ok && order != rowid || order == "visitor_id" && !(c.Owner || c.OwnerOnSite) {
			return &Error{Code: "invalid_rows", Message: "invalid row request"}
		}
		desc := q.Get("desc")
		if desc != "" && desc != "0" && desc != "1" {
			return &Error{Code: "invalid_rows", Message: "desc is 0 or 1"}
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
			return &Error{Code: "invalid_rows", Message: "at most 3 where filters"}
		}
		for _, k := range keys {
			col := strings.TrimPrefix(k, "where.")
			if _, ok := cols[col]; !ok || col == "visitor_id" && !(c.Owner || c.OwnerOnSite) {
				return &Error{Code: "invalid_rows", Message: "invalid row request"}
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
				return &Error{Code: "invalid_rows", Message: "invalid after"}
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
			return sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
		}
		stmt, _, err := conn.Prepare(query)
		if err != nil || stmt == nil {
			return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
		}
		defer stmt.Close()
		s.sqliteStage("after_prepare")
		if err = rowBindAll(stmt, params); err != nil {
			return sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
		}
		rows := make([][]any, 0)
		mine := make([]bool, 0)
		next := ""
		hasMore := false
		resultCols := orderCols
		encodedCols, _ := json.Marshal(resultCols)
		resultBytes := len(encodedCols) + 64
		for stmt.Step() {
			s.sqliteStage("after_step")
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
				return &Error{Code: "result_too_large", Message: "query result is too large"}
			}
			resultBytes += len(encoded)
			rows = append(rows, row)
			mine = append(mine, m)
			next = cursor
		}
		if stmt.Err() != nil {
			return sqliteRowFailure(stmt.Err(), &Error{Code: "invalid_rows", Message: "query failed"})
		}
		if !hasMore {
			next = ""
		}
		result := map[string]any{"columns": resultCols, "rows": rows, "next_after": next}
		if !(c.Owner || c.OwnerOnSite) {
			result["mine"] = mine
		}
		out.Body = result
		return nil
	})
	return out, err
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

func (s *Service) sqliteStage(stage string) {
	if s.sqliteHook != nil {
		s.sqliteHook(stage)
	}
}
