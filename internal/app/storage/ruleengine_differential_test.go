package storage

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func ruleTestConn(t *testing.T) *sqlite3.Conn {
	t.Helper()
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
	return conn
}

func ruleTestExec(t *testing.T, conn *sqlite3.Conn, query string) {
	t.Helper()
	if err := conn.Exec(query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func ruleTestStmt(t *testing.T, conn *sqlite3.Conn, query string, args []ruleengine.Value, buildErr error) *sqlite3.Stmt {
	t.Helper()
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	stmt, tail, err := conn.Prepare(query)
	if err != nil {
		t.Fatalf("prepare %s: %v", query, err)
	}
	if stmt == nil || strings.TrimSpace(tail) != "" {
		t.Fatalf("not one statement: %q", query)
	}
	if stmt.BindCount() != len(args) {
		t.Fatalf("%d parameters, %d args: %s", stmt.BindCount(), len(args), query)
	}
	if err := runtime.BindRuleArgs(stmt, 1, args); err != nil {
		stmt.Close()
		t.Fatal(err)
	}
	return stmt
}

func ruleTestValue(stmt *sqlite3.Stmt, i int) ruleengine.Value {
	switch stmt.ColumnType(i) {
	case sqlite3.NULL:
		return ruleengine.Null()
	case sqlite3.INTEGER:
		return ruleengine.Int(stmt.ColumnInt64(i))
	case sqlite3.FLOAT:
		return ruleengine.Float(stmt.ColumnFloat(i))
	case sqlite3.BLOB:
		return ruleengine.Value{Kind: ruleengine.KindBlob}
	default:
		return ruleengine.Text(stmt.ColumnText(i))
	}
}

func ruleTestPlan(t *testing.T, table ruleengine.Table, expr string, schema ruleengine.BindingSchema) *ruleengine.Plan {
	t.Helper()
	plan, errs := ruleengine.Compile(ruleengine.PolicySet{Policies: []ruleengine.Policy{
		{Name: "read", Command: ruleengine.CmdRead, Using: expr},
		{Name: "add", Command: ruleengine.CmdAdd, Check: expr},
		{Name: "edit", Command: ruleengine.CmdEdit, Using: "auth.signed_in && (" + expr + ")", Check: expr},
		{Name: "delete", Command: ruleengine.CmdDelete, Using: "auth.signed_in && (" + expr + ")"},
	}}, table, schema, ruleengine.DefaultLimits())
	if len(errs) != 0 {
		t.Fatalf("compile %q: %v", expr, errs)
	}
	return plan
}

// This corpus reads values back after SQLite affinity conversion. The evaluator
// must see the actual storage classes, not the types originally sent by a client.
func TestRuleEngineDifferential(t *testing.T) {
	const seed int64 = 0x52152b
	rng := rand.New(rand.NewSource(seed))
	atoms := []string{
		`c == "x"`, `c != "x"`, `"x" == c`, `c == auth.id`, `auth.id != c`,
		`c == 5`, `c != 5.0`, `c == auth.number`, `auth.number == c`,
		`c == true`, `c == false`, `c == auth.flag`,
		`c in ["x","X","x "]`, `c in [1,5.0,-2.5]`, `c in auth.teams`,
		`c contains "x"`, `c contains ""`, `c contains auth.id`,
		`lower(c) == "x"`, `lower(c) != auth.id`, `lower(c) in ["x","é"]`,
		`lower(c) in auth.teams`, `lower(c) contains auth.id`,
		`c is null`, `c is not null`, `row.mIxEdCaSe == "x"`,
		`auth.signed_in`, `auth.flag == true`, `auth.id == "x"`,
		`auth.id == auth.other`, `auth.number == 5`, `auth.number in [1,5.0]`,
		`auth.id in auth.teams`, `auth.id contains "x"`, `lower(auth.id) == "x"`,
		`auth.id is null`, `auth.number is not null`, `true`, `false`,
	}
	for _, n := range []string{"9007199254740991", "9007199254740992", "9007199254740993", "-9007199254740993", "9223372036854775807", "-9223372036854775808", "9223372036854775808.0", "-9223372036854775808.0", "9007199254740992.0", "-9007199254740992.0"} {
		atoms = append(atoms, "c == "+n, "c in ["+n+",0]")
	}
	expressions := append([]string(nil), atoms...)
	generated := 160
	if testing.Short() {
		generated = 16
	}
	for i := 0; i < generated; i++ {
		a, b, c := atoms[rng.Intn(len(atoms))], atoms[rng.Intn(len(atoms))], atoms[rng.Intn(len(atoms))]
		expressions = append(expressions, "("+a+" || "+b+") && !("+c+")")
	}
	inputs := []ruleengine.Value{
		ruleengine.Null(), ruleengine.Value{Kind: ruleengine.KindBlob},
		ruleengine.Int(0), ruleengine.Int(1), ruleengine.Int(5), ruleengine.Int(-1),
		ruleengine.Int(math.MinInt64), ruleengine.Int(math.MaxInt64),
		ruleengine.Int(1<<53 - 1), ruleengine.Int(1 << 53), ruleengine.Int(1<<53 + 1), ruleengine.Int(-1<<53 - 1),
		ruleengine.Float(0), ruleengine.Float(5), ruleengine.Float(-2.5), ruleengine.Float(1 << 53),
		ruleengine.Float(1 << 63), ruleengine.Float(-1 << 63), ruleengine.Float(math.Nextafter(1<<63, 0)),
		ruleengine.Float(math.Nextafter(-1<<63, math.Inf(-1))), ruleengine.Float(math.Inf(1)), ruleengine.Float(math.Inf(-1)),
		ruleengine.Text(""), ruleengine.Text("x"), ruleengine.Text("X"), ruleengine.Text("x "), ruleengine.Text("5"),
		ruleengine.Text("5.0"), ruleengine.Text("é"), ruleengine.Text("É"), ruleengine.Text("x\x00X"), ruleengine.Text("\xffX"),
	}
	extraRows := 24
	if testing.Short() {
		extraRows = 4
	}
	for i := 0; i < extraRows; i++ {
		switch rng.Intn(3) {
		case 0:
			inputs = append(inputs, ruleengine.Int(rng.Int63()))
		case 1:
			inputs = append(inputs, ruleengine.Float(rng.NormFloat64()*10))
		case 2:
			inputs = append(inputs, ruleengine.Text(fmt.Sprintf("x%X ", rng.Uint64())))
		}
	}
	schema := ruleengine.BindingSchema{
		"auth.signed_in": {Type: ruleengine.TypeBool}, "auth.flag": {Type: ruleengine.TypeBool},
		"auth.id": {Type: ruleengine.TypeText, Nullable: true}, "auth.other": {Type: ruleengine.TypeText, Nullable: true},
		"auth.number": {Type: ruleengine.TypeNumber, Nullable: true}, "auth.teams": {Type: ruleengine.TypeTextList},
	}
	bindings := []ruleengine.Values{
		{"auth.signed_in": ruleengine.Bool(true), "auth.flag": ruleengine.Bool(true), "auth.id": ruleengine.Text("x"), "auth.other": ruleengine.Text("X"), "auth.number": ruleengine.Int(5), "auth.teams": ruleengine.List("x", "X")},
		{"auth.signed_in": ruleengine.Bool(false), "auth.flag": ruleengine.Bool(false), "auth.id": ruleengine.Null(), "auth.other": ruleengine.Null(), "auth.number": ruleengine.Null(), "auth.teams": ruleengine.List()},
		{"auth.signed_in": ruleengine.Bool(true), "auth.flag": ruleengine.Bool(false), "auth.id": ruleengine.Text(""), "auth.other": ruleengine.Text(""), "auth.number": ruleengine.Float(1 << 63), "auth.teams": ruleengine.List("é", "x ")},
	}
	affinities := []string{"BLOB", "TEXT COLLATE NOCASE", "TEXT COLLATE RTRIM", "INTEGER", "REAL", "NUMERIC"}
	t.Logf("seed=%d expressions=%d (both polarities) rows=%d affinities=%d binding sets=%d", seed, len(expressions), len(inputs), len(affinities), len(bindings))
	comparisons := 0
	for _, affinity := range affinities {
		t.Run(affinity, func(t *testing.T) {
			conn := ruleTestConn(t)
			ruleTestExec(t, conn, `CREATE TABLE corpus(c `+affinity+`, MixedCase TEXT)`)
			insert := ruleTestStmt(t, conn, `INSERT INTO corpus(c,MixedCase) VALUES (?, 'x')`, []ruleengine.Value{ruleengine.Null()}, nil)
			for _, v := range inputs {
				var err error
				if v.Kind == ruleengine.KindBlob {
					err = insert.BindBlob(1, []byte("x"))
				} else {
					err = runtime.BindRuleArgs(insert, 1, []ruleengine.Value{v})
				}
				if err != nil {
					t.Fatal(err)
				}
				insert.Step()
				if err := insert.Err(); err != nil {
					t.Fatal(err)
				}
				if err := insert.Reset(); err != nil {
					t.Fatal(err)
				}
			}
			insert.Close()
			rows := []ruleengine.Row{}
			scan := ruleTestStmt(t, conn, `SELECT c,MixedCase FROM corpus ORDER BY rowid`, nil, nil)
			for scan.Step() {
				rows = append(rows, ruleengine.Row{"c": ruleTestValue(scan, 0), "MixedCase": ruleTestValue(scan, 1)})
			}
			if err := scan.Err(); err != nil {
				t.Fatal(err)
			}
			scan.Close()
			table := ruleengine.Table{Name: "corpus", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "c"}, {Name: "MixedCase"}}}
			for _, expr := range expressions {
				for _, expr := range []string{expr, "!(" + expr + ")"} {
					plan := ruleTestPlan(t, table, expr, schema)
					for _, values := range bindings {
						for _, clause := range []struct {
							op    ruleengine.Op
							check bool
						}{{ruleengine.OpRead, false}, {ruleengine.OpEdit, false}, {ruleengine.OpDelete, false}, {ruleengine.OpAdd, true}, {ruleengine.OpEdit, true}} {
							op, check := clause.op, clause.check
							var pred ruleengine.Predicate
							var err error

							if check {
								pred, err = plan.Check(op, values)
							} else {
								pred, err = plan.Using(op, values)
							}
							if err != nil {
								t.Fatal(err)
							}
							var query string
							var args []ruleengine.Value
							if check {
								query, args, err = runtime.RuleCheck("corpus", "rowid", ruleengine.Int(1), pred)
							} else {
								query, args, err = runtime.RuleSelectOne("corpus", "rowid", ruleengine.Int(1), pred)
							}
							stmt := ruleTestStmt(t, conn, query, args, err)
							for i, row := range rows {
								if err := stmt.BindInt64(1, int64(i+1)); err != nil {
									t.Fatal(err)
								}
								got := stmt.Step()
								if err := stmt.Err(); err != nil {
									t.Fatal(err)
								}
								var want bool
								if check {
									want, err = plan.EvalCheck(op, values, row)
								} else {
									want, err = plan.EvalUsing(op, values, row)
								}
								if err != nil {
									t.Fatal(err)
								}
								if got != want {
									t.Fatalf("expr=%s op=%d row=%+v values=%+v SQL=%s: sqlite=%v eval=%v", expr, op, row, values, pred.SQL, got, want)
								}
								comparisons++
								if err := stmt.Reset(); err != nil {
									t.Fatal(err)
								}
							}
							stmt.Close()
						}
					}
				}
			}
		})
	}
	t.Logf("seed=%d comparisons=%d", seed, comparisons)
}

