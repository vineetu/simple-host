package rules

import (
	"reflect"
	"testing"
)

func TestConvertExpressions(t *testing.T) {
	for _, tc := range []struct {
		who, expr string
	}{
		{"nobody", ""},
		{"owner", `auth.role == "owner"`},
		{"own", `visitor_id == auth.id || auth.role == "owner"`},
		{"signed-in", `auth.signed_in`},
		{"checked", `auth.checked || auth.ai || auth.role == "owner"`},
		{"anyone", `true`},
	} {
		for _, command := range []Command{CmdRead, CmdAdd, CmdEdit, CmdDelete} {
			t.Run(tc.who+"/"+string(command), func(t *testing.T) {
				m := Matrix{"nobody", "nobody", "nobody", "nobody"}
				dst := map[Command]*string{CmdRead: &m.Read, CmdAdd: &m.Add, CmdEdit: &m.Edit, CmdDelete: &m.Delete}
				*dst[command] = tc.who
				out := Convert(ConvertInput{Default: m, Kind: "sqlite"})
				manual := tc.who == "own" && command == CmdAdd || tc.who == "checked" && command != CmdAdd || tc.who == "anyone" && (command == CmdEdit || command == CmdDelete)
				if manual {
					if len(out.Manual) == 0 || out.Default.Policies != nil || out.Tables != nil {
						t.Fatalf("partial/manual conversion: %+v", out)
					}
					return
				}
				want := PolicySet{Version: 1, Policies: []Policy{}}
				if tc.expr != "" {
					p := Policy{Name: string(command), Command: command, Using: tc.expr}
					if command == CmdAdd {
						p.Using, p.Check = "", tc.expr
					}
					want.Policies = append(want.Policies, p)
				}
				if len(out.Manual) != 0 || !reflect.DeepEqual(out.Default, want) {
					t.Fatalf("got %+v, want %+v", out, want)
				}
				if _, errs := Compile(out.Default, ruleTable(), testSchema(), DefaultLimits()); len(errs) != 0 {
					t.Fatal(errs)
				}
			})
		}
	}
}

func TestConvertApprovalAndOverrides(t *testing.T) {
	checked := Matrix{"anyone", "checked", "owner", "owner"}
	plain := Matrix{"anyone", "signed-in", "owner", "own"}
	for _, defaultMatrix := range []Matrix{checked, plain} {
		for _, hold := range []bool{false, true} {
			in := ConvertInput{Default: defaultMatrix, Tables: map[string]Matrix{"comments": checked, "wall": plain}, ApproveFirst: hold, Kind: "sqlite"}
			out := Convert(in)
			if len(out.Manual) != 0 {
				t.Fatal(out.Manual)
			}
			sets := map[string]PolicySet{"default": out.Default, "comments": out.Tables["comments"], "wall": out.Tables["wall"]}
			for name, set := range sets {
				wantHold := hold && (name == "comments" || name == "default" && defaultMatrix.Add == "checked")
				var found bool
				for _, p := range set.Policies {
					if p.Restrictive {
						found = true
						if p.Command != CmdRead || p.Using != `auth.role == "owner" || approved_at is not null` {
							t.Fatal(p)
						}
					}
				}
				if found != wantHold {
					t.Fatalf("%s: hold=%v, want %v", name, found, wantHold)
				}
			}
			if !reflect.DeepEqual(out, Convert(in)) || in.Tables["comments"] != checked || in.Tables["wall"] != plain {
				t.Fatal("conversion mutates inputs or is not deterministic")
			}
		}
	}
}

func TestConvertManualIsAtomicAndOrdered(t *testing.T) {
	in := ConvertInput{Kind: "sqlite", Default: Matrix{"anyone", "signed-in", "own", "own"}, Tables: map[string]Matrix{
		"z": {"anyone", "anyone", "anyone", "anyone"},
		"a": {"anyone", "anyone", "anyone", "nobody"},
	}}
	want := []string{`a: edit "anyone" needs a manual decision`, `z: edit "anyone" needs a manual decision`, `z: delete "anyone" needs a manual decision`}
	for i := 0; i < 20; i++ {
		out := Convert(in)
		if !reflect.DeepEqual(out, ConvertOutput{Manual: want}) {
			t.Fatal(out)
		}
	}
}
