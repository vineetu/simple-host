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

// RowsRequest is a row route's request as the route decoded it: the action
// its method names ("" for a method the routes do not know), the table, the
// row id from the path, for= on an add and the list query.
type RowsRequest struct {
	Call           Call
	Action         string
	Table, ID, For string
	Query          url.Values
}

// TableInput contains the decoded facts for a table operation.
type TableInput struct {
	Call       Call
	Table      string
	ID         int64
	Values     map[string]json.RawMessage
	Query      url.Values
	Authorized TableAuthorization
}

// TableAuthorization is an add, edit or delete as AuthorizeTableWrite
// decided it, before the idempotency boundary: the call as the rules refined
// it, the table, the row and the action. Authorization is the refusal.
type TableAuthorization struct {
	call          Call
	Authorization *Error
	allowed       bool
	verified      bool
	table, action string
	id            int64
	// Account is whose idempotency entries the write uses: the owner's
	// account, or the visitor's.
	Account string
}

// Allowed reports whether the table write was authorized.
func (a TableAuthorization) Allowed() bool { return a.verified && a.allowed }

// AuthorizeTableWrite checks the gates before the idempotency boundary.
func (s *Service) AuthorizeTableWrite(ctx context.Context, in TableInput, action string) TableAuthorization {
	c, auth, allowed := s.authorizeTable(ctx, in.Call, in.Table, action)
	account := in.Call.AccountID
	if !in.Call.Owner {
		account = in.Call.VisitorID
	}
	return TableAuthorization{call: c, Authorization: auth, allowed: allowed, verified: true, table: in.Table, action: action, id: in.ID, Account: account}
}

// TableResult is a row operation's answer: the response body, or the
// refusal in Authorization. Event is a committed write's notification
// (nil sends nothing); WriteRow queues it.
type TableResult struct {
	Body          map[string]any
	Authorization *Error
	Event         *Event
}

// ReadRows answers a row route's read: one row when the path names an id,
// otherwise a page of rows.
func (s *Service) ReadRows(ctx context.Context, req RowsRequest) (TableResult, error) {
	id, err := RowRequest(req.Action, req.Table, req.ID)
	if err != nil {
		return TableResult{}, err
	}
	return s.TableRead(ctx, TableInput{Call: req.Call, Table: req.Table, ID: id, Query: req.Query})
}

// AuthorizeRowWrite decides a row route's add, edit or delete before the
// idempotency boundary, so a replay never skips the gates: the request's
// shape, then for= on an add, then the table's rules. A request that cannot
// run is the error; a refusal is the result's Authorization.
//
// for= is the owner adding the row on behalf of one signed-in visitor (a
// visitor_id or the email they signed in with): ResolveOwnerFor refuses
// anyone but the owner, and a target who never signed in on this site.
// Named viewers and this email resolution are not a Simple Hack concept, so
// a product's bindings refuse for= altogether.
func (s *Service) AuthorizeRowWrite(ctx context.Context, req RowsRequest) (TableAuthorization, error) {
	id, err := RowRequest(req.Action, req.Table, req.ID)
	if err != nil {
		return TableAuthorization{}, err
	}
	c := req.Call
	if ref := strings.TrimSpace(req.For); req.Action == "add" && ref != "" {
		if s.deps.Bindings != nil {
			return TableAuthorization{}, &Error{Code: "for_not_available", Message: "for is not available on Simple Hack"}
		}
		if c.OwnerFor, err = s.ResolveOwnerFor(ctx, c, ref); err != nil {
			return TableAuthorization{}, err
		}
	}
	return s.AuthorizeTableWrite(ctx, TableInput{Call: c, Table: req.Table, ID: id}, req.Action), nil
}