// Independent expected answers pin the numbered semantics as well as comparing
// the two implementations, so agreement on a shared mistake cannot pass.
func TestRuleEngineSQLiteSemantics(t *testing.T) {
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE semantics(c COLLATE NOCASE, MixedCase TEXT)`)
	schema := ruleengine.BindingSchema{
		"auth.signed_in": {Type: ruleengine.TypeBool}, "auth.id": {Type: ruleengine.TypeText, Nullable: true},
		"auth.teams": {Type: ruleengine.TypeTextList},
	}
	values := ruleengine.Values{"auth.signed_in": ruleengine.Bool(true), "auth.id": ruleengine.Text("x"), "auth.teams": ruleengine.List()}
	cases := []struct {
		expr  string
		value ruleengine.Value
		want  bool
	}{
		{`!(c == "x" || c == "y")`, ruleengine.Text("z"), true},
		{`!(c == "x" && c == "y")`, ruleengine.Text("x"), true},
		{`c == "x"`, ruleengine.Null(), false}, {`!(c == "x")`, ruleengine.Null(), false},
		{`c == "x"`, ruleengine.Int(5), false}, {`!(c == "x")`, ruleengine.Int(5), false},
		{`c == "x"`, ruleengine.Value{Kind: ruleengine.KindBlob}, false},
		{`!(c == "x")`, ruleengine.Value{Kind: ruleengine.KindBlob}, false},
		{`c == "x"`, ruleengine.Text("X"), false}, {`c == "x"`, ruleengine.Text("x "), false},
		{`lower(c) == "x"`, ruleengine.Text("X"), true}, {`lower(c) == "é"`, ruleengine.Text("É"), false},
		{`c == 5`, ruleengine.Text("5"), false}, {`c == 5`, ruleengine.Float(5), true},
		{`c == 9007199254740992.0`, ruleengine.Int(1<<53 + 1), false},
		{`c == 9223372036854775808.0`, ruleengine.Int(math.MaxInt64), false},
		{`c == -9223372036854775808.0`, ruleengine.Int(math.MinInt64), true},
		{`c == true`, ruleengine.Int(1), true}, {`c == false`, ruleengine.Float(0), true},
		{`c contains ""`, ruleengine.Text(""), true}, {`c contains ""`, ruleengine.Null(), false},
		{`c contains "x"`, ruleengine.Text("pre\x00x"), true},
		{`lower(c) contains "x"`, ruleengine.Text("\xffX"), true},
		{`c in auth.teams`, ruleengine.Text("x"), false}, {`!(c in auth.teams)`, ruleengine.Text("x"), true},
		{`!(c in auth.teams)`, ruleengine.Int(1), false}, {`!(c in auth.teams)`, ruleengine.Null(), false},
		{`c is null || c == 5`, ruleengine.Null(), true}, {`c is not null && c == 5`, ruleengine.Null(), false},
		{`auth.signed_in && auth.id == "x"`, ruleengine.Null(), true},
	}
	for i, tc := range cases {
		ruleTestExec(t, conn, `DELETE FROM semantics`)
		ins := ruleTestStmt(t, conn, `INSERT INTO semantics(c) VALUES (?)`, []ruleengine.Value{ruleengine.Null()}, nil)
		var err error
		if tc.value.Kind == ruleengine.KindBlob {
			err = ins.BindBlob(1, []byte("x"))
		} else {
			err = runtime.BindRuleArgs(ins, 1, []ruleengine.Value{tc.value})
		}
		if err != nil {
			t.Fatal(err)
		}
		ins.Step()
		if err := ins.Err(); err != nil {
			t.Fatal(err)
		}
		ins.Close()
		plan := ruleTestPlan(t, ruleengine.Table{Name: "semantics", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "c"}}}, tc.expr, schema)
		pred, err := plan.Using(ruleengine.OpRead, values)
		if err != nil {
			t.Fatal(err)
		}
		query, args, err := runtime.RuleSelectOne("semantics", "rowid", ruleengine.Int(1), pred)
		stmt := ruleTestStmt(t, conn, query, args, err)
		got := stmt.Step()
		if err := stmt.Err(); err != nil {
			t.Fatal(err)
		}
		stmt.Close()
		want, err := plan.EvalUsing(ruleengine.OpRead, values, ruleengine.Row{"c": tc.value})
		if err != nil || got != tc.want || want != tc.want {
			t.Fatalf("case %d %s %+v: sqlite=%v evaluator=%v expected=%v err=%v", i, tc.expr, tc.value, got, want, tc.want, err)
		}
	}
}

func TestRuleEngineRowKeysAndIdentifiers(t *testing.T) {
	for _, tc := range []struct{ name, definition, key string }{
		{"no_id", `body TEXT`, "_rowid_"},
		{"shadow_rowid", `rowid TEXT, body TEXT`, "_rowid_"},
		{"shadow_first", `_RoWiD_ TEXT, body TEXT`, "rowid"},
		{"shadow_two", `_rowid_ TEXT, RoWiD TEXT, body TEXT`, "oid"},
		{"shadow_all", `_rowid_ TEXT, rowid TEXT, oid TEXT, body TEXT`, ""},
		{"without", `body TEXT PRIMARY KEY) WITHOUT ROWID --`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := ruleTestConn(t)
			ruleTestExec(t, conn, `CREATE TABLE items (`+tc.definition+`)`)
			cols, _, err := runtime.TableInfo(conn, "items")
			if err != nil {
				t.Fatal(err)
			}
			key, err := runtime.RowID(conn, "items", cols)
			if tc.key == "" {
				if err == nil {
					t.Fatal("expected no accessible rowid")
				}
				return
			}
			if err != nil || key != tc.key {
				t.Fatalf("key=%q err=%v", key, err)
			}
			ruleTestExec(t, conn, `INSERT INTO items(body) VALUES ('x')`)
			q, a, e := runtime.RuleSelectOne("items", key, ruleengine.Int(1), ruleengine.Predicate{SQL: "1"})
			stmt := ruleTestStmt(t, conn, q, a, e)
			defer stmt.Close()
			if !stmt.Step() || stmt.ColumnInt64(stmt.ColumnCount()-1) != 1 {
				t.Fatalf("resolved row key did not select row: %v", stmt.Err())
			}
		})
	}
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE "CaseTable" ("MiXeD" TEXT, "odd"";--name" TEXT, visitor_id TEXT)`)
	markers := []string{"quote'\"marker", "semicolon; DROP TABLE CaseTable;", "comment--marker", "control\x01marker"}
	for _, marker := range markers {
		ruleTestExec(t, conn, `DELETE FROM CaseTable`)
		q, a, e := runtime.RuleUpdate("CaseTable", "rowid", []string{`odd";--name`}, []ruleengine.Value{ruleengine.Text(marker)}, ruleengine.Int(1), ruleengine.Predicate{SQL: "1"}, ruleengine.Predicate{SQL: "1"}, "")
		if strings.Contains(q, marker) {
			t.Fatal("value interpolated")
		}
		stmt := ruleTestStmt(t, conn, q, a, e)
		stmt.Close()
		ins := ruleTestStmt(t, conn, `INSERT INTO CaseTable(MiXeD) VALUES (?)`, []ruleengine.Value{ruleengine.Text(marker)}, nil)
		ins.Step()
		if err := ins.Err(); err != nil {
			t.Fatal(err)
		}
		ins.Close()
		// Both a literal and a binding carrying injection text must stay in Args.
		literal, _ := json.Marshal(marker)
		for _, expr := range []string{"mixed == " + string(literal), `row.MIXED == auth.id`} {
			schema := ruleengine.BindingSchema{"auth.id": {Type: ruleengine.TypeText}, "auth.signed_in": {Type: ruleengine.TypeBool}}
			plan := ruleTestPlan(t, ruleengine.Table{Name: "CaseTable", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "MiXeD"}, {Name: `odd";--name`}}}, expr, schema)
			pred, err := plan.Using(ruleengine.OpRead, ruleengine.Values{"auth.id": ruleengine.Text(marker), "auth.signed_in": ruleengine.Bool(true)})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(pred.SQL, marker) || !strings.Contains(pred.SQL, `"MiXeD"`) {
				t.Fatalf("unsafe or wrong column spelling: %s", pred.SQL)
			}
			q, a, e := runtime.RuleSelectOne("casetable", "rowid", ruleengine.Int(1), pred)
			stmt := ruleTestStmt(t, conn, q, a, e)
			if !stmt.Step() {
				t.Fatalf("injection marker not matched: %v", stmt.Err())
			}
			stmt.Close()
		}
	}
	ruleTestExec(t, conn, `CREATE TABLE kv("key" TEXT PRIMARY KEY,value TEXT); INSERT INTO kv VALUES ('a','x')`)
	q, a, e := runtime.RuleSelectOne("kv", `"key"`, ruleengine.Text("a"), ruleengine.Predicate{SQL: "1"})
	stmt := ruleTestStmt(t, conn, q, a, e)
	defer stmt.Close()
	if !stmt.Step() {
		t.Fatalf("quoted key: %v", stmt.Err())
	}
}

