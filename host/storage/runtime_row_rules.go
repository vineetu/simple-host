package storage

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// ErrStatementTooLarge means the complete statement exceeds a SQLite connection
// limit, even though each of its predicates may fit the engine's own limits.
var ErrStatementTooLarge = errors.New("rules statement exceeds SQLite limits")

// These match OpenTableConn's SQL limit and SQLite's default variable limit.
// Tests lower them to exercise refusals without exceeding the engine's bounds.
var ruleStatementBytes = 32768
var ruleStatementArgs = 32766

// RuleFilter is one already validated where.<column> equality filter.
type RuleFilter struct {
	Column string
	Value  ruleengine.Value
}

// RuleCursor carries the decoded cursor's sort value and row key.
type RuleCursor struct {
	Value ruleengine.Value
	ID    ruleengine.Value
}

// RulePage contains validated listing options. Limit includes the extra row used
// to determine next_after. An empty Order means the resolved row key.
type RulePage struct {
	Filters []RuleFilter
	Order   string
	Desc    bool
	After   *RuleCursor
	Limit   int
}

// RuleSelectOne reads a row only when the read predicate permits it. All Rule
// builders take engine-produced predicates and a resolved RowID (or "key" for
// KV), never SQL supplied by a request.
func RuleSelectOne(table, rowKey string, id ruleengine.Value, read ruleengine.Predicate) (string, []ruleengine.Value, error) {
	query := "SELECT *, " + rowKey + " FROM " + Quote(table) + " WHERE " + rowKey + " = ? AND (" + read.SQL + ")"
	return ruleStatement(rowKey, query, append([]ruleengine.Value{id}, read.Args...))
}

// RuleSelectPage preserves the row API's equality filters, NULL ordering and
// row-key tie break. Column names come from the inspected schema, not SQL text.
func RuleSelectPage(table, rowKey string, page RulePage, read ruleengine.Predicate) (string, []ruleengine.Value, error) {
	order := Quote(page.Order)
	if page.Order == "" || page.Order == rowKey {
		order = rowKey
	}
	direction, op := " ASC", ">"
	if page.Desc {
		direction, op = " DESC", "<"
	}
	where := make([]string, 0, len(page.Filters)+2)
	args := make([]ruleengine.Value, 0)
	for _, filter := range page.Filters {
		where = append(where, Quote(filter.Column)+" = ?")
		args = append(args, filter.Value)
	}
	if cur := page.After; cur != nil {
		if cur.Value.Kind == ruleengine.KindNull {
			if page.Desc {
				where = append(where, "("+order+" IS NULL AND "+rowKey+op+"?)")
			} else {
				where = append(where, "("+order+" IS NOT NULL OR ("+order+" IS NULL AND "+rowKey+op+"?))")
			}
			args = append(args, cur.ID)
		} else {
			nulls := ""
			if page.Desc {
				nulls = order + " IS NULL OR "
			}
			where = append(where, "("+nulls+order+op+"? OR ("+order+" = ? AND "+rowKey+op+"?))")
			args = append(args, cur.Value, cur.Value, cur.ID)
		}
	}
	where = append(where, "("+read.SQL+")")
	args = append(args, read.Args...)
	query := "SELECT *, " + rowKey + " FROM " + Quote(table) + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY " + order + direction + ", " + rowKey + direction + " LIMIT ?"
	args = append(args, ruleengine.Int(int64(page.Limit)))
	return ruleStatement(rowKey, query, args)
}

// RuleUpdate requires both read visibility and edit permission on the old row.
// The caller checks the resulting row with RuleCheck before committing. Names
// and values have already passed the row API's column and scalar validation.
// A nonempty visitorAI adds the mandatory visitor-AI author restriction.
func RuleUpdate(table, rowKey string, names []string, values []ruleengine.Value, id ruleengine.Value, read, edit ruleengine.Predicate, visitorAI string) (string, []ruleengine.Value, error) {
	sets := make([]string, len(names))
	for i, name := range names {
		sets[i] = Quote(name) + " = ?"
	}
	args := append([]ruleengine.Value(nil), values...)
	args = append(args, id)
	args = append(args, read.Args...)
	args = append(args, edit.Args...)
	query := "UPDATE OR ABORT " + Quote(table) + " SET " + strings.Join(sets, ", ") + " WHERE " + rowKey + " = ? AND (" + read.SQL + ") AND (" + edit.SQL + ")"
	query, args = ruleAI(query, args, visitorAI)
	return ruleStatement(rowKey, query, args)
}

// RuleDelete requires both read visibility and delete permission on the old row.
func RuleDelete(table, rowKey string, id ruleengine.Value, read, del ruleengine.Predicate, visitorAI string) (string, []ruleengine.Value, error) {
	args := append([]ruleengine.Value{id}, read.Args...)
	args = append(args, del.Args...)
	query := "DELETE FROM " + Quote(table) + " WHERE " + rowKey + " = ? AND (" + read.SQL + ") AND (" + del.SQL + ")"
	query, args = ruleAI(query, args, visitorAI)
	return ruleStatement(rowKey, query, args)
}