// WriteRow runs an add, edit or delete AuthorizeRowWrite allowed, with the
// request body (the column values; a delete has none), and queues its
// notification once it commits.
func (s *Service) WriteRow(ctx context.Context, a TableAuthorization, body []byte) (TableResult, error) {
	in := TableInput{Call: a.call, Table: a.table, ID: a.id, Authorized: a}
	if a.action != "delete" {
		if a.action == "add" && a.call.CheckedPost {
			if err := CheckPostLimits(body, s.deps.SpamCheckedMaxBytes, s.deps.SpamCheckedMaxLinks); err != nil {
				return TableResult{}, err
			}
		}
		var err error
		if in.Values, err = DecodeRowValues(body, a.action); err != nil {
			return TableResult{}, err
		}
	}
	out, err := s.TableWrite(ctx, in)
	if err != nil {
		return TableResult{}, err
	}
	if out.Event != nil {
		s.Emit(*out.Event)
	}
	return out, nil
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

// rowMetadata is a table's checked columns, rowid expression and whether new
// rows wait for approval.
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

// tableMetadata applies the checks shared by table reads and writes.
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

// rowTable is one authorized row operation's open table: the connection,
// the call as the rules refined it, the table's checked metadata and, for a
// read under rules, the row predicate.
type rowTable struct {
	conn      *sqlite3.Conn
	c         Call
	table     string
	meta      rowMetadata
	predicate ruleengine.Predicate
}

// openTable opens the site's database for one row operation (inside its
// admission), checks the table and settles its rules for op; the caller
// defers the returned close. An add for someone first gives the table its
// written_by column, in the same locked window as the add.
func (s *Service) openTable(ctx context.Context, c Call, table string, op ruleengine.Op) (*rowTable, func(), error) {
	conn, done, err := s.openTableConn(ctx, c, op != ruleengine.OpRead)
	if err != nil {
		return nil, nil, err
	}
	closeTable := func() { closeSQLite(func() { defer s.sqliteStage("closed"); defer done(); s.sqliteStage("close") }) }
	opened := false
	defer func() {
		if !opened {
			closeTable()
		}
	}()
	s.sqliteStage("after_open")
	if op == ruleengine.OpAdd && c.OwnerFor != "" {
		if err := addWrittenByColumn(conn, table); err != nil {
			return nil, nil, err
		}
	}
	t := &rowTable{conn: conn, c: c, table: table}
	if t.meta, err = tableMetadata(conn, c, table); err != nil {
		return nil, nil, err
	}
	if op != ruleengine.OpRead {
		err = s.ruleWritePlan(&t.c, table, t.meta, op)
	} else if c.rulePlan != nil {
		t.predicate, err = s.ruleReadPredicate(ctx, &t.c, table, t.meta)
	}
	if err != nil {
		return nil, nil, err
	}
	opened = true
	return t, closeTable, nil
}

// TableRead authorizes and reads a table: one row when in names an id,
// otherwise a page of rows. A refusal is the result's Authorization.
func (s *Service) TableRead(ctx context.Context, in TableInput) (TableResult, error) {
	c, auth, allowed := s.authorizeTable(ctx, in.Call, in.Table, "read")
	if !allowed {
		return TableResult{Authorization: auth}, nil
	}
	var out TableResult
	err := s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *Admission) error {
		t, closeTable, err := s.openTable(ctx, c, in.Table, ruleengine.OpRead)
		if err != nil {
			return err
		}
		defer closeTable()
		if in.ID != 0 {
			out, err = s.readRow(t, in.ID)
		} else {
			out, err = s.readRows(t, in.Query)
		}
		return err
	})
	if err != nil {
		return TableResult{}, err
	}
	return out, nil
}