func TestRuleEngineAuthorIndex(t *testing.T) {
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE posts(visitor_id TEXT, body TEXT); CREATE INDEX posts_author ON posts(visitor_id)`)
	schema := ruleengine.BindingSchema{"auth.id": {Type: ruleengine.TypeText}, "auth.signed_in": {Type: ruleengine.TypeBool}}
	plan := ruleTestPlan(t, ruleengine.Table{Name: "posts", RowKey: "rowid", Columns: []ruleengine.Column{{Name: "visitor_id"}, {Name: "body"}}}, `visitor_id == auth.id`, schema)
	read, err := plan.Using(ruleengine.OpRead, ruleengine.Values{"auth.id": ruleengine.Text("person"), "auth.signed_in": ruleengine.Bool(true)})
	if err != nil {
		t.Fatal(err)
	}
	q, a, e := runtime.RuleSelectPage("posts", "rowid", runtime.RulePage{Limit: 101}, read)
	stmt := ruleTestStmt(t, conn, "EXPLAIN QUERY PLAN "+q, a, e)
	defer stmt.Close()
	found := false
	for stmt.Step() {
		detail := stmt.ColumnText(3)
		t.Log(detail)
		found = found || strings.Contains(detail, "SEARCH posts USING INDEX posts_author")
	}
	if err := stmt.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("author rule did not use index")
	}
}

func TestRuleEnginePageOrdering(t *testing.T) {
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE page(c TEXT, a TEXT, b TEXT, visitor_id TEXT);
 INSERT INTO page VALUES (NULL,'filter','second','v'),(NULL,'filter','second','other'),('a','filter','second','v'),('a','filter','second','v'),('b','filter','second','other'),('c','filter','second','v'),('z','wrong','second','v')`)
	read := ruleengine.Predicate{SQL: `"visitor_id" = ?`, Args: []ruleengine.Value{ruleengine.Text("v")}}
	for _, tc := range []struct {
		name   string
		desc   bool
		cursor *runtime.RuleCursor
		want   []int64
	}{
		{name: "ascending", want: []int64{1, 3, 4, 6}},
		{name: "descending", desc: true, want: []int64{6, 4, 3, 1}},
		{name: "ascending_null", cursor: &runtime.RuleCursor{Value: ruleengine.Null(), ID: ruleengine.Int(1)}, want: []int64{3, 4, 6}},
		{name: "descending_null", desc: true, cursor: &runtime.RuleCursor{Value: ruleengine.Null(), ID: ruleengine.Int(2)}, want: []int64{1}},
		{name: "ascending_value", cursor: &runtime.RuleCursor{Value: ruleengine.Text("a"), ID: ruleengine.Int(3)}, want: []int64{4, 6}},
		{name: "descending_value", desc: true, cursor: &runtime.RuleCursor{Value: ruleengine.Text("a"), ID: ruleengine.Int(4)}, want: []int64{3, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := runtime.RulePage{Order: "c", Desc: tc.desc, After: tc.cursor, Limit: 101, Filters: []runtime.RuleFilter{{Column: "a", Value: ruleengine.Text("filter")}, {Column: "b", Value: ruleengine.Text("second")}}}
			q, a, e := runtime.RuleSelectPage("page", "rowid", options, read)
			stmt := ruleTestStmt(t, conn, q, a, e)
			defer stmt.Close()
			var got []int64
			for stmt.Step() {
				got = append(got, stmt.ColumnInt64(4))
			}
			if err := stmt.Err(); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
	// Paging by the row key and fetching one extra row cannot expose a hidden row.
	q, a, e := runtime.RuleSelectPage("page", "rowid", runtime.RulePage{After: &runtime.RuleCursor{Value: ruleengine.Int(1), ID: ruleengine.Int(1)}, Limit: 2}, read)
	stmt := ruleTestStmt(t, conn, q, a, e)
	defer stmt.Close()
	var got []int64
	for stmt.Step() {
		got = append(got, stmt.ColumnInt64(4))
	}
	if err := stmt.Err(); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != "[3 4]" {
		t.Fatalf("row-key page: %v", got)
	}
}

func TestRuleEngineMutationStatements(t *testing.T) {
	conn := ruleTestConn(t)
	ruleTestExec(t, conn, `CREATE TABLE posts(body TEXT UNIQUE ON CONFLICT REPLACE, visitor_id TEXT, n INTEGER DEFAULT 5);
 INSERT INTO posts(body,visitor_id) VALUES ('visible','v'),('hidden','v'),('other','other')`)
	read := ruleengine.Predicate{SQL: `"body" != ? AND "body" != ?`, Args: []ruleengine.Value{ruleengine.Text("hidden"), ruleengine.Text("now-hidden")}}
	change := ruleengine.Predicate{SQL: `"visitor_id" = ?`, Args: []ruleengine.Value{ruleengine.Text("v")}}
	yes := ruleengine.Predicate{SQL: "1"}
	run := func(query string, args []ruleengine.Value, err error) int {
		t.Helper()
		stmt := ruleTestStmt(t, conn, query, args, err)
		defer stmt.Close()
		stmt.Step()
		if err := stmt.Err(); err != nil {
			t.Fatal(err)
		}
		return int(conn.Changes())
	}
	// Read and edit/delete filters, including mandatory AI ownership, all apply.
	for _, id := range []int64{2, 3, 99} {
		q, a, e := runtime.RuleUpdate("posts", "rowid", []string{"body"}, []ruleengine.Value{ruleengine.Text("changed")}, ruleengine.Int(id), read, change, "")
		if run(q, a, e) != 0 {
			t.Fatalf("edited hidden/missing/other row %d", id)
		}
		q, a, e = runtime.RuleDelete("posts", "rowid", ruleengine.Int(id), read, change, "")
		if run(q, a, e) != 0 {
			t.Fatalf("deleted hidden/missing/other row %d", id)
		}
	}
	q, a, e := runtime.RuleUpdate("posts", "rowid", []string{"body"}, []ruleengine.Value{ruleengine.Text("changed")}, ruleengine.Int(3), yes, yes, "v")
	if run(q, a, e) != 0 {
		t.Fatal("AI edited another author's row")
	}
	q, a, e = runtime.RuleDelete("posts", "rowid", ruleengine.Int(3), yes, yes, "v")
	if run(q, a, e) != 0 {
		t.Fatal("AI deleted another author's row")
	}
	// OR ABORT must override the table's REPLACE constraint.
	q, a, e = runtime.RuleUpdate("posts", "rowid", []string{"body"}, []ruleengine.Value{ruleengine.Text("other")}, ruleengine.Int(1), read, change, "")
	stmt := ruleTestStmt(t, conn, q, a, e)
	stmt.Step()
	if err := stmt.Err(); err == nil {
		t.Fatal("UPDATE replaced a conflicting row")
	}
	stmt.Close()
	// Read visibility applies before the edit; its CHECK alone sees the new row.
	ruleTestExec(t, conn, "BEGIN")
	q, a, e = runtime.RuleUpdate("posts", "rowid", []string{"body", "n"}, []ruleengine.Value{ruleengine.Text("now-hidden"), ruleengine.Text("7")}, ruleengine.Int(1), read, change, "v")
	if run(q, a, e) != 1 {
		t.Fatal("allowed edit failed")
	}
	check := ruleengine.Predicate{SQL: `"body" = ? AND typeof("n") = 'integer' AND "n" = ?`, Args: []ruleengine.Value{ruleengine.Text("now-hidden"), ruleengine.Int(7)}}
	q, a, e = runtime.RuleCheck("posts", "rowid", ruleengine.Int(1), check)
	stmt = ruleTestStmt(t, conn, q, a, e)
	if !stmt.Step() {
		t.Fatalf("post-affinity check failed: %v", stmt.Err())
	}
	stmt.Close()
	q, a, e = runtime.RuleCheck("posts", "rowid", ruleengine.Int(1), ruleengine.Predicate{SQL: "0"})
	stmt = ruleTestStmt(t, conn, q, a, e)
	if stmt.Step() {
		t.Fatal("denied check passed")
	}
	if err := stmt.Err(); err != nil {
		t.Fatal(err)
	}
	stmt.Close()
	ruleTestExec(t, conn, "ROLLBACK")
	q, a, e = runtime.RuleSelectOne("posts", "rowid", ruleengine.Int(1), yes)
	stmt = ruleTestStmt(t, conn, q, a, e)
	if !stmt.Step() || stmt.ColumnText(0) != "visible" {
		t.Fatal("failed check rollback lost old row")
	}
	stmt.Close()
	// INSERT still uses PrepareInsert, followed by a check against stored defaults.
	ruleTestExec(t, conn, "BEGIN")
	query, params, err := runtime.PrepareInsert("posts", "rowid", map[string]string{"body": "TEXT"}, map[string]json.RawMessage{"body": json.RawMessage(`"added"`)}, true)
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := runtime.RunRowStatement(conn, query, params, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	q, a, e = runtime.RuleCheck("posts", "rowid", ruleengine.Int(id), ruleengine.Predicate{SQL: `typeof("n") = 'integer' AND "n" = ?`, Args: []ruleengine.Value{ruleengine.Int(5)}})
	stmt = ruleTestStmt(t, conn, q, a, e)
	if !stmt.Step() {
		t.Fatalf("insert default check: %v", stmt.Err())
	}
	stmt.Close()
	ruleTestExec(t, conn, "COMMIT")
	q, a, e = runtime.RuleDelete("posts", "rowid", ruleengine.Int(1), read, change, "v")
	if run(q, a, e) != 1 {
		t.Fatal("allowed delete failed")
	}
}