// RuleCheck tests the stored, post-affinity row after an insert or update.
func RuleCheck(table, rowKey string, id ruleengine.Value, check ruleengine.Predicate) (string, []ruleengine.Value, error) {
	query := "SELECT 1 FROM " + Quote(table) + " WHERE " + rowKey + " = ? AND (" + check.SQL + ")"
	return ruleStatement(rowKey, query, append([]ruleengine.Value{id}, check.Args...))
}

func ruleAI(query string, args []ruleengine.Value, visitorAI string) (string, []ruleengine.Value) {
	if visitorAI != "" {
		query += ` AND "visitor_id" = ?`
		args = append(args, ruleengine.Text(visitorAI))
	}
	return query, args
}

func ruleStatement(rowKey, query string, args []ruleengine.Value) (string, []ruleengine.Value, error) {
	switch rowKey {
	case "rowid", "_rowid_", "oid", `"key"`:
	default:
		return "", nil, errors.New("invalid rules row key")
	}
	if len(query) > ruleStatementBytes || len(args) > ruleStatementArgs {
		return "", nil, ErrStatementTooLarge
	}
	return query, args, nil
}

// BindRuleArgs binds arguments starting at the one-based parameter index start.
// Predicate arguments are int, float or text. Row inputs also allow null/bool,
// with booleans stored as 0/1 just as BindStorageParam does.
func BindRuleArgs(stmt *sqlite3.Stmt, start int, args []ruleengine.Value) error {
	for i, value := range args {
		var err error
		switch value.Kind {
		case ruleengine.KindNull:
			err = stmt.BindNull(start + i)
		case ruleengine.KindBool:
			n := int64(0)
			if value.Bool {
				n = 1
			}
			err = stmt.BindInt64(start+i, n)
		case ruleengine.KindInt:
			err = stmt.BindInt64(start+i, value.Int)
		case ruleengine.KindFloat:
			err = stmt.BindFloat(start+i, value.Float)
		case ruleengine.KindText:
			err = stmt.BindText(start+i, value.Text)
		default:
			return fmt.Errorf("invalid rules argument kind %d", value.Kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RuleWorstCase checks complete visitor statements with maximal predicates.
// Columns are the prospective schema's visitor-settable columns. Checking all
// of them is conservative when some are server-owned for a particular caller.
func RuleWorstCase(table, rowKey string, columns []string, read, edit, del, checkAdd, checkEdit ruleengine.Predicate) error {
	_, err := ruleWorstStatements(table, rowKey, columns, read, edit, del, checkAdd, checkEdit)
	return err
}

type ruleBuiltStatement struct {
	query string
	args  []ruleengine.Value
}

// Keep the enumeration shared with the real-SQLite preparation test.
func ruleWorstStatements(table, rowKey string, columns []string, read, edit, del, checkAdd, checkEdit ruleengine.Predicate) ([]ruleBuiltStatement, error) {
	names := append([]string(nil), columns...)
	sort.SliceStable(names, func(i, j int) bool { return len(Quote(names[i])) > len(Quote(names[j])) })
	if len(names) > 100 {
		names = names[:100]
	}
	values := make([]ruleengine.Value, len(names))
	page := RulePage{Desc: true, Limit: 501, After: &RuleCursor{Value: ruleengine.Text(""), ID: ruleengine.Int(1)}}
	if len(names) > 0 && len(Quote(names[0])) > len(rowKey) {
		page.Order = names[0]
	}
	for _, name := range names[:min(3, len(names))] {
		page.Filters = append(page.Filters, RuleFilter{Column: name, Value: ruleengine.Text("")})
	}
	var statements []ruleBuiltStatement
	add := func(query string, args []ruleengine.Value, err error) error {
		if err == nil {
			statements = append(statements, ruleBuiltStatement{query, args})
		}
		return err
	}
	if len(names) > 0 {
		if err := add(RuleUpdate(table, rowKey, names, values, ruleengine.Int(1), read, edit, "visitor")); err != nil {
			return nil, err
		}
	}
	if err := add(RuleSelectPage(table, rowKey, page, read)); err != nil {
		return nil, err
	}
	if err := add(RuleSelectOne(table, rowKey, ruleengine.Int(1), read)); err != nil {
		return nil, err
	}
	if err := add(RuleDelete(table, rowKey, ruleengine.Int(1), read, del, "visitor")); err != nil {
		return nil, err
	}
	for _, check := range []ruleengine.Predicate{checkAdd, checkEdit} {
		if err := add(RuleCheck(table, rowKey, ruleengine.Int(1), check)); err != nil {
			return nil, err
		}
	}
	return statements, nil
}
