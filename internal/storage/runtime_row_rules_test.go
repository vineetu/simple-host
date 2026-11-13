package storage

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func TestRuleWorstCasePrepares(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
	if got := conn.Limit(sqlite3.LIMIT_VARIABLE_NUMBER, -1); got != ruleStatementArgs {
		t.Fatalf("builder parameter limit=%d SQLite=%d", ruleStatementArgs, got)
	}
	// A legal 100-column schema, including the author column for the AI guard.
	columns := []string{"visitor_id"}
	definitions := []string{`visitor_id TEXT`}
	for i := 1; i < 100; i++ {
		name := fmt.Sprintf("c%063d", i)
		columns = append(columns, name)
		definitions = append(definitions, Quote(name)+" TEXT")
	}
	if err := conn.Exec(`CREATE TABLE widest (` + strings.Join(definitions, ",") + `)`); err != nil {
		t.Fatal(err)
	}
	// Compile the maximal 256 arguments (four 64-item list bindings), then pad
	// with harmless SQL whitespace to exercise exactly the 8192-byte envelope.
	lim := ruleengine.DefaultLimits()
	schema := ruleengine.BindingSchema{"auth.teams": {Type: ruleengine.TypeTextList}}
	clause := strings.Join([]string{columns[1] + " in auth.teams", columns[2] + " in auth.teams", columns[3] + " in auth.teams", columns[4] + " in auth.teams"}, " || ")
	table := ruleengine.Table{Name: "widest", RowKey: "_rowid_"}
	for _, name := range columns {
		table.Columns = append(table.Columns, ruleengine.Column{Name: name})
	}
	plan, errs := ruleengine.Compile(ruleengine.PolicySet{Policies: []ruleengine.Policy{{Name: "read", Command: ruleengine.CmdRead, Using: clause}}}, table, schema, lim)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	items := make([]string, lim.ListItems)
	pred, err := plan.Using(ruleengine.OpRead, ruleengine.Values{"auth.teams": ruleengine.List(items...)})
	if err != nil {
		t.Fatal(err)
	}
	if len(pred.Args) != lim.PredicateArgs {
		t.Fatalf("not maximal args: %d", len(pred.Args))
	}
	pred.SQL += strings.Repeat(" ", lim.PredicateBytes-len(pred.SQL))
	if err := RuleWorstCase("widest", "_rowid_", columns, pred, pred, pred, pred, pred); err != nil {
		t.Fatal(err)
	}
	statements, err := ruleWorstStatements("widest", "_rowid_", columns, pred, pred, pred, pred, pred)
	if err != nil {
		t.Fatal(err)
	}
	if len(statements) != 6 {
		t.Fatalf("missing a statement shape: %d", len(statements))
	}
	for i, built := range statements {
		if len(built.query) > 32768 || len(built.args) > 32766 {
			t.Fatal("oversized statement returned")
		}
		stmt, tail, err := conn.Prepare(built.query)
		if err != nil {
			t.Fatalf("statement %d: %v", i, err)
		}
		if tail != "" || stmt.BindCount() != len(built.args) {
			t.Fatalf("statement %d bind count or tail", i)
		}
		if err := BindRuleArgs(stmt, 1, built.args); err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		t.Logf("statement %d: %d bytes, %d arguments; prepares", i, len(built.query), len(built.args))
	}
	// Also reproduce the corrected brief's all-64-character, 100-column update
	// without an AI guard (that schema has no visitor_id). It fits, as ruled.
	columns[0] = strings.Repeat("z", 64)
	if err := conn.Exec(`ALTER TABLE widest RENAME COLUMN visitor_id TO ` + Quote(columns[0])); err != nil {
		t.Fatal(err)
	}
	q, a, err := RuleUpdate("widest", "_rowid_", columns, make([]ruleengine.Value, 100), ruleengine.Int(1), pred, pred, "")
	if err != nil {
		t.Fatal(err)
	}
	stmt, _, err := conn.Prepare(q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if stmt.BindCount() != len(a) {
		t.Fatal("argument count")
	}
	t.Logf("100 64-character names: %d bytes, %d arguments; prepares", len(q), len(a))
}

func TestRuleStatementLimits(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE items(c TEXT,visitor_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	prepares := 0
	if err := conn.SetAuthorizer(func(sqlite3.AuthorizerActionCode, string, string, string, string) sqlite3.AuthorizerReturnCode {
		prepares++
		return sqlite3.AUTH_OK
	}); err != nil {
		t.Fatal(err)
	}
	pred := ruleengine.Predicate{SQL: `"c" = ?`, Args: []ruleengine.Value{ruleengine.Text("x")}}
	id := ruleengine.Int(1)
	builders := map[string]func() (string, []ruleengine.Value, error){
		"one": func() (string, []ruleengine.Value, error) { return RuleSelectOne("items", "rowid", id, pred) },
		"page": func() (string, []ruleengine.Value, error) {
			return RuleSelectPage("items", "rowid", RulePage{Filters: []RuleFilter{{"c", ruleengine.Text("x")}}, Order: "c", Desc: true, After: &RuleCursor{Value: ruleengine.Text("x"), ID: id}, Limit: 501}, pred)
		},
		"update": func() (string, []ruleengine.Value, error) {
			return RuleUpdate("items", "rowid", []string{"c"}, []ruleengine.Value{ruleengine.Text("y")}, id, pred, pred, "visitor")
		},
		"delete": func() (string, []ruleengine.Value, error) {
			return RuleDelete("items", "rowid", id, pred, pred, "visitor")
		},
		"check": func() (string, []ruleengine.Value, error) { return RuleCheck("items", "rowid", id, pred) },
	}
	bytes, args := ruleStatementBytes, ruleStatementArgs
	t.Cleanup(func() { ruleStatementBytes, ruleStatementArgs = bytes, args })
	for name, build := range builders {
		t.Run(name, func(t *testing.T) {
			q, a, err := build()
			if err != nil {
				t.Fatal(err)
			}
			for _, bound := range []string{"bytes", "args"} {
				t.Run(bound, func(t *testing.T) {
					ruleStatementBytes, ruleStatementArgs = len(q), len(a)
					t.Cleanup(func() { ruleStatementBytes, ruleStatementArgs = bytes, args })
					if _, _, err := build(); err != nil {
						t.Fatalf("exact boundary: %v", err)
					}
					if bound == "bytes" {
						ruleStatementBytes--
					} else {
						ruleStatementArgs--
					}
					query, params, err := build()
					if !errors.Is(err, ErrStatementTooLarge) || query != "" || params != nil {
						t.Fatalf("oversize returned %q %v %v", query, params, err)
					}
					// The caller never prepares anything after a builder refusal.
					if err == nil {
						s, _, _ := conn.Prepare(query)
						if s != nil {
							s.Close()
						}
					}
					if prepares != 0 {
						t.Fatal("refused statement reached SQLite")
					}
					if err := RuleWorstCase("items", "rowid", []string{"c", "visitor_id"}, pred, pred, pred, pred, pred); !errors.Is(err, ErrStatementTooLarge) {
						t.Fatalf("worst case: %v", err)
					}
				})
			}
		})
	}
}

func TestRuleBuilderRowKey(t *testing.T) {
	for _, key := range []string{"id", `rowid; DELETE FROM items`, `"rowid"`, "ROWID", ""} {
		q, a, err := RuleSelectOne("items", key, ruleengine.Int(1), ruleengine.Predicate{SQL: "1"})
		if err == nil || q != "" || a != nil {
			t.Fatalf("accepted row key %q", key)
		}
	}
}

func TestRuleWorstPageCoversCursorShapes(t *testing.T) {
	pred := ruleengine.Predicate{SQL: "1"}
	for _, columns := range [][]string{{"c"}, {"a", "bb", "ccc"}, {`has"quotes`, "short", "other"}} {
		statements, err := ruleWorstStatements("items", "_rowid_", columns, pred, pred, pred, pred, pred)
		if err != nil {
			t.Fatal(err)
		}
		worst := statements[1] // update, then page
		for _, order := range append(append([]string(nil), columns...), "_rowid_") {
			for _, desc := range []bool{false, true} {
				for _, value := range []ruleengine.Value{ruleengine.Null(), ruleengine.Int(1)} {
					page := RulePage{Order: order, Desc: desc, Limit: 501, After: &RuleCursor{Value: value, ID: ruleengine.Int(1)}}
					for _, col := range columns {
						page.Filters = append(page.Filters, RuleFilter{Column: col, Value: ruleengine.Text("")})
					}
					q, a, err := RuleSelectPage("items", "_rowid_", page, pred)
					if err != nil {
						t.Fatal(err)
					}
					if len(q) > len(worst.query) || len(a) > len(worst.args) {
						t.Fatalf("worst page %d/%d does not cover %d/%d: %s", len(worst.query), len(worst.args), len(q), len(a), q)
					}
				}
			}
		}
	}
}

func TestBindRuleArgs(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	stmt, _, err := conn.Prepare(`SELECT ?, ?, ?, ?, ?, ?, ?`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if err := stmt.BindText(1, "prefix"); err != nil {
		t.Fatal(err)
	}
	args := []ruleengine.Value{ruleengine.Null(), ruleengine.Bool(false), ruleengine.Bool(true), ruleengine.Int(-9223372036854775808), ruleengine.Float(1.5), ruleengine.Text("a'\";--\x01")}
	if err := BindRuleArgs(stmt, 2, args); err != nil {
		t.Fatal(err)
	}
	if !stmt.Step() {
		t.Fatal(stmt.Err())
	}
	if stmt.ColumnText(0) != "prefix" || stmt.ColumnType(1) != sqlite3.NULL || stmt.ColumnInt64(2) != 0 || stmt.ColumnInt64(3) != 1 || stmt.ColumnInt64(4) != -9223372036854775808 || stmt.ColumnFloat(5) != 1.5 || stmt.ColumnText(6) != args[5].Text {
		t.Fatal("bound values changed")
	}
	if err := stmt.Reset(); err != nil {
		t.Fatal(err)
	}
	if err := BindRuleArgs(stmt, 8, []ruleengine.Value{ruleengine.Int(1)}); err == nil {
		t.Fatal("binding beyond statement succeeded")
	}
	for _, value := range []ruleengine.Value{ruleengine.List("x"), {Kind: ruleengine.KindBlob}} {
		if err := BindRuleArgs(stmt, 1, []ruleengine.Value{value}); err == nil {
			t.Fatalf("accepted non-scalar argument: %+v", value)
		}
	}
}
