package rules

import (
	"math"
	"strings"
	"testing"
)

func testSchema() BindingSchema {
	return BindingSchema{
		"auth.id": {Type: TypeText, Nullable: true}, "auth.signed_in": {Type: TypeBool},
		"auth.role": {Type: TypeText}, "auth.checked": {Type: TypeBool, AddCheckOnly: true},
		"auth.ai": {Type: TypeBool}, "auth.teams": {Type: TypeTextList},
		"auth.organiser": {Type: TypeBool}, "event.number": {Type: TypeNumber, Nullable: true},
	}
}
func expressionErrors(s string) []RuleError {
	return ValidateSyntax(PolicySet{Policies: []Policy{{Command: CmdRead, Using: s}}}, testSchema(), DefaultLimits())
}
func TestGrammarExamples(t *testing.T) {
	for _, s := range []string{
		`true`, `auth.signed_in`, `visitor_id == auth.id`,
		`visitor_id == auth.id || auth.role == "owner"`,
		`auth.role == "owner" || approved_at is not null`,
		`status in ["open", "review"] && auth.signed_in`, `lower(row.in) == "yes"`,
		`key contains "public/" && auth.signed_in`, `team in auth.teams || auth.organiser`,
		`!(status == "draft")`,
	} {
		t.Run(s, func(t *testing.T) {
			if errs := expressionErrors(s); len(errs) != 0 {
				t.Fatal(errs)
			}
		})
	}
	for _, tc := range []struct {
		expr, clause string
		pos          int
	}{
		{`visitor_id`, "type", 0}, {`price > 10`, "using", 6}, {`a == b`, "type", 2},
		{`visitor_id == null`, "type", 14}, {`status = "open"`, "using", 7},
		{`auth.nickname == "x"`, "binding", 0}, {`team.id == 1`, "binding", 0},
		{`tags in []`, "type", 8}, {`x in ["a", 1]`, "type", 11},
		{`lower(lower(name)) == "x"`, "type", 0}, {`(a == 1) == true`, "using", 9},
		{`auth.checked`, "binding", 0}, {`"a" == "a"`, "type", 4},
	} {
		t.Run(tc.expr, func(t *testing.T) {
			errs := expressionErrors(tc.expr)
			if len(errs) != 1 || errs[0].Clause != tc.clause || errs[0].Position != tc.pos {
				t.Fatalf("got %+v; want %s at %d", errs, tc.clause, tc.pos)
			}
		})
	}
}

func TestOperandPositions(t *testing.T) {
	for _, s := range []string{
		`false`, `!auth.signed_in`, `(auth.signed_in)`, `auth.signed_in == true`,
		`c == "x"`, `"x" != c`, `c == auth.id`, `auth.id == c`, `auth.id == "x"`,
		`auth.id == auth.role`, `event.number == 2`, `event.number == event.number`,
		`lower(c) == auth.id`, `auth.id == lower(c)`, `lower(auth.id) == "x"`,
		`c == true`, `true == c`,
		`c in [1, 2.5]`, `auth.id in ["x"]`, `event.number in [1]`, `lower(c) in auth.teams`,
		`c contains auth.id`, `auth.id contains "x"`, `lower(c) contains "x"`,
		`lower("X") contains "x"`, `lower("X") in ["x"]`,
		`c is null`, `auth.id is not null`, `event.number is null`,
		`row.true == false`, `row.row == auth.id`, `Null == 1`, `IN == 1`, `auth == "x"`,
	} {
		t.Run(s, func(t *testing.T) {
			if errs := expressionErrors(s); len(errs) != 0 {
				t.Fatal(errs)
			}
		})
	}
	for _, s := range []string{
		`"x"`, `5`, `auth.id`, `auth.teams`, `auth.id == 2`, `event.number == true`,
		`auth.id == event.number`, `c == auth.teams`, `null == c`,
		`true in [1]`, `c in [true]`, `c in [false]`, `c in [null]`, `auth.id in [1]`,
		`c in auth.id`, `c in column`, `c in "x"`, `c contains 1`, `1 contains c`,
		`c contains other`, `c contains event.number`, `event.number contains "x"`,
		`1 is null`, `auth.signed_in is null`, `auth.role is null`, `lower(c) is null`,
		`lower(1) == "x"`, `lower(auth.teams) == "x"`, `lower(c) == 2`,
		`lower(c) == lower(auth.id)`, `lower(auth.id) == lower(auth.role)`,
		`auth.role == lower("OWNER")`, `lower("OWNER") == auth.role`,
		`c == (auth.id)`, `c == 1 == true`, `c in ["x",]`,
	} {
		t.Run(s, func(t *testing.T) {
			if errs := expressionErrors(s); len(errs) == 0 {
				t.Fatal("accepted")
			}
		})
	}
}

