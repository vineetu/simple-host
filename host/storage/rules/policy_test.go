package rules

import (
	"bytes"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeRoundTrip(t *testing.T) {
	for _, raw := range []string{
		`{"policies":[]}`, `{"version":1,"policies":[]}`,
		`{"policies":[{"name":"a","command":"read","using":"  true\n"},{"command":"add","check":"true","restrictive":false}]}`,
		`{"version":1,"policies":[{"name":"","command":"read","using":"true"},{"name":"","command":"read","using":"false","restrictive":true}]}`,
	} {
		s, errs := DecodeSet([]byte(raw))
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		if s.Version != 1 {
			t.Fatal("noncanonical version", s)
		}
		encoded := EncodeSet(s)
		again, errs := DecodeSet(encoded)
		if len(errs) != 0 || !reflect.DeepEqual(s, again) || !bytes.Equal(encoded, EncodeSet(again)) {
			t.Fatalf("roundtrip: %s %+v", encoded, errs)
		}
	}
	s, errs := DecodeSet([]byte(`{"policies":[{"command":"read","using":"  true\n"}]}`))
	if len(errs) != 0 || s.Policies[0].Using != "  true\n" {
		t.Fatal("clause changed")
	}
	if got := string(EncodeSet(PolicySet{Policies: []Policy{}})); got != `{"version":1,"policies":[]}` {
		t.Fatal(got)
	}
}
func TestDecodeRefusals(t *testing.T) {
	for _, tc := range []struct{ raw, clause string }{
		{`{}`, "json"}, {`null`, "json"}, {`[]`, "json"}, {`{"policies":null}`, "json"},
		{`{"policies":{}}`, "json"}, {`{"policies":[null]}`, "json"},
		{`{"policies":[],"extra":1}`, "json"}, {`{"policies":[{"extra":1}]}`, "json"},
		{`{"policies":[{"using":null}]}`, "json"}, {`{"policies":[{"restrictive":"yes"}]}`, "json"},
		{`{"policies":[{"using":1}]}`, "json"}, {`{"policies":[]} {}`, "json"},
		{`{"version":0,"policies":[]}`, "version"}, {`{"version":2,"policies":[]}`, "version"},
		{`{"version":null,"policies":[]}`, "version"}, {`{"version":"1","policies":[]}`, "version"},
		{`{"policies":[{"name":"same"},{"name":"same"}]}`, "name"},
		{`{"policies":[{"command":"read","using":"true","check":""}]}`, "check"},
		{`{"policies":[{"command":"delete","using":"auth.signed_in","check":""}]}`, "check"},
		{`{"policies":[{"command":"add","check":"true","using":""}]}`, "using"},
		{`{"policies":[{"command":"read","using":"true","Check":""}]}`, "json"},
		{`{"Policies":[]}`, "json"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			_, errs := DecodeSet([]byte(tc.raw))
			if len(errs) == 0 || errs[0].Clause != tc.clause || errs[0].Position != -1 {
				t.Fatalf("%+v", errs)
			}
		})
	}
}
func TestClauseTable(t *testing.T) {
	for _, cmd := range []Command{CmdRead, CmdAdd, CmdEdit, CmdDelete, CmdAll} {
		for _, using := range []bool{false, true} {
			for _, check := range []bool{false, true} {
				p := Policy{Name: "p", Command: cmd}
				if using {
					p.Using = "auth.signed_in"
				}
				if check {
					p.Check = "true"
				}
				errs := ValidateSyntax(PolicySet{Policies: []Policy{p}}, testSchema(), DefaultLimits())
				var want []string
				if cmd == CmdAdd && using || cmd != CmdAdd && !using {
					want = append(want, "using")
				}
				if cmd == CmdAdd && !check || (cmd == CmdRead || cmd == CmdDelete) && check {
					want = append(want, "check")
				}
				if len(errs) != len(want) {
					t.Fatalf("%s using=%t check=%t: %+v want %v", cmd, using, check, errs, want)
				}
				for i, clause := range want {
					if errs[i].Clause != clause || errs[i].Position != -1 || errs[i].Policy != "p" {
						t.Fatal(errs)
					}
				}
			}
		}
	}
	// Inherited check is validated in using's placement, not as an add-only check.
	for _, cmd := range []Command{CmdRead, CmdAdd, CmdEdit, CmdDelete, CmdAll} {
		for _, clause := range []string{"using", "check"} {
			p := Policy{Command: cmd, Using: "auth.signed_in"}
			if cmd == CmdAdd {
				p.Using = ""
				p.Check = "true"
			}
			if clause == "using" {
				p.Using = "auth.checked"
			} else {
				p.Check = "auth.checked"
			}
			errs := ValidateSyntax(PolicySet{Policies: []Policy{p}}, testSchema(), DefaultLimits())
			if (len(errs) == 0) != (cmd == CmdAdd && clause == "check") {
				t.Fatalf("%s %s: %+v", cmd, clause, errs)
			}
		}
	}
	errs := ValidateSyntax(PolicySet{Policies: []Policy{{Command: CmdAdd, Check: `x > 1`}}}, testSchema(), DefaultLimits())
	if len(errs) != 1 || errs[0].Clause != "check" || errs[0].Position != 2 {
		t.Fatal(errs)
	}
}
func TestAnonymousChange(t *testing.T) {
	accepted := []string{
		`visitor_id == auth.id || auth.role == "owner"`, `(status == "open" || status == "new") && auth.signed_in`,
		`auth.signed_in`, `auth.signed_in == true`, `true == auth.signed_in`,
		`auth.id == row.VISITOR_ID`, `"owner" == auth.role`, `((auth.signed_in))`,
		`(auth.signed_in && status == "open") || (auth.role == "owner" && true)`,
	}
	refused := []string{
		`!auth.signed_in`, `auth.signed_in == false`, `auth.signed_in || true`, `status == "open"`, `true`,
		`!!auth.signed_in`, `!(auth.role != "owner")`, `(auth.signed_in || status == "open") && true`,
		`lower(visitor_id) == auth.id`,
	}
	for _, cmd := range []Command{CmdEdit, CmdDelete, CmdAll} {
		for _, s := range append(append([]string{}, accepted...), refused...) {
			want := false
			for _, a := range accepted {
				want = want || a == s
			}
			p := Policy{Command: cmd, Using: s}
			errs := ValidateSyntax(PolicySet{Policies: []Policy{p}}, testSchema(), DefaultLimits())
			if want && len(errs) != 0 || !want && (len(errs) != 1 || errs[0].Clause != "anonymous_change") {
				t.Fatalf("%s %s: %+v", cmd, s, errs)
			}
			p.Restrictive = true
			if errs := ValidateSyntax(PolicySet{Policies: []Policy{p}}, testSchema(), DefaultLimits()); len(errs) != 0 {
				t.Fatal(errs)
			}
		}
	}
	p := Policy{Command: CmdEdit, Using: "true", Check: "auth.signed_in"}
	if errs := ValidateSyntax(PolicySet{Policies: []Policy{p}}, testSchema(), DefaultLimits()); len(errs) != 1 || errs[0].Clause != "anonymous_change" {
		t.Fatal(errs)
	}
}
func TestSetLimits(t *testing.T) {
	for _, n := range []int{32, 33} {
		s := PolicySet{Policies: make([]Policy, n)}
		for i := range s.Policies {
			s.Policies[i] = Policy{Command: CmdRead, Using: "true"}
		}
		errs := ValidateSyntax(s, testSchema(), DefaultLimits())
		if (len(errs) == 0) != (n == 32) {
			t.Fatal(n, errs)
		}
	}
	for _, n := range []int{64, 65} {
		s := PolicySet{Policies: []Policy{{Name: strings.Repeat("a", n), Command: CmdRead, Using: "true"}}}
		errs := ValidateSyntax(s, testSchema(), DefaultLimits())
		if (len(errs) == 0) != (n == 64) {
			t.Fatal(n, errs)
		}
	}
	for _, s := range []PolicySet{{Version: 2, Policies: []Policy{}}, {}, {Policies: []Policy{{Command: "bad"}}}} {
		if errs := ValidateSyntax(s, testSchema(), DefaultLimits()); len(errs) == 0 {
			t.Fatal("accepted", s)
		}
	}
}
func TestValuesAndUnbuiltCompiler(t *testing.T) {
	if _, errs := Compile(PolicySet{}, Table{}, nil, DefaultLimits()); len(errs) != 1 || errs[0].Message != "not built yet" {
		t.Fatal(errs)
	}
	items := []string{"a"}
	v := List(items...)
	items[0] = "b"
	if v.List[0] != "a" || Null().Kind != KindNull {
		t.Fatal(v)
	}
	defer func() {
		if recover() == nil {
			t.Error("NaN accepted")
		}
	}()
	Float(math.NaN())
}
