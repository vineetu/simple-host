package storage

import (
	"context"
	"reflect"
	"testing"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type testBindingProvider struct {
	NoBindings
	schema ruleengine.BindingSchema
	calls  int
}

func (p *testBindingProvider) Schema() ruleengine.BindingSchema { p.calls++; return p.schema }
func (*testBindingProvider) OwnerOnSite(context.Context, string, string) (bool, error) {
	return false, nil
}

func TestRuleEnginePlatformSchema(t *testing.T) {
	for _, spam := range []bool{false, true} {
		schema := PlatformSchema(spam)
		if _, ok := schema["auth.checked"]; ok != spam {
			t.Fatal(schema)
		}
		if schema["auth.id"] != (ruleengine.Binding{Type: ruleengine.TypeText, Nullable: true}) || schema["auth.teams"].Type != ruleengine.TypeTextList {
			t.Fatal(schema)
		}
		if spam && !schema["auth.checked"].AddCheckOnly {
			t.Fatal(schema)
		}
		set, errs := ruleengine.DecodeSet([]byte(`{"policies":[{"command":"read","using":"visitor_id == auth.id"}]}`))
		if len(errs) != 0 {
			t.Fatal(errs)
		}
		if errs := ruleengine.ValidateSyntax(set, schema, ruleengine.DefaultLimits()); len(errs) != 0 {
			t.Fatal(errs)
		}
	}
	a := PlatformSchema(false)
	delete(a, "auth.id")
	if _, ok := PlatformSchema(false)["auth.id"]; !ok {
		t.Fatal("schema shared across calls")
	}
}
func TestRuleEngineProductSchema(t *testing.T) {
	for _, tc := range []struct {
		name  string
		b     ruleengine.Binding
		valid bool
	}{
		{"auth.id", ruleengine.Binding{Type: ruleengine.TypeText}, false},
		{"row.x", ruleengine.Binding{Type: ruleengine.TypeText}, false},
		{"auth.teams", ruleengine.Binding{Type: ruleengine.TypeBool}, false},
		{"auth.teams", ruleengine.Binding{Type: ruleengine.TypeTextList}, true},
		{"auth.teams", ruleengine.Binding{Type: ruleengine.TypeTextList, AddCheckOnly: true}, false},
		{"auth.checked", ruleengine.Binding{Type: ruleengine.TypeBool}, false},
		{"event.stage", ruleengine.Binding{Type: ruleengine.TypeText, Nullable: true}, true},
		{"event.flag", ruleengine.Binding{Type: ruleengine.TypeBool, Nullable: true}, false},
		{"event.list", ruleengine.Binding{Type: ruleengine.TypeTextList, Nullable: true}, false},
		{"event.bad", ruleengine.Binding{Type: 0}, false},
		{"no_namespace", ruleengine.Binding{Type: ruleengine.TypeText}, false},
		{"a.b.c", ruleengine.Binding{Type: ruleengine.TypeText}, false},
		{"é.name", ruleengine.Binding{Type: ruleengine.TypeText}, false},
		{"event.1", ruleengine.Binding{Type: ruleengine.TypeText}, false},
		{"true.x", ruleengine.Binding{Type: ruleengine.TypeText}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &testBindingProvider{schema: ruleengine.BindingSchema{tc.name: tc.b}}
			got, err := MergeBindingSchema(false, p)
			if (err == nil) != tc.valid {
				t.Fatalf("%+v: %v", tc, err)
			}
			if p.calls != 1 {
				t.Fatal("Schema must be read once")
			}
			if tc.valid {
				p.schema[tc.name] = ruleengine.Binding{}
				if got[tc.name] != tc.b {
					t.Fatal("retained provider map")
				}
			}
		})
	}
	if s, err := MergeBindingSchema(true, nil); err != nil || !reflect.DeepEqual(s, PlatformSchema(true)) {
		t.Fatal(s, err)
	}
	if ok, err := (DefaultProvider{}).OwnerOnSite(context.Background(), "site", ""); err != nil || ok {
		t.Fatal(ok, err)
	}
	if v, err := (NoBindings{}).Values(context.Background(), BindingCall{}, nil); err != nil || v != nil {
		t.Fatal(v, err)
	}
}
