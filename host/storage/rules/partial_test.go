package rules

import "testing"

func TestPartialIdentityAndSimulatedSignIn(t *testing.T) {
	anon := Values{"auth.signed_in": Bool(false), "auth.role": Text("anon"), "auth.id": Null(), "auth.ai": Bool(false), "auth.checked": Bool(false)}
	signed := Values{"auth.signed_in": Bool(true), "auth.role": Text("visitor"), "auth.id": Text("me"), "auth.ai": Bool(false), "auth.checked": Bool(false)}
	owner := Values{"auth.signed_in": Bool(true), "auth.role": Text("owner"), "auth.id": Text("owner"), "auth.ai": Bool(false), "auth.checked": Bool(false)}
	simulated := Values{"auth.signed_in": Bool(true), "auth.role": Text("visitor"), "auth.ai": Bool(false), "auth.checked": Bool(false)}
	for _, tc := range []struct {
		expr string
		want [4]Tri
	}{
		{`auth.signed_in`, [4]Tri{False, True, True, True}},
		{`auth.role == "owner"`, [4]Tri{False, False, True, False}},
		{`visitor_id == auth.id`, [4]Tri{Unknown, Unknown, Unknown, Unknown}},
		{`auth.id == "me"`, [4]Tri{False, True, False, Unknown}},
		{`auth.signed_in && visitor_id == auth.id`, [4]Tri{False, Unknown, Unknown, Unknown}},
		{`visitor_id == auth.id || auth.role == "owner"`, [4]Tri{Unknown, Unknown, True, Unknown}},
		{`!(auth.signed_in && c == "x")`, [4]Tri{True, Unknown, Unknown, Unknown}},
		{`!(auth.id == "me")`, [4]Tri{False, False, True, Unknown}},
	} {
		p := readPlan(t, tc.expr)
		for i, v := range []Values{anon, signed, owner, simulated} {
			if got := p.Partial(OpRead, v); got != tc.want[i] {
				t.Fatal(tc.expr, i, got, tc.want[i])
			}
		}
	}
	p := compilePolicies(t, Policy{Command: CmdAdd, Check: `auth.checked || auth.ai || auth.role == "owner"`})
	if p.Partial(OpAdd, anon) != False || p.Partial(OpAdd, owner) != True {
		t.Fatal("checked partial")
	}
	anon["auth.checked"] = Bool(true)
	if p.Partial(OpAdd, anon) != True {
		t.Fatal("simulated spam check")
	}
	p = compilePolicies(t, Policy{Command: CmdRead, Using: "false"}, Policy{Command: CmdEdit, Using: "auth.signed_in", Check: "false"}, Policy{Command: CmdDelete, Using: "auth.signed_in"})
	if p.Partial(OpEdit, signed) != True || p.Partial(OpDelete, signed) != True {
		t.Fatal("partial mixed in read or check")
	}
}
func TestPartialTruthTables(t *testing.T) {
	forms := []string{"false", "true", `c == "x"`}
	and := [3][3]Tri{{False, False, False}, {False, True, Unknown}, {False, Unknown, Unknown}}
	or := [3][3]Tri{{False, True, Unknown}, {True, True, True}, {Unknown, True, Unknown}}
	for a, x := range forms {
		for b, y := range forms {
			if got := readPlan(t, "("+x+") && ("+y+")").Partial(OpRead, nil); got != and[a][b] {
				t.Fatal("AND", a, b, got)
			}
			if got := readPlan(t, "("+x+") || ("+y+")").Partial(OpRead, nil); got != or[a][b] {
				t.Fatal("OR", a, b, got)
			}
		}
	}
}
func TestReadIsOwnOrOwner(t *testing.T) {
	for _, tc := range []struct {
		name        string
		expressions []string
		want        bool
	}{
		{"one policy", []string{`visitor_id == auth.id || auth.role == "owner"`}, true},
		{"two policies", []string{`visitor_id == auth.id`, `auth.role == "owner"`}, true},
		{"public alternative", []string{`visitor_id == auth.id || true`}, false},
		{"signed-in alternative", []string{`visitor_id == auth.id`, `auth.signed_in`}, false},
		{"owner only", []string{`auth.role == "owner"`}, false},
		{"no permissive", nil, false},
		{"term reversed and qualified", []string{`(auth.id == row.VISITOR_ID && c == "x") || ("owner" == auth.role)`}, true},
		{"nested unguarded alternative", []string{`(visitor_id == auth.id || auth.role == "owner") && c == "x"`}, false},
		{"negation is not structural guard", []string{`!(visitor_id != auth.id)`}, false},
		{"extra public policy", []string{`visitor_id == auth.id`, `true`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var policies []Policy
			for _, s := range tc.expressions {
				policies = append(policies, Policy{Command: CmdRead, Using: s})
			}
			policies = append(policies, Policy{Command: CmdRead, Using: `auth.signed_in`, Restrictive: true})
			if got := compilePolicies(t, policies...).ReadIsOwnOrOwner(); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	p := compilePolicies(t, Policy{Command: CmdAll, Using: `visitor_id == auth.id || auth.role == "owner"`})
	if !p.ReadIsOwnOrOwner() {
		t.Fatal("all policy not counted")
	}
}
