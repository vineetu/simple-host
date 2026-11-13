package rules

import (
	"fmt"
	"math"
	"testing"
)

func TestSemantics(t *testing.T) {
	blob := Value{Kind: KindBlob}
	// Each numbered semantics line in 3.2 has explicit positive and rewritten
	// negative answers. NULL/wrong-class cases deliberately have two false answers.
	for _, tc := range []struct {
		name, expr string
		stored     Value
		values     Values
		yes, no    bool
	}{
		{"1 de Morgan AND", `c == "x" && auth.signed_in`, Text("y"), Values{"auth.signed_in": Bool(true)}, false, true},
		{"1 de Morgan OR", `c == "x" || auth.signed_in`, Null(), Values{"auth.signed_in": Bool(false)}, false, false},
		{"1 double negation", `!!(c == "x")`, Text("x"), nil, true, false},
		{"2 null equality", `c == "x"`, Null(), nil, false, false},
		{"2 null inequality", `c != "x"`, Null(), nil, false, false},
		{"2 null membership", `c in ["x"]`, Null(), nil, false, false},
		{"2 null contains", `c contains "x"`, Null(), nil, false, false},
		{"2 null test", `c is null`, Null(), nil, true, false},
		{"2 not null test", `c is not null`, Text("x"), nil, true, false},
		{"3 blob text equality", `c == "x"`, blob, nil, false, false},
		{"3 blob number equality", `c == 1`, blob, nil, false, false},
		{"3 blob membership", `c in ["x"]`, blob, nil, false, false},
		{"3 blob contains", `c contains "x"`, blob, nil, false, false},
		{"3 blob not null", `c is null`, blob, nil, false, true},
		{"4 binary", `c == "X"`, Text("x"), nil, false, true},
		{"4 no trim", `c == "x"`, Text("x "), nil, false, true},
		{"4 ascii lower", `lower(c) == "xÉ"`, Text("XÉ"), nil, true, false},
		{"4 no unicode lower", `lower(c) == "é"`, Text("É"), nil, false, true},
		{"4 lower wrong class", `lower(c) == "5"`, Int(5), nil, false, false},
		{"5 number on stored text", `c == 5`, Text("5"), nil, false, false},
		{"5 text on stored number", `c == "5"`, Int(5), nil, false, false},
		{"5 real integer", `c == 5`, Float(5), nil, true, false},
		{"5 integer real", `c == 5.0`, Int(5), nil, true, false},
		{"5 real", `c == 1.25`, Float(1.25), nil, true, false},
		{"5 fractional", `c == -1.5`, Int(-1), nil, false, true},
		{"6 true", `c == true`, Int(1), nil, true, false},
		{"6 false", `c == false`, Float(0), nil, true, false},
		{"6 other number", `c == true`, Int(2), nil, false, true},
		{"6 text boolean", `c == true`, Text("1"), nil, false, false},
		{"7 contains", `c contains "bc"`, Text("abcd"), nil, true, false},
		{"7 missing substring", `c contains "bc"`, Text("abCd"), nil, false, true},
		{"7 empty needle", `c contains ""`, Text(""), nil, true, false},
		{"7 null needle", `c contains auth.id`, Text("x"), Values{"auth.id": Null()}, false, false},
		{"8 text list", `c in ["a","b"]`, Text("b"), nil, true, false},
		{"8 numeric list", `c in [1,2.5]`, Float(1), nil, true, false},
		{"8 mixed numeric list", `c in [1,2.5]`, Int(2), nil, false, true},
		{"8 wrong list class", `c in [1,2]`, Text("1"), nil, false, false},
		{"8 empty list", `c in auth.teams`, Text("x"), Values{"auth.teams": List()}, false, true},
		{"8 empty list null", `c in auth.teams`, Null(), Values{"auth.teams": List()}, false, false},
		{"8 empty list number", `c in auth.teams`, Int(1), Values{"auth.teams": List()}, false, false},
		{"8 lower in", `lower(c) in ["x"]`, Text("X"), nil, true, false},
		{"9 AND", `c == "x" && c contains "x"`, Text("x"), nil, true, false},
		{"9 OR", `c == "x" || c == "y"`, Text("y"), nil, true, false},
		{"10 bool binding", `auth.signed_in`, Null(), Values{"auth.signed_in": Bool(true)}, true, false},
		{"10 text binding", `auth.id == "x"`, Null(), Values{"auth.id": Text("x")}, true, false},
		{"10 null binding", `auth.id == "x"`, Null(), Values{"auth.id": Null()}, false, false},
		{"10 binding null test", `auth.id is null`, Null(), Values{"auth.id": Null()}, true, false},
		{"10 lower binding", `lower(auth.id) == "x"`, Null(), Values{"auth.id": Text("X")}, true, false},
		{"10 list binding", `auth.id in auth.teams`, Null(), Values{"auth.id": Text("a"), "auth.teams": List("a")}, true, false},
		{"10 number binding", `event.number in [1,2.5]`, Null(), Values{"event.number": Float(2.5)}, true, false},
		{"10 contains binding", `auth.id contains "x"`, Null(), Values{"auth.id": Text("xy")}, true, false},
		{"10 bool equality", `auth.signed_in == true`, Null(), Values{"auth.signed_in": Bool(false)}, false, true},
		{"10 bindings equality", `auth.id == auth.role`, Null(), Values{"auth.id": Text("x"), "auth.role": Text("x")}, true, false},
		{"10 constant", `true`, Null(), nil, true, false},
		{"stored NUL equality", `c == "x"`, Text("x\x00y"), nil, false, true},
		{"stored NUL contains", `c contains "y"`, Text("x\x00y"), nil, true, false},
		{"stored NUL lower", `lower(c) contains "y"`, Text("X\x00Y"), nil, true, false},
		{"stored invalid UTF8 equality", `c == "x"`, Text("x\xff"), nil, false, true},
		{"stored invalid UTF8 contains", `c contains "y"`, Text("x\xffy"), nil, true, false},
		{"stored invalid UTF8 lower", `lower(c) contains "y"`, Text("X\xffY"), nil, true, false},
		{"stored positive infinity", `c == 1`, Float(math.Inf(1)), nil, false, true},
		{"stored negative infinity", `c in [-1,1]`, Float(math.Inf(-1)), nil, false, true},
	} {
		for _, neg := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/neg=%v", tc.name, neg), func(t *testing.T) {
				expr, want := tc.expr, tc.yes
				if neg {
					expr, want = "!("+expr+")", tc.no
				}
				p := compilePolicies(t, Policy{Command: CmdRead, Using: expr}, Policy{Command: CmdAdd, Check: expr})
				row := Row{"c": tc.stored}
				got, err := p.EvalUsing(OpRead, tc.values, row)
				if err != nil || got != want {
					t.Fatalf("using = %v, %v; want %v", got, err, want)
				}
				got, err = p.EvalCheck(OpAdd, tc.values, row)
				if err != nil || got != want {
					t.Fatalf("check = %v, %v; want %v", got, err, want)
				}
				if !hasColumn(p.using[OpRead]) {
					pred, err := p.Using(OpRead, tc.values)
					expected := "0"
					if want {
						expected = "1"
					}
					if err != nil || pred.SQL != expected || len(pred.Args) != 0 {
						t.Fatal(pred, err)
					}
				}
			})
		}
	}
}
func TestMixedNumberBoundaries(t *testing.T) {
	for _, tc := range []struct {
		i  int64
		f  float64
		eq bool
	}{
		{1 << 53, 1 << 53, true}, {1<<53 + 1, 1 << 53, false}, {1<<53 - 1, 1<<53 - 1, true},
		{-1 << 53, -1 << 53, true}, {-1<<53 - 1, -1 << 53, false}, {-1<<53 + 1, -1<<53 + 1, true},
		{math.MaxInt64, float64(math.MaxInt64), false}, {math.MinInt64, float64(math.MinInt64), true},
		{math.MinInt64 + 1, float64(math.MinInt64), false},
		{math.MaxInt64, math.Nextafter(float64(math.MaxInt64), 0), false},
		{math.MaxInt64 - 1023, math.Nextafter(float64(math.MaxInt64), 0), true},
		{math.MinInt64, math.Nextafter(float64(math.MinInt64), math.Inf(-1)), false},
		{math.MinInt64, math.Nextafter(float64(math.MinInt64), 0), false},
		{0, math.Copysign(0, -1), true}, {1, 1.5, false}, {-1, -1.5, false},
		{math.MaxInt64, math.Inf(1), false}, {math.MinInt64, math.Inf(-1), false},
	} {
		for _, swap := range []bool{false, true} {
			for _, neg := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%g/swap=%v/neg=%v", tc.i, tc.f, swap, neg), func(t *testing.T) {
					stored, bound := Float(tc.f), Int(tc.i)
					if swap {
						if math.IsInf(tc.f, 0) {
							return
						}
						stored, bound = Int(tc.i), Float(tc.f)
					}
					expr := `c == event.number`
					if neg {
						expr = "!(" + expr + ")"
					}
					p := readPlan(t, expr)
					got, err := p.EvalUsing(OpRead, Values{"event.number": bound}, Row{"c": stored})
					if err != nil || got != (tc.eq != neg) {
						t.Fatal(got, err, "equal", tc.eq)
					}
				})
			}
		}
	}
	// Integer/integer remains exact at both extremes; real/real is IEEE equality.
	for _, v := range []Value{Int(math.MinInt64), Int(math.MaxInt64), Float(math.MaxFloat64), Float(math.SmallestNonzeroFloat64)} {
		p := readPlan(t, `c == event.number`)
		got, err := p.EvalUsing(OpRead, Values{"event.number": v}, Row{"c": v})
		if err != nil || !got {
			t.Fatal(v, got, err)
		}
	}
}
