package rows

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/host/data"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/host/httpx"
	runtime "github.com/vsriram/simple-host/host/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// RowsRequest is a row route's request as the route decoded it: the action
// its method names ("" for a method the routes do not know), the table, the
// row id from the path, for= on an add and the list query.
type RowsRequest struct {
	Call           data.Call
	Action         string
	Table, ID, For string
	Query          url.Values
}

// TableInput contains the decoded facts for a table operation.
type TableInput struct {
	Call       data.Call
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
	call          data.Call
	Authorization *data.Error
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
func AuthorizeTableWrite(s *data.Service, ctx context.Context, in TableInput, action string) TableAuthorization {
	c, auth, allowed := authorizeTable(s, ctx, in.Call, in.Table, action)
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
	Authorization *data.Error
	Event         *data.Event
}

// ReadRows answers a row route's read: one row when the path names an id,
// otherwise a page of rows.
func ReadRows(s *data.Service, ctx context.Context, req RowsRequest) (TableResult, error) {
	id, err := RowRequest(req.Action, req.Table, req.ID)
	if err != nil {
		return TableResult{}, err
	}
	return TableRead(s, ctx, TableInput{Call: req.Call, Table: req.Table, ID: id, Query: req.Query})
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
func AuthorizeRowWrite(s *data.Service, ctx context.Context, req RowsRequest) (TableAuthorization, error) {
	id, err := RowRequest(req.Action, req.Table, req.ID)
	if err != nil {
		return TableAuthorization{}, err
	}
	c := req.Call
	if ref := strings.TrimSpace(req.For); req.Action == "add" && ref != "" {
		if s.Deps.Bindings != nil {
			return TableAuthorization{}, &data.Error{Code: "for_not_available", Message: "for is not available on Simple Hack"}
		}
		if c.OwnerFor, err = s.ResolveOwnerFor(ctx, c, ref); err != nil {
			return TableAuthorization{}, err
		}
	}
	return AuthorizeTableWrite(s, ctx, TableInput{Call: c, Table: req.Table, ID: id}, req.Action), nil
}

// WriteRow runs an add, edit or delete AuthorizeRowWrite allowed, with the
// request body (the column values; a delete has none), and queues its
// notification once it commits.
func WriteRow(s *data.Service, ctx context.Context, a TableAuthorization, body []byte) (TableResult, error) {
	in := TableInput{Call: a.call, Table: a.table, ID: a.id, Authorized: a}
	if a.action != "delete" {
		if a.action == "add" && a.call.CheckedPost {
			if err := data.CheckPostLimits(body, s.Deps.SpamCheckedMaxBytes, s.Deps.SpamCheckedMaxLinks); err != nil {
				return TableResult{}, err
			}
		}
		var err error
		if in.Values, err = DecodeRowValues(body, a.action); err != nil {
			return TableResult{}, err
		}
	}
	out, err := TableWrite(s, ctx, in)
	if err != nil {
		return TableResult{}, err
	}
	if out.Event != nil {
		s.Emit(*out.Event)
	}
	return out, nil
}

var tableActionOps = map[string]ruleengine.Op{"read": ruleengine.OpRead, "add": ruleengine.OpAdd, "edit": ruleengine.OpEdit, "delete": ruleengine.OpDelete}

// authorizeTable runs authorize for one table. An add for someone (OwnerFor)
// first needs read rules that limit the table's rows to their visitor or the
// owner.
func authorizeTable(s *data.Service, ctx context.Context, c data.Call, table, action string) (data.Call, *data.Error, bool) {
	if c.OwnerFor != "" {
		if err := s.Gate(c, true); err != nil {
			return c, err.(*data.Error), false
		}
		if err := s.CompileBindingRules(&c, table); err != nil {
			return c, err.(*data.Error), false
		}
		if !c.RulePlan.ReadIsOwnOrOwner() {
			return c, &data.Error{Code: "for_requires_own", Message: "for needs read rules limited to the row's visitor or owner"}, false
		}
		c.RulePlan = nil
	}
	err := s.Authorize(ctx, &c, table, tableActionOps[action])
	return c, err, err == nil
}

// rowTable is one authorized row operation's open table: the connection,
// the call as the rules refined it, the table's checked metadata and, for a
// read under rules, the row predicate.
type rowTable struct {
	conn      *sqlite3.Conn
	c         data.Call
	table     string
	meta      data.RowMetadata
	predicate ruleengine.Predicate
}

// openTable opens the site's database for one row operation (inside its
// admission), checks the table and settles its rules for op; the caller
// defers the returned close. An add for someone first gives the table its
// written_by column, in the same locked window as the add.
func openTable(s *data.Service, ctx context.Context, c data.Call, table string, op ruleengine.Op) (*rowTable, func(), error) {
	conn, done, err := s.OpenTableConn(ctx, c, op != ruleengine.OpRead)
	if err != nil {
		return nil, nil, err
	}
	closeTable := func() {
		data.CloseSQLite(func() { defer s.SQLiteStage("closed"); defer done(); s.SQLiteStage("close") })
	}
	opened := false
	defer func() {
		if !opened {
			closeTable()
		}
	}()
	s.SQLiteStage("after_open")
	if op == ruleengine.OpAdd && c.OwnerFor != "" {
		if err := addWrittenByColumn(conn, table); err != nil {
			return nil, nil, err
		}
	}
	t := &rowTable{conn: conn, c: c, table: table}
	if t.meta, err = data.TableMetadata(conn, c, table); err != nil {
		return nil, nil, err
	}
	if op != ruleengine.OpRead {
		err = s.RuleWritePlan(&t.c, table, t.meta, op)
	} else if c.RulePlan != nil {
		t.predicate, err = s.RuleReadPredicate(ctx, &t.c, table, t.meta)
	}
	if err != nil {
		return nil, nil, err
	}
	opened = true
	return t, closeTable, nil
}

// TableRead authorizes and reads a table: one row when in names an id,
// otherwise a page of rows. A refusal is the result's Authorization.
func TableRead(s *data.Service, ctx context.Context, in TableInput) (TableResult, error) {
	c, auth, allowed := authorizeTable(s, ctx, in.Call, in.Table, "read")
	if !allowed {
		return TableResult{Authorization: auth}, nil
	}
	var out TableResult
	err := s.Deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, _ *data.Admission) error {
		t, closeTable, err := openTable(s, ctx, c, in.Table, ruleengine.OpRead)
		if err != nil {
			return err
		}
		defer closeTable()
		if in.ID != 0 {
			out, err = readRow(s, t, in.ID)
		} else {
			out, err = readRows(s, t, in.Query)
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
func TableWrite(s *data.Service, ctx context.Context, in TableInput) (TableResult, error) {
	a := in.Authorized
	if !a.Allowed() || a.table != in.Table {
		return TableResult{Authorization: a.Authorization}, nil
	}
	c, op := a.call, tableActionOps[a.action]
	ctx, unlock, err := s.WriteLock(ctx, c, true)
	if err != nil {
		return TableResult{}, err
	}
	defer unlock()
	var out TableResult
	err = s.Deps.SiteSlots.Admit(ctx, c.SiteID, func(ctx context.Context, admission *data.Admission) error {
		c.Context = ctx
		t, closeTable, err := openTable(s, ctx, c, in.Table, op)
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
			result, err = addRow(s, t, in.Values)
		case ruleengine.OpEdit:
			result, err = editRow(s, t, in.ID, in.Values)
		default:
			result, err = removeRow(s, t, in.ID)
		}
		if err != nil {
			return err
		}
		out = result
		return finishTableWrite(s, t.conn, t.c, commit, admission)
	})
	if err != nil {
		return TableResult{}, err
	}
	return out, nil
}

// seesEveryRow reports whether the caller sees every row's visitor_id and
// rows still waiting for approval.
func seesEveryRow(c data.Call) bool { return c.Owner || c.OwnerOnSite }

// isVisitorColumn reports whether name is the column holding the row's
// visitor; under rules SQLite's case-insensitive spelling counts too.
func isVisitorColumn(c data.Call, name string) bool {
	return name == "visitor_id" || c.RulePlan != nil && strings.EqualFold(name, "visitor_id")
}

// hiddenColumn reports whether the caller may not sort or filter by name.
func hiddenColumn(c data.Call, name string) bool { return isVisitorColumn(c, name) && !seesEveryRow(c) }

// visibleRow reads the statement's current row, hiding another visitor's
// visitor_id from a caller who does not see every row. mine reports whether
// the row is the caller's own.
func visibleRow(stmt *sqlite3.Stmt, columns []string, c data.Call) ([]any, bool) {
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
func prepareRowRead(s *data.Service, t *rowTable, query string, ruleArgs []ruleengine.Value, params []json.RawMessage) (*sqlite3.Stmt, error) {
	if err := data.RowReadAuthorizer(t.conn, t.c.Owner); err != nil {
		return nil, data.SQLiteRowFailure(err, &data.Error{Code: "internal_error", Message: "internal server error"})
	}
	stmt, _, err := t.conn.Prepare(query)
	if err != nil || stmt == nil {
		return nil, data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	bound := false
	defer func() {
		if !bound {
			stmt.Close()
		}
	}()
	s.SQLiteStage("after_prepare")
	if t.c.RulePlan != nil {
		err = runtime.BindRuleArgs(stmt, 1, ruleArgs)
	} else {
		err = rowBindAll(stmt, params)
	}
	if err != nil {
		return nil, data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "invalid row request"})
	}
	bound = true
	return stmt, nil
}

// readRow reads one row of an open table.
func readRow(s *data.Service, t *rowTable, id int64) (TableResult, error) {
	c, meta := t.c, t.meta
	query := "SELECT *, " + meta.RowID + " FROM " + runtime.Quote(t.table) + " WHERE " + meta.RowID + " = ?"
	if meta.Hold && !seesEveryRow(c) {
		query += " AND approved_at IS NOT NULL"
	}
	var ruleArgs []ruleengine.Value
	if c.RulePlan != nil {
		var err error
		if query, ruleArgs, err = runtime.RuleSelectOne(t.table, meta.RowID, ruleengine.Int(id), t.predicate); err != nil {
			return TableResult{}, data.RulesInvalid()
		}
	}
	stmt, err := prepareRowRead(s, t, query, ruleArgs, []json.RawMessage{data.RowJSON(id)})
	if err != nil {
		return TableResult{}, err
	}
	defer stmt.Close()
	if !stmt.Step() {
		if stmt.Err() != nil {
			return TableResult{}, data.SQLiteRowFailure(stmt.Err(), &data.Error{Code: "invalid_rows", Message: "query failed"})
		}
		return TableResult{}, &data.Error{Code: "row_not_found", Message: "row not found"}
	}
	s.SQLiteStage("after_step")
	row, mine := visibleRow(stmt, meta.Order, c)
	body := map[string]any{"columns": meta.Order, "row": row}
	if !seesEveryRow(c) {
		body["mine"] = mine
	}
	return TableResult{Body: body}, nil
}

// readRows reads one page of an open table's rows.
func readRows(s *data.Service, t *rowTable, q url.Values) (TableResult, error) {
	page, err := parseRowPage(q, t.c, t.meta)
	if err != nil {
		return TableResult{}, err
	}
	var query string
	var params []json.RawMessage
	var ruleArgs []ruleengine.Value
	if t.c.RulePlan != nil {
		if query, ruleArgs, err = runtime.RuleSelectPage(t.table, t.meta.RowID, page.rule, t.predicate); err != nil {
			return TableResult{}, data.RulesInvalid()
		}
	} else {
		query, params = page.query(t.table, t.meta.RowID, t.meta.Hold && !seesEveryRow(t.c))
	}
	stmt, err := prepareRowRead(s, t, query, ruleArgs, params)
	if err != nil {
		return TableResult{}, err
	}
	defer stmt.Close()
	return readRowPage(s, stmt, page, t.c, t.meta.Order)
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
func parseRowPage(q url.Values, c data.Call, meta data.RowMetadata) (*rowPage, error) {
	invalid := func(msg string) error { return &data.Error{Code: "invalid_rows", Message: msg} }
	limit, ok := httpx.Limit(q, 100, 500)
	if !ok {
		return nil, invalid("invalid limit")
	}
	order := cmp.Or(q.Get("order"), meta.RowID)
	if _, known := meta.Cols[order]; !known && order != meta.RowID || hiddenColumn(c, order) {
		return nil, invalid("invalid row request")
	}
	desc := q.Get("desc")
	if desc != "" && desc != "0" && desc != "1" {
		return nil, invalid("desc is 0 or 1")
	}
	p := &rowPage{limit: limit, orderIndex: slices.Index(meta.Order, order), rule: runtime.RulePage{Order: order, Desc: desc == "1", Limit: limit + 1}}
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
		if _, known := meta.Cols[col]; !known || hiddenColumn(c, col) {
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
		params = append(params, data.RowJSON(f.Value.Text))
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
		params = append(params, data.RowJSON(p.after.ID))
	}
	query := "SELECT *, " + rowid + " FROM " + runtime.Quote(table)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY " + order + direction + ", " + rowid + direction + " LIMIT ?"
	return query, append(params, data.RowJSON(p.limit+1))
}

// readRowPage steps through one page of rows, stopping at the page size or
// the result size cap, and shapes the list response with the next cursor.
func readRowPage(s *data.Service, stmt *sqlite3.Stmt, p *rowPage, c data.Call, columns []string) (TableResult, error) {
	encodedCols, _ := json.Marshal(columns)
	size, maxSize := len(encodedCols)+64, data.ResultLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES"))
	rows, mine := make([][]any, 0), make([]bool, 0)
	next, hasMore := "", false
	for stmt.Step() {
		s.SQLiteStage("after_step")
		if len(rows) >= p.limit {
			hasMore = true
			break
		}
		// The order column is never hidden (parseRowPage), so the cursor
		// can be taken from the visible row.
		row, m := visibleRow(stmt, columns, c)
		encoded, err := json.Marshal(row)
		if err != nil || size+len(encoded) > maxSize {
			return TableResult{}, &data.Error{Code: "result_too_large", Message: "query result is too large"}
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
		return TableResult{}, data.SQLiteRowFailure(err, &data.Error{Code: "invalid_rows", Message: "query failed"})
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
