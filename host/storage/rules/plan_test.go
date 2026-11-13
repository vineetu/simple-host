package rules

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestPolicyCompositionAndDefaults(t *testing.T) {
	for _, policies := range [][]Policy{nil, {{Command: CmdRead, Using: "true", Restrictive: true}}} {
		p := compilePolicies(t, policies...)
		got, err := p.Using(OpRead, nil)
		if err != nil || got.SQL != "0" || len(got.Args) != 0 {
			t.Fatal(got, err)
		}
	}
	p := compilePolicies(t,
		Policy{Command: CmdRead, Using: `c == "a"`}, Policy{Command: CmdRead, Using: `c == "b"`},
		Policy{Command: CmdRead, Using: `c != "b"`, Restrictive: true}, Policy{Command: CmdRead, Using: `c is not null`, Restrictive: true},
	)
	for _, s := range []string{"a", "b", "c"} {
		got, err := p.EvalUsing(OpRead, nil, Row{"c": Text(s)})
		if err != nil || got != (s == "a") {
			t.Fatal(s, got, err)
		}
	}
	pred, err := p.Using(OpRead, nil)
	if err != nil || !strings.Contains(pred.SQL, " OR ") || !strings.Contains(pred.SQL, " AND ") || !reflect.DeepEqual(pred.Args, []Value{Text("a"), Text("b"), Text("b")}) {
		t.Fatal(pred, err)
	}
	for _, cmd := range []Command{CmdAll, CmdEdit} {
		for _, explicit := range []bool{false, true} {
			pol := Policy{Command: cmd, Using: `visitor_id == auth.id`}
			if explicit {
				pol.Check = `c == "new"`
			}
			p := compilePolicies(t, pol)
			v := Values{"auth.id": Text("me")}
			for _, op := range []Op{OpRead, OpAdd, OpEdit, OpDelete} {
				if op != OpAdd {
					got, err := p.EvalUsing(op, v, Row{"visitor_id": Text("me")})
					want := cmd == CmdAll || op == OpEdit
					if err != nil || got != want {
						t.Fatal(cmd, op, got, err)
					}
				}
				if op == OpAdd || op == OpEdit {
					got, err := p.EvalCheck(op, v, Row{"visitor_id": Text("me"), "c": Text("old")})
					want := (cmd == CmdAll || op == OpEdit) && !explicit
					if err != nil || got != want {
						t.Fatal(cmd, op, got, err)
					}
				}
			}
		}
	}
	// Edit's USING neither includes read nor silently applies its written-row check.
	p = compilePolicies(t, Policy{Command: CmdRead, Using: "false"}, Policy{Command: CmdEdit, Using: "auth.signed_in", Check: "false"})
	v := Values{"auth.signed_in": Bool(true)}
	got, err := p.Using(OpEdit, v)
	if err != nil || got.SQL != "1" {
		t.Fatal(got, err)
	}
	check, err := p.Check(OpEdit, v)
	if err != nil || check.SQL != "0" {
		t.Fatal(check, err)
	}
	for _, op := range []Op{0, OpAdd, 99} {
		if _, err := p.Using(op, v); err == nil {
			t.Fatal("Using accepted", op)
		}
		if _, err := p.EvalUsing(op, v, nil); err == nil {
			t.Fatal("EvalUsing accepted", op)
		}
	}
	for _, op := range []Op{0, OpRead, OpDelete, 99} {
		if _, err := p.Check(op, v); err == nil {
			t.Fatal("Check accepted", op)
		}
		if _, err := p.EvalCheck(op, v, nil); err == nil {
			t.Fatal("EvalCheck accepted", op)
		}
	}
}
func TestPlanMetadata(t *testing.T) {
	p := compilePolicies(t,
		Policy{Command: CmdRead, Using: `visitor_id == auth.id`},
		Policy{Command: CmdRead, Using: `approved_at is not null || auth.organiser`, Restrictive: true},
		Policy{Command: CmdAdd, Check: `auth.checked || auth.ai`},
		Policy{Command: CmdEdit, Using: `auth.signed_in`, Check: `c in auth.teams`},
		Policy{Command: CmdDelete, Using: `auth.role == "owner"`},
	)
	for _, tc := range []struct {
		op   Op
		want []string
	}{
		{OpRead, []string{"auth.id", "auth.organiser"}}, {OpAdd, []string{"auth.ai", "auth.checked"}},
		{OpEdit, []string{"auth.id", "auth.organiser", "auth.signed_in", "auth.teams"}},
		{OpDelete, []string{"auth.id", "auth.organiser", "auth.role"}},
	} {
		if got := p.Needs(tc.op); !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.op, got, tc.want)
		}
	}
	if !p.ReferencesColumn("VISITOR_ID") || !p.ReferencesColumn("approved_AT") || p.ReferencesColumn("missing") {
		t.Fatal("column metadata")
	}
	if !p.ReferencesBinding(OpAdd, "auth.checked") || p.ReferencesBinding(OpRead, "auth.checked") || p.ReferencesBinding(OpDelete, "auth.id") {
		t.Fatal("binding metadata")
	}
	for _, op := range []Op{OpRead, OpAdd, OpEdit, OpDelete} {
		if !p.NeedsIdentity(op) {
			t.Fatal("identity metadata", op)
		}
	}
	p = compilePolicies(t, Policy{Command: CmdRead, Using: "true"}, Policy{Command: CmdRead, Using: `auth.role == "owner"`, Restrictive: true})
	if p.NeedsIdentity(OpRead) || !p.ReferencesBinding(OpRead, "auth.role") {
		t.Fatal("restrictive binding triggers identity")
	}
	for _, tc := range []struct {
		policies []Policy
		want     bool
	}{
		{nil, false}, {[]Policy{{Command: CmdRead, Using: `visitor_id == auth.id`}}, true},
		{[]Policy{{Command: CmdAdd, Check: "true"}}, true}, {[]Policy{{Command: CmdAdd, Check: `auth.role == "owner"`}}, false},
		{[]Policy{{Command: CmdAdd, Check: `auth.signed_in && c == "x"`}}, true},
		{[]Policy{{Command: CmdAdd, Check: `auth.checked || auth.ai`}}, true},
		{[]Policy{{Command: CmdAdd, Check: `auth.ai`}}, false},
		{[]Policy{{Command: CmdAdd, Check: `auth.id == "person"`}}, true},
	} {
		if got := compilePolicies(t, tc.policies...).AuthorTracked(); got != tc.want {
			t.Fatal(tc.policies, got)
		}
	}
}
func TestBindingValidation(t *testing.T) {
	for _, tc := range []struct {
		expr, name string
		bad        []Value
	}{
		{`c == auth.id`, "auth.id", []Value{Int(1), Text("x\x00"), Text("\xff"), List()}},
		{`c == event.number`, "event.number", []Value{Text("1"), Float(math.Inf(1)), Float(math.Inf(-1)), {Kind: KindFloat, Float: math.NaN()}}},
		{`auth.signed_in`, "auth.signed_in", []Value{Null(), Int(1), Text("true")}},
		{`c in auth.teams`, "auth.teams", []Value{Null(), Text("x"), List("\x00"), List("\xff"), List(make([]string, 65)...)}},
	} {
		p := compilePolicies(t, Policy{Command: CmdRead, Using: tc.expr}, Policy{Command: CmdAdd, Check: tc.expr})
		values := []Values{nil}
		for _, bad := range tc.bad {
			values = append(values, Values{tc.name: bad})
		}
		for _, v := range values {
			if _, err := p.Using(OpRead, v); err == nil {
				t.Fatal("Using accepted", tc.expr, v)
			}
			if _, err := p.Check(OpAdd, v); err == nil {
				t.Fatal("Check accepted", tc.expr, v)
			}
			if _, err := p.EvalUsing(OpRead, v, nil); err == nil {
				t.Fatal("EvalUsing accepted", tc.expr, v)
			}
			if _, err := p.EvalCheck(OpAdd, v, nil); err == nil {
				t.Fatal("EvalCheck accepted", tc.expr, v)
			}
			if got := p.Partial(OpRead, v); got != Unknown {
				t.Fatal("partial invalid binding", got)
			}
		}
	}
	// A true branch must not conceal a required missing value.
	p := readPlan(t, `true || auth.signed_in`)
	if _, err := p.Using(OpRead, nil); err == nil {
		t.Fatal("short circuit concealed missing binding")
	}
	// Extras are irrelevant to the effective operation.
	if _, err := readPlan(t, "true").Using(OpRead, Values{"unused": Text("\x00")}); err != nil {
		t.Fatal(err)
	}
}