func TestLexicalRules(t *testing.T) {
	for _, s := range []string{
		`c == 'it\'s'`, `c == "a\"b"`, `c == '\"\'\\\/\b\f\n\r\t\u0041\ud83d\ude00'`,
		`c == "é"`, "c == \"\x01\"", "\t c \n == \r 1 ", `c == -0`, `c == 1.5e+2`,
		`c == -9223372036854775808`, `c == 9223372036854775807`, `c == 9223372036854775808`,
		`c == 1e308`,
	} {
		t.Run(s, func(t *testing.T) {
			if errs := expressionErrors(s); len(errs) != 0 {
				t.Fatal(errs)
			}
		})
	}
	for _, s := range []string{
		`c == +1`, `c == 0x10`, `c == 1_000`, `c == 01`, `c == .1`, `c == 1.`, `c == 1e`,
		`c == 1e309`, `c == -1e309`, `c == --1`, `c == 1 // hello`, `é == 1`,
		`c == "\u0000"`, `c == "\ud800"`, `c == "\udc00"`, `c == "\ud800\u0041"`,
		`c == "\u12xz"`, `c == "\x41"`, "c == \"\xff\"", "c == \"a\nb\"", "c == \"\x00\"",
		`c == "unterminated`, `true | false`, `true & false`, `c < 1`, `c <= 1`, `c >= 1`,
		`row == 1`, `auth..id == 1`,
	} {
		t.Run(s, func(t *testing.T) {
			if errs := expressionErrors(s); len(errs) == 0 {
				t.Fatal("accepted")
			}
		})
	}
	for _, tc := range []struct {
		s string
		v Value
	}{
		{`-0`, Int(0)}, {`9223372036854775807`, Int(math.MaxInt64)},
		{`-9223372036854775808`, Int(math.MinInt64)}, {`9223372036854775808`, Float(9223372036854775808)},
		{`1e2`, Float(100)}, {`'\ud83d\ude00'`, Text("😀")},
	} {
		x, err := Parse("c == "+tc.s, DefaultLimits())
		if err != nil || x.right.value.Kind != tc.v.Kind || x.right.value.Int != tc.v.Int || x.right.value.Float != tc.v.Float || x.right.value.Text != tc.v.Text {
			t.Fatalf("%s: %v %v", tc.s, x, err)
		}
	}
	// Offsets are bytes, not runes.
	errs := expressionErrors(`c == "é" && nope.missing`)
	if len(errs) != 1 || errs[0].Position != 13 {
		t.Fatalf("byte offset: %+v", errs)
	}
}

func TestParserLimits(t *testing.T) {
	def := DefaultLimits()
	if def != (Limits{2048, 256, 32, 64, 32, 64, 8192, 256}) {
		t.Fatal(def)
	}
	tests := []struct {
		name, at, over string
		lim            Limits
	}{
		{"bytes", "true" + strings.Repeat(" ", 2044), "true" + strings.Repeat(" ", 2045), def},
		{"depth", strings.Repeat("(", 31) + "true" + strings.Repeat(")", 31), strings.Repeat("(", 32) + "true" + strings.Repeat(")", 32), def},
		{"not depth", strings.Repeat("!", 31) + "true", strings.Repeat("!", 32) + "true", def},
		{"list", `x in [` + strings.Repeat(`"a",`, 63) + `"a"]`, `x in [` + strings.Repeat(`"a",`, 64) + `"a"]`, def},
		{"identifier", strings.Repeat("a", 64) + " == 1", strings.Repeat("a", 65) + " == 1", def},
	}
	// A list has one node plus its items; each comparison adds two, OR adds one.
	// Four comparisons with 60,60,60,61 items and three ORs total 256 nodes.
	expr := func(last int) string {
		var terms []string
		for _, n := range []int{60, 60, 60, last} {
			terms = append(terms, "x in ["+strings.Repeat("1,", n-1)+"1]")
		}
		return strings.Join(terms, " || ")
	}
	tests = append(tests, struct {
		name, at, over string
		lim            Limits
	}{"nodes", expr(61), expr(62), def})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.at, tc.lim); err != nil {
				t.Fatalf("boundary: %v", err)
			}
			if _, err := Parse(tc.over, tc.lim); err == nil || err.Clause != "size" {
				t.Fatalf("over: %v", err)
			}
		})
	}
	if _, err := Parse(strings.Repeat("(", 288)+"true"+strings.Repeat(")", 288), def); err == nil || err.Clause != "size" {
		t.Fatalf("288 deep: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{`true`, `visitor_id == auth.id`, `x in ["a",1]`, strings.Repeat("(", 288) + "true" + strings.Repeat(")", 288), `c == '\ud800'`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		lim := DefaultLimits()
		x, err := Parse(s, lim)
		if err != nil {
			if x != nil {
				t.Fatal("tree on error")
			}
			return
		}
		var count int
		var walk func(*Expr, int)
		walk = func(n *Expr, depth int) {
			if n == nil {
				return
			}
			count++
			if depth > lim.ExprDepth {
				t.Fatal("depth exceeded")
			}
			walk(n.left, depth+1)
			walk(n.right, depth+1)
			for _, item := range n.items {
				walk(item, depth+1)
			}
		}
		walk(x, 1)
		if count > lim.ExprNodes {
			t.Fatal("node count exceeded")
		}
		expressionErrors(s)
	})
}