// TableWrite runs an add, edit or delete AuthorizeTableWrite allowed for
// this table. It takes the site write lock and an admission slot, opens the
// table, and keeps the change, its reference and rule checks and the commit
// in one SQLite transaction. The answer is settled before the commit, so a
// panic after the commit keeps it. A refusal is the result's Authorization.
func (s *Service) TableWrite(ctx context.Context, in TableInput) (TableResult, error) {
	a := in.Authorized
	if !a.Allowed() || a.table != in.Table {
		return TableResult{Authorization: a.Authorization}, nil
	}
	c, op := a.call, tableActionOps[a.action]
	ctx, unlock, err := s.writeLock(ctx, c, true)
	if err != nil {
		return TableResult{}, err
	}
	defer unlock()
	var out TableResult
	err = s.deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, admission *Admission) error {
		c.Context = ctx
		t, closeTable, err := s.openTable(ctx, c, in.Table, op)
		if err != nil {
			return err
		}
		defer closeTable()
		commit, rollback, err := beginTableWrite(t.conn)
		if err != nil {
			return err
		}
		defer rollback()
		var result TableResult
		switch op {
		case ruleengine.OpAdd:
			result, err = s.addRow(t, in.Values)
		case ruleengine.OpEdit:
			result, err = s.editRow(t, in.ID, in.Values)
		default:
			result, err = s.removeRow(t, in.ID)
		}
		if err != nil {
			return err
		}
		out = result
		return s.finishTableWrite(t.conn, t.c, commit, admission)
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
func (s *Service) prepareRowRead(t *rowTable, query string, ruleArgs []ruleengine.Value, params []json.RawMessage) (*sqlite3.Stmt, error) {
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

// readRow reads one row of an open table.
func (s *Service) readRow(t *rowTable, id int64) (TableResult, error) {
	c, meta := t.c, t.meta
	query := "SELECT *, " + meta.rowid + " FROM " + runtime.Quote(t.table) + " WHERE " + meta.rowid + " = ?"
	if meta.hold && !seesEveryRow(c) {
		query += " AND approved_at IS NOT NULL"
	}
	var ruleArgs []ruleengine.Value
	if c.rulePlan != nil {
		var err error
		if query, ruleArgs, err = runtime.RuleSelectOne(t.table, meta.rowid, ruleengine.Int(id), t.predicate); err != nil {
			return TableResult{}, rulesInvalid()
		}
	}
	stmt, err := s.prepareRowRead(t, query, ruleArgs, []json.RawMessage{rowJSON(id)})
	if err != nil {
		return TableResult{}, err
	}
	defer stmt.Close()
	if !stmt.Step() {
		if stmt.Err() != nil {
			return TableResult{}, sqliteRowFailure(stmt.Err(), &Error{Code: "invalid_rows", Message: "query failed"})
		}
		return TableResult{}, &Error{Code: "row_not_found", Message: "row not found"}
	}
	s.sqliteStage("after_step")
	row, mine := visibleRow(stmt, meta.order, c)
	body := map[string]any{"columns": meta.order, "row": row}
	if !seesEveryRow(c) {
		body["mine"] = mine
	}
	return TableResult{Body: body}, nil
}

// readRows reads one page of an open table's rows.
func (s *Service) readRows(t *rowTable, q url.Values) (TableResult, error) {
	page, err := parseRowPage(q, t.c, t.meta)
	if err != nil {
		return TableResult{}, err
	}
	var query string
	var params []json.RawMessage
	var ruleArgs []ruleengine.Value
	if t.c.rulePlan != nil {
		if query, ruleArgs, err = runtime.RuleSelectPage(t.table, t.meta.rowid, page.rule, t.predicate); err != nil {
			return TableResult{}, rulesInvalid()
		}
	} else {
		query, params = page.query(t.table, t.meta.rowid, t.meta.hold && !seesEveryRow(t.c))
	}
	stmt, err := s.prepareRowRead(t, query, ruleArgs, params)
	if err != nil {
		return TableResult{}, err
	}
	defer stmt.Close()
	return s.readRowPage(stmt, page, t.c, t.meta.order)
}

// rowPage is a parsed list request: page size, sort and the cursor of the
// previous page's last row; rule is the same page, with the equality
// filters, for the rules' query builder.
type rowPage struct {
	limit      int
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
	order := cmp.Or(q.Get("order"), meta.rowid)
	if _, known := meta.cols[order]; !known && order != meta.rowid || hiddenColumn(c, order) {
		return nil, invalid("invalid row request")
	}
	desc := q.Get("desc")
	if desc != "" && desc != "0" && desc != "1" {
		return nil, invalid("desc is 0 or 1")
	}
	p := &rowPage{limit: limit, orderIndex: slices.Index(meta.order, order), rule: runtime.RulePage{Order: order, Desc: desc == "1", Limit: limit + 1}}
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
		if err != nil || json.Unmarshal(b, &cur) != nil || cur.Order != order || cur.Desc != p.rule.Desc || cur.Value == nil || !runtime.StorageScalar(cur.Value) {
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
	if p.rule.Desc {
		direction, op = " DESC", "<"
	}
	order := runtime.Quote(p.rule.Order)
	if p.rule.Order == rowid {
		order = rowid
	}
	switch {
	case p.after == nil:
	case string(p.after.Value) == "null" && p.rule.Desc:
		where = append(where, "("+order+" IS NULL AND "+rowid+op+"?)")
	case string(p.after.Value) == "null":
		where = append(where, "("+order+" IS NOT NULL OR ("+order+" IS NULL AND "+rowid+op+"?))")
	default:
		nulls := ""
		if p.rule.Desc {
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
		id := stmt.ColumnInt64(len(columns))
		value := any(id)
		if p.orderIndex >= 0 {
			value = row[p.orderIndex]
		}
		raw, _ := json.Marshal(value)
		b, _ := json.Marshal(rowCursor{p.rule.Order, p.rule.Desc, raw, id})
		next = base64.RawURLEncoding.EncodeToString(b)
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
