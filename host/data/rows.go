package data

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/httpx"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
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
	c, auth, allowed := s.authorizeTable(ctx, in.Call, in.Table, action)
	return TableAuthorization{call: c, Authorization: auth, allowed: allowed, verified: true, table: in.Table, action: action}
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
func (s *Service) openTableConn(ctx context.Context, c Call, write bool) (*sqlite3.Conn, func(), error) {
	timeout := s.deps.SQLiteTimeout
	var remaining func() (int64, error)
	if write {
		timeout = s.deps.WriteTimeout
		remaining = func() (int64, error) {
			u, err := s.measureSiteStorageAdmitted(ctx, c.SiteID, c.RuntimeDir)
			return SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool) - s.BudgetUsed(u), err
		}
	}
	conn, done, err := runtime.OpenTableConn(ctx, s.deps.SQLitePath, write, timeout, s.deps.SQLiteMaxMemory, remaining)
	var storageErr *Error
	if errors.As(err, &storageErr) && storageErr.Code == "storage_busy" {
		return nil, nil, storageErr
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

// declaredColumn resolves SQLite's case-insensitive names to their schema spelling.
func declaredColumn(cols map[string]string, name string) string {
	for column := range cols {
		if strings.EqualFold(column, name) {
			return column
		}
	}
	return ""
}

func tableMetadata(conn *sqlite3.Conn, c Call, table string) (rowMetadata, error) {
	var m rowMetadata
	var err error
	if c.rulePlan != nil {
		m.cols, m.order, err = runtime.TableXInfo(conn, table)
	} else {
		m.cols, m.order, err = runtime.TableInfo(conn, table)
	}
	if err != nil {
		return m, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	if !c.Owner && len(m.cols) > 100 {
		return m, &Error{Code: "invalid_rows", Message: "too many columns"}
	}
	visitorColumn := declaredColumn(m.cols, "visitor_id")
	writtenByColumn := declaredColumn(m.cols, "written_by")
	if c.OwnerFor != "" {
		if m.cols[visitorColumn] != "TEXT" {
			return m, &Error{Code: "visitor_id_required", Message: "adding for someone needs the visitor_id TEXT column; set the policy again so the server adds it"}
		}
		if m.cols[writtenByColumn] != "TEXT" {
			return m, &Error{Code: "written_by_required", Message: "adding for someone needs the written_by TEXT column; set the policy again so the server adds it"}
		}
	}
	m.rowid, err = runtime.RowID(conn, table, m.cols)
	if err != nil {
		return m, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	approvalColumn := declaredColumn(m.cols, "approved_at")
	if c.rulePlan != nil {
		m.hold = c.rulePlan.ReferencesColumn("approved_at") && m.cols[approvalColumn] == "TEXT"
	} else {
		// Owners bypass row rules but keep the same approval stamping.
		set, errs := ruleengine.DecodeSet(c.Resource.EffectiveRules(table))
		if len(errs) == 0 {
			_, hold, err := ruleengine.RequiredColumns(set, PlatformSchema(true), ruleengine.DefaultLimits())
			m.hold = err == nil && hold && m.cols[approvalColumn] == "TEXT"
		}
	}
	if m.hold && m.cols[approvalColumn] != "TEXT" {
		return m, &Error{Code: "approved_at_required", Message: "approve_first needs the approved_at TEXT column; set the policy again so the server adds it"}
	}
	return m, nil
}

var tableActionOps = map[string]ruleengine.Op{"read": ruleengine.OpRead, "add": ruleengine.OpAdd, "edit": ruleengine.OpEdit, "delete": ruleengine.OpDelete}

// authorizeTable runs authorize for one table. An add for someone (OwnerFor)
// first needs read rules that limit the table's rows to their visitor or the
// owner.
func (s *Service) authorizeTable(ctx context.Context, c Call, table, action string) (Call, *Error, bool) {
	if c.OwnerFor != "" {
		if err := s.Gate(c, true); err != nil {
			return c, err.(*Error), false
		}
		if err := s.compileBindingRules(&c, table); err != nil {
			return c, err.(*Error), false
		}
		if !c.rulePlan.ReadIsOwnOrOwner() {
			return c, &Error{Code: "for_requires_own", Message: "for needs read rules limited to the row's visitor or owner"}, false
		}
		c.rulePlan = nil
	}
	err := s.authorize(ctx, &c, table, tableActionOps[action])
	return c, err, err == nil
}

// tableSession is one authorized row operation on an open connection: the
// call as the rules refined it, the table's checked metadata and, for a read
// under rules, the row predicate.
type tableSession struct {
	conn      *sqlite3.Conn
	c         Call
	table     string
	meta      rowMetadata
	predicate ruleengine.Predicate
	a         *Admission
}

// withTable owns the steps every row route shares: authorize, take the site
// write lock for a change, admit the site, open the database, check the
// table and settle its rules, then run work. work fills out before it
// commits, so a panic after the commit keeps the result. A refusal is
// returned as the result's Authorization with no error.
func (s *Service) withTable(ctx context.Context, in TableInput, action string, work func(t *tableSession, out *TableResult) error) (TableResult, error) {
	// A read is authorized here; a change was authorized before the
	// idempotency boundary (AuthorizeTableWrite), for this table and action.
	c, auth, allowed := in.Authorized.call, in.Authorized.Authorization, in.Authorized.Allowed() && in.Authorized.table == in.Table && in.Authorized.action == action
	if action == "read" {
		c, auth, allowed = s.authorizeTable(ctx, in.Call, in.Table, action)
	}
	if !allowed {
		return TableResult{Authorization: auth}, nil
	}
	op := tableActionOps[action]
	write := op != ruleengine.OpRead
	if write {
		lockCtx, unlock, err := s.writeLock(ctx, c.OwnerID+"/"+c.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
		if err != nil {
			return TableResult{}, err
		}
		defer unlock()
		ctx = lockCtx
	}
	var out TableResult
	err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, a *Admission) error {
		if write {
			c.Context = ctx
		}
		conn, done, err := s.openTableConn(ctx, c, write)
		if err != nil {
			return err
		}
		defer closeSQLite(func() { defer s.sqliteStage("closed"); defer done(); s.sqliteStage("close") })
		s.sqliteStage("after_open")
		if op == ruleengine.OpAdd && c.OwnerFor != "" {
			if err := addWrittenByColumn(conn, in.Table); err != nil {
				return err
			}
		}
		t := &tableSession{conn: conn, c: c, table: in.Table, a: a}
		if t.meta, err = tableMetadata(conn, c, in.Table); err != nil {
			return err
		}
		if write {
			err = s.ruleWritePlan(&t.c, in.Table, t.meta, op)
		} else if c.rulePlan != nil {
			t.predicate, err = s.ruleReadPredicate(ctx, &t.c, in.Table, t.meta)
		}
		if err != nil {
			return err
		}
		return work(t, &out)
	})
	if err != nil {
		return TableResult{}, err
	}
	return out, nil
}

// seesEveryRow reports whether the caller sees every row's visitor_id and
// rows still waiting for approval.
func seesEveryRow(c Call) bool { return c.Owner || c.OwnerOnSite }

// isVisitorColumn reports whether name is the column holding the row's
// visitor; under rules SQLite's case-insensitive spelling counts too.
func isVisitorColumn(c Call, name string) bool {
	return name == "visitor_id" || c.rulePlan != nil && strings.EqualFold(name, "visitor_id")
}

// hiddenColumn reports whether the caller may not sort or filter by name.
func hiddenColumn(c Call, name string) bool { return isVisitorColumn(c, name) && !seesEveryRow(c) }

// visibleRow reads the statement's current row, hiding another visitor's
// visitor_id from a caller who does not see every row. mine reports whether
// the row is the caller's own.
func visibleRow(stmt *sqlite3.Stmt, columns []string, c Call) ([]any, bool) {
	row := make([]any, len(columns))
	mine := false
	for i, name := range columns {
		row[i] = runtime.StorageColumn(stmt, i)
		if !isVisitorColumn(c, name) {
			continue
		}
		id, _ := row[i].(string)
		mine = id != "" && c.VisitorID != "" && id == c.VisitorID
		if !mine && !seesEveryRow(c) {
			row[i] = nil
		}
	}
	return row, mine
}

// prepareRowRead compiles a row read under the caller's read authorizer and
// binds its values: the rules' arguments under rules, params otherwise.
func (s *Service) prepareRowRead(t *tableSession, query string, ruleArgs []ruleengine.Value, params []json.RawMessage) (*sqlite3.Stmt, error) {
	if err := rowReadAuthorizer(t.conn, t.c.Owner); err != nil {
		return nil, sqliteRowFailure(err, &Error{Code: "internal_error", Message: "internal server error"})
	}
	stmt, _, err := t.conn.Prepare(query)
	if err != nil || stmt == nil {
		return nil, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	bound := false
	defer func() {
		if !bound {
			stmt.Close()
		}
	}()
	s.sqliteStage("after_prepare")
	if t.c.rulePlan != nil {
		err = runtime.BindRuleArgs(stmt, 1, ruleArgs)
	} else {
		err = rowBindAll(stmt, params)
	}
	if err != nil {
		return nil, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	bound = true
	return stmt, nil
}

// TableGet authorizes and reads one SQLite table row.
func (s *Service) TableGet(ctx context.Context, in TableInput) (TableResult, error) {
	return s.withTable(ctx, in, "read", func(t *tableSession, out *TableResult) error {
		c, meta := t.c, t.meta
		query := "SELECT *, " + meta.rowid + " FROM " + runtime.Quote(in.Table) + " WHERE " + meta.rowid + " = ?"
		if meta.hold && !seesEveryRow(c) {
			query += " AND approved_at IS NOT NULL"
		}
		var ruleArgs []ruleengine.Value
		if c.rulePlan != nil {
			var err error
			if query, ruleArgs, err = runtime.RuleSelectOne(in.Table, meta.rowid, ruleengine.Int(in.ID), t.predicate); err != nil {
				return rulesInvalid()
			}
		}
		stmt, err := s.prepareRowRead(t, query, ruleArgs, []json.RawMessage{rowJSON(in.ID)})
		if err != nil {
			return err
		}
		defer stmt.Close()
		if !stmt.Step() {
			if stmt.Err() != nil {
				return sqliteRowFailure(stmt.Err(), &Error{Code: "invalid_rows", Message: "query failed"})
			}
			return &Error{Code: "row_not_found", Message: "row not found"}
		}
		s.sqliteStage("after_step")
		row, mine := visibleRow(stmt, meta.order, c)
		body := map[string]any{"columns": meta.order, "row": row}
		if !seesEveryRow(c) {
			body["mine"] = mine
		}
		out.Body = body
		return nil
	})
}

// TableList authorizes and lists SQLite table rows.
func (s *Service) TableList(ctx context.Context, in TableInput) (TableResult, error) {
	return s.withTable(ctx, in, "read", func(t *tableSession, out *TableResult) error {
		page, err := parseRowPage(in.Query, t.c, t.meta)
		if err != nil {
			return err
		}
		var query string
		var params []json.RawMessage
		var ruleArgs []ruleengine.Value
		if t.c.rulePlan != nil {
			if query, ruleArgs, err = runtime.RuleSelectPage(in.Table, t.meta.rowid, page.rule, t.predicate); err != nil {
				return rulesInvalid()
			}
		} else {
			query, params = page.query(in.Table, t.meta.rowid, t.meta.hold && !seesEveryRow(t.c))
		}
		stmt, err := s.prepareRowRead(t, query, ruleArgs, params)
		if err != nil {
			return err
		}
		defer stmt.Close()
		*out, err = s.readRowPage(stmt, page, t.c, t.meta.order)
		return err
	})
}

// rowPage is a parsed list request: page size, sort and the cursor of the
// previous page's last row; rule is the same page, with the equality
// filters, for the rules' query builder.
type rowPage struct {
	limit      int
	order      string
	desc       bool
	orderIndex int
	after      *rowCursor
	rule       runtime.RulePage
}

type rowCursor struct {
	Order string          `json:"order"`
	Desc  bool            `json:"desc"`
	Value json.RawMessage `json:"value"`
	ID    int64           `json:"id"`
}

// parseRowPage reads and checks a list request's limit, order, desc, where.*
// filters (equality on real columns, at most three) and after cursor.
// Owner-only options use the refreshed owner-on-site status.
func parseRowPage(q url.Values, c Call, meta rowMetadata) (*rowPage, error) {
	invalid := func(msg string) error { return &Error{Code: "invalid_rows", Message: msg} }
	limit, ok := httpx.Limit(q, 100, 500)
	if !ok {
		return nil, invalid("invalid limit")
	}
	p := &rowPage{limit: limit, order: cmp.Or(q.Get("order"), meta.rowid)}
	if _, known := meta.cols[p.order]; !known && p.order != meta.rowid || hiddenColumn(c, p.order) {
		return nil, invalid("invalid row request")
	}
	desc := q.Get("desc")
	if desc != "" && desc != "0" && desc != "1" {
		return nil, invalid("desc is 0 or 1")
	}
	p.desc = desc == "1"
	p.orderIndex = slices.Index(meta.order, p.order)
	p.rule = runtime.RulePage{Order: p.order, Desc: p.desc, Limit: limit + 1}
	keys := []string{}
	for k := range q {
		if strings.HasPrefix(k, "where.") {
			keys = append(keys, k)
		}
	}
	if len(keys) > 3 {
		return nil, invalid("at most 3 where filters")
	}
	sort.Strings(keys)
	for _, k := range keys {
		col := strings.TrimPrefix(k, "where.")
		if _, known := meta.cols[col]; !known || hiddenColumn(c, col) {
			return nil, invalid("invalid row request")
		}
		p.rule.Filters = append(p.rule.Filters, runtime.RuleFilter{Column: col, Value: ruleengine.Text(q.Get(k))})
	}
	if after := q.Get("after"); after != "" {
		b, err := base64.RawURLEncoding.DecodeString(after)
		var cur rowCursor
		if err != nil || json.Unmarshal(b, &cur) != nil || cur.Order != p.order || cur.Desc != p.desc || cur.Value == nil || !runtime.StorageScalar(cur.Value) {
			return nil, invalid("invalid after")
		}
		value, err := ruleScalar(cur.Value)
		if err != nil {
			return nil, invalid("invalid after")
		}
		p.after, p.rule.After = &cur, &runtime.RuleCursor{Value: value, ID: ruleengine.Int(cur.ID)}
	}
	return p, nil
}

// query builds the page's SELECT for a table without rules. SQLite sorts
// NULL first in ascending order, so the cursor condition places NULL rows
// on the right side of the previous page.
func (p *rowPage) query(table, rowid string, approvedOnly bool) (string, []json.RawMessage) {
	where, params := []string{}, []json.RawMessage{}
	if approvedOnly {
		where = append(where, "approved_at IS NOT NULL")
	}
	for _, f := range p.rule.Filters {
		where = append(where, runtime.Quote(f.Column)+" = ?")
		params = append(params, rowJSON(f.Value.Text))
	}
	direction, op := " ASC", ">"
	if p.desc {
		direction, op = " DESC", "<"
	}
	order := runtime.Quote(p.order)
	if p.order == rowid {
		order = rowid
	}
	switch {
	case p.after == nil:
	case string(p.after.Value) == "null" && p.desc:
		where = append(where, "("+order+" IS NULL AND "+rowid+op+"?)")
	case string(p.after.Value) == "null":
		where = append(where, "("+order+" IS NOT NULL OR ("+order+" IS NULL AND "+rowid+op+"?))")
	default:
		nulls := ""
		if p.desc {
			nulls = order + " IS NULL OR "
		}
		where = append(where, "("+nulls+order+op+"? OR ("+order+" = ? AND "+rowid+op+"?))")
		params = append(params, p.after.Value, p.after.Value)
	}
	if p.after != nil {
		params = append(params, rowJSON(p.after.ID))
	}
	query := "SELECT *, " + rowid + " FROM " + runtime.Quote(table)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY " + order + direction + ", " + rowid + direction + " LIMIT ?"
	return query, append(params, rowJSON(p.limit+1))
}

// readRowPage steps through one page of rows, stopping at the page size or
// the result size cap, and shapes the list response with the next cursor.
func (s *Service) readRowPage(stmt *sqlite3.Stmt, p *rowPage, c Call, columns []string) (TableResult, error) {
	encodedCols, _ := json.Marshal(columns)
	size, maxSize := len(encodedCols)+64, ResultLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES"))
	rows, mine := make([][]any, 0), make([]bool, 0)
	next, hasMore := "", false
	for stmt.Step() {
		s.sqliteStage("after_step")
		if len(rows) >= p.limit {
			hasMore = true
			break
		}
		// The order column is never hidden (parseRowPage), so the cursor
		// can be taken from the visible row.
		row, m := visibleRow(stmt, columns, c)
		encoded, err := json.Marshal(row)
		if err != nil || size+len(encoded) > maxSize {
			return TableResult{}, &Error{Code: "result_too_large", Message: "query result is too large"}
		}
		size += len(encoded)
		rows, mine = append(rows, row), append(mine, m)
		next = p.cursor(row, stmt.ColumnInt64(len(columns)))
	}
	if err := stmt.Err(); err != nil {
		return TableResult{}, sqliteRowFailure(err, &Error{Code: "invalid_rows", Message: "query failed"})
	}
	if !hasMore {
		next = ""
	}
	body := map[string]any{"columns": columns, "rows": rows, "next_after": next}
	if !seesEveryRow(c) {
		body["mine"] = mine
	}
	return TableResult{Body: body}, nil
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

func ruleScalar(raw json.RawMessage) (ruleengine.Value, error) {
	if string(raw) == "null" {
		return ruleengine.Null(), nil
	}
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return ruleengine.Value{}, err
	}
	switch v := v.(type) {
	case string:
		return ruleengine.Text(v), nil
	case bool:
		return ruleengine.Bool(v), nil
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return ruleengine.Int(i), nil
		}
		f, err := v.Float64()
		return ruleengine.Float(f), err
	}
	return ruleengine.Value{}, fmt.Errorf("invalid scalar")
}
