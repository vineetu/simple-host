package rules

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func ruleTable() Table {
	return Table{Name: "records", RowKey: "rowid", Columns: []Column{{"c"}, {"visitor_id"}, {"approved_at"}, {"Status"}, {"in"}, {"rowid"}, {"auth"}}}
}
func compilePolicies(t *testing.T, policies ...Policy) *Plan {
	t.Helper()
	if policies == nil {
		policies = []Policy{}
	}
	p, errs := Compile(PolicySet{Policies: policies}, ruleTable(), testSchema(), DefaultLimits())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return p
}
func readPlan(t *testing.T, expr string) *Plan {
	t.Helper()
	return compilePolicies(t, Policy{Command: CmdRead, Using: expr})
}
func readPredicate(t *testing.T, expr string, v Values) Predicate {
	t.Helper()
	p, err := readPlan(t, expr).Using(OpRead, v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Every compile-table row, including both polarities, has a literal expected
// predicate. These expectations do not derive from the emitter or evaluator.
func TestCompileTable(t *testing.T) {
	for _, tc := range []struct {
		expr, positive, negative string
		args                     []Value
		values                   Values
	}{
		{`c == "x"`, `(typeof("c") = 'text' AND "c" = ? COLLATE BINARY)`, `(typeof("c") = 'text' AND "c" != ? COLLATE BINARY)`, []Value{Text("x")}, nil},
		{`c != "x"`, `(typeof("c") = 'text' AND "c" != ? COLLATE BINARY)`, `(typeof("c") = 'text' AND "c" = ? COLLATE BINARY)`, []Value{Text("x")}, nil},
		{`auth.id == c`, `(typeof("c") = 'text' AND "c" = ? COLLATE BINARY)`, `(typeof("c") = 'text' AND "c" != ? COLLATE BINARY)`, []Value{Text("person")}, Values{"auth.id": Text("person")}},
		{`c == auth.id`, `0`, `0`, nil, Values{"auth.id": Null()}},
		{`c == 5`, `(typeof("c") IN ('integer','real') AND "c" = ?)`, `(typeof("c") IN ('integer','real') AND "c" != ?)`, []Value{Int(5)}, nil},
		{`c != 5`, `(typeof("c") IN ('integer','real') AND "c" != ?)`, `(typeof("c") IN ('integer','real') AND "c" = ?)`, []Value{Int(5)}, nil},
		{`true == c`, `(typeof("c") IN ('integer','real') AND "c" = ?)`, `(typeof("c") IN ('integer','real') AND "c" != ?)`, []Value{Int(1)}, nil},
		{`c == auth.signed_in`, `(typeof("c") IN ('integer','real') AND "c" = ?)`, `(typeof("c") IN ('integer','real') AND "c" != ?)`, []Value{Int(0)}, Values{"auth.signed_in": Bool(false)}},
		{`c == event.number`, `(typeof("c") IN ('integer','real') AND "c" = ?)`, `(typeof("c") IN ('integer','real') AND "c" != ?)`, []Value{Float(5.5)}, Values{"event.number": Float(5.5)}},
		{`c in ["a","b"]`, `(typeof("c") = 'text' AND "c" COLLATE BINARY IN (?,?))`, `(typeof("c") = 'text' AND "c" COLLATE BINARY NOT IN (?,?))`, []Value{Text("a"), Text("b")}, nil},
		{`c in auth.teams`, `(typeof("c") = 'text' AND "c" COLLATE BINARY IN (?,?))`, `(typeof("c") = 'text' AND "c" COLLATE BINARY NOT IN (?,?))`, []Value{Text("a"), Text("b")}, Values{"auth.teams": List("a", "b")}},
		{`c in auth.teams`, `0`, `(typeof("c") = 'text')`, nil, Values{"auth.teams": List()}},
		{`c in [1,2.5]`, `(typeof("c") IN ('integer','real') AND "c" IN (?,?))`, `(typeof("c") IN ('integer','real') AND "c" NOT IN (?,?))`, []Value{Int(1), Float(2.5)}, nil},
		{`c contains "x"`, `(typeof("c") = 'text' AND instr("c", ?) > 0)`, `(typeof("c") = 'text' AND instr("c", ?) = 0)`, []Value{Text("x")}, nil},
		{`c contains auth.id`, `0`, `0`, nil, Values{"auth.id": Null()}},
		{`lower(c) == "x"`, `(typeof("c") = 'text' AND lower("c") = ? COLLATE BINARY)`, `(typeof("c") = 'text' AND lower("c") != ? COLLATE BINARY)`, []Value{Text("x")}, nil},
		{`c is null`, `"c" IS NULL`, `"c" IS NOT NULL`, nil, nil},
		{`c is not null`, `"c" IS NOT NULL`, `"c" IS NULL`, nil, nil},
		{`auth.id == "x"`, `1`, `0`, nil, Values{"auth.id": Text("x")}},
		{`auth.id == "x"`, `0`, `0`, nil, Values{"auth.id": Null()}},
		{`auth.signed_in`, `1`, `0`, nil, Values{"auth.signed_in": Bool(true)}},
	} {
		for _, neg := range []bool{false, true} {
			expr, want := tc.expr, tc.positive
			if neg {
				expr, want = "!("+expr+")", tc.negative
			}
			t.Run(expr+fmt.Sprint(tc.values), func(t *testing.T) {
				got := readPredicate(t, expr, tc.values)
				if got.SQL != want || !reflect.DeepEqual(got.Args, tc.args) {
					t.Fatalf("got %+v, want %s / %+v", got, want, tc.args)
				}
			})
		}
	}
}
func TestCompileResolutionAndRowKey(t *testing.T) {
	for _, key := range []string{"rowid", "_rowid_", "oid", `"key"`} {
		table := ruleTable()
		table.RowKey = key
		p, errs := Compile(PolicySet{Policies: []Policy{{Command: CmdRead, Using: `row.sTaTuS == "open"`}}}, table, testSchema(), DefaultLimits())
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		pred, err := p.Using(OpRead, nil)
		if err != nil || !strings.Contains(pred.SQL, `"Status"`) || !p.ReferencesColumn("STATUS") {
			t.Fatal(pred, err)
		}
	}
	for _, key := range []string{"", "id", "ROWID", "rowid; DROP TABLE t", `"rowid"`, "key", "rowid --"} {
		table := ruleTable()
		table.RowKey = key
		p, errs := Compile(PolicySet{Policies: []Policy{}}, table, nil, DefaultLimits())
		if p != nil || len(errs) == 0 || errs[0].Clause != "shape" {
			t.Fatal(key, p, errs)
		}
	}
	for _, expr := range []string{`missing == 1`, `lower(missing) == "x"`, `row.MISSING in ["x"]`} {
		p, errs := Compile(PolicySet{Policies: []Policy{{Name: "readers", Command: CmdRead, Using: expr}}}, ruleTable(), nil, DefaultLimits())
		if p != nil || len(errs) != 1 || errs[0].Clause != "column" || errs[0].Policy != "readers" || errs[0].Table != "records" || errs[0].Position < 0 {
			t.Fatal(expr, p, errs)
		}
	}
	// SQLite folds ASCII identifiers only; Unicode lookalikes cannot resolve.
	table := Table{Name: "t", RowKey: "rowid", Columns: []Column{{"ſtatus"}}}
	if _, errs := Compile(PolicySet{Policies: []Policy{{Command: CmdRead, Using: `status == "x"`}}}, table, nil, DefaultLimits()); len(errs) == 0 {
		t.Fatal("Unicode case folding resolved an ASCII identifier")
	}
	for _, expr := range []string{`c == c`, `c == null`, `auth.id == 1`, `missing`} {
		if _, errs := Compile(PolicySet{Policies: []Policy{{Command: CmdRead, Using: expr}}}, ruleTable(), testSchema(), DefaultLimits()); len(errs) == 0 || errs[0].Clause != "type" {
			t.Fatal(expr, errs)
		}
	}
}
func TestPredicateBounds(t *testing.T) {
	set := PolicySet{Policies: []Policy{{Command: CmdRead, Using: `c == "x"`}}}
	size := len(readPredicate(t, `c == "x"`, nil).SQL)
	for _, n := range []int{size - 1, size} {
		lim := DefaultLimits()
		lim.PredicateBytes = n
		p, errs := Compile(set, ruleTable(), testSchema(), lim)
		if n == size {
			if p == nil || len(errs) > 0 {
				t.Fatal(errs)
			}
		} else if p != nil || len(errs) == 0 || errs[0].Clause != "composed" {
			t.Fatal(errs)
		}
	}
	for _, n := range []int{255, 256, 257} {
		// Four maximal list bindings are 256 args, a fifth scalar pushes it over.
		expr := `c in auth.teams && c in auth.teams && c in auth.teams && c in auth.teams`
		if n == 257 {
			expr += ` && c == "x"`
		}
		lim := DefaultLimits()
		if n == 255 {
			lim.PredicateArgs = 255
		}
		p, errs := Compile(PolicySet{Policies: []Policy{{Command: CmdRead, Using: expr}}}, ruleTable(), testSchema(), lim)
		if n == 256 {
			if p == nil || len(errs) > 0 {
				t.Fatal(errs)
			}
		} else if p != nil || len(errs) == 0 || errs[0].Clause != "composed" {
			t.Fatal(errs)
		}
	}
	// Each policy is legal alone; the composed OR exceeds the default SQL cap.
	policies := make([]Policy, 32)
	expr := strings.TrimSuffix(strings.Repeat(`c == "x" && `, 6), " && ")
	for i := range policies {
		policies[i] = Policy{Command: CmdRead, Using: expr}
	}
	if p, errs := Compile(PolicySet{Policies: policies}, ruleTable(), testSchema(), DefaultLimits()); p != nil || len(errs) == 0 || errs[0].Clause != "composed" {
		t.Fatal(errs)
	}
	// The same bounds cover checks, including inherited all/edit checks.
	for _, cmd := range []Command{CmdAdd, CmdEdit, CmdAll} {
		pol := Policy{Command: cmd, Check: `c in auth.teams`}
		if cmd != CmdAdd {
			pol.Using = "auth.signed_in"
		}
		lim := DefaultLimits()
		lim.PredicateArgs = 63
		if _, errs := Compile(PolicySet{Policies: []Policy{pol}}, ruleTable(), testSchema(), lim); len(errs) == 0 || errs[0].Clause != "composed" {
			t.Fatal(cmd, errs)
		}
	}
}
func TestPredicateMarkersStayBound(t *testing.T) {
	const literal = "marker_z7H2J9t3aQ8c1Y6p0V5b4N2m7R8w6F9s"
	const binding = "marker_c8G3K7n1X5u0P4d9A6h2W8e7L5q4T3r"
	for _, expr := range []string{
		fmt.Sprintf(`c == %q || c == auth.id`, literal),
		fmt.Sprintf(`c != %q && c != auth.id`, literal),
		fmt.Sprintf(`c in [%q] || c in auth.teams`, literal),
		fmt.Sprintf(`!(c in [%q]) && !(c in auth.teams)`, literal),
		fmt.Sprintf(`c contains %q || c contains auth.id`, literal),
		fmt.Sprintf(`!(c contains %q) && !(c contains auth.id)`, literal),
		fmt.Sprintf(`lower(c) == %q || lower(c) == auth.id`, literal),
	} {
		pred := readPredicate(t, expr, Values{"auth.id": Text(binding), "auth.teams": List(binding)})
		for _, marker := range []string{literal, binding} {
			if strings.Contains(pred.SQL, marker) {
				t.Fatal("interpolated marker", pred.SQL)
			}
			found := false
			for _, arg := range pred.Args {
				found = found || reflect.DeepEqual(arg, Text(marker))
			}
			if !found {
				t.Fatal("marker missing from args", expr, marker, pred)
			}
		}
	}
}
func TestPlanImmutability(t *testing.T) {
	table, schema := ruleTable(), testSchema()
	set := PolicySet{Policies: []Policy{{Command: CmdRead, Using: `c in auth.teams`}}}
	p, errs := Compile(set, table, schema, DefaultLimits())
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	table.Columns[0].Name = "changed"
	schema["auth.teams"] = Binding{Type: TypeBool}
	set.Policies[0].Using = "false"
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := Values{"auth.teams": List("a")}
			pred, err := p.Using(OpRead, v)
			if err != nil {
				t.Error(err)
				return
			}
			pred.Args[0] = Text("changed")
			needs := p.Needs(OpRead)
			needs[0] = "changed"
			got, err := p.EvalUsing(OpRead, v, Row{"c": Text("a")})
			if err != nil || !got {
				t.Error(got, err)
			}
		}()
	}
	wg.Wait()
}
