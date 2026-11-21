package datahttp

import (
	"context"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type storageBindingFixture struct {
	appstorage.NoBindings
	schema      ruleengine.BindingSchema
	schemaCalls int
}

func (p *storageBindingFixture) Schema() ruleengine.BindingSchema { p.schemaCalls++; return p.schema }
func (*storageBindingFixture) OwnerOnSite(context.Context, string, string) (bool, error) {
	return false, nil
}

func TestSetStorageBindings(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  ruleengine.Type
		bad  bool
	}{
		{"auth.id", ruleengine.TypeText, true}, {"row.x", ruleengine.TypeText, true},
		{"auth.teams", ruleengine.TypeBool, true}, {"auth.teams", ruleengine.TypeTextList, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &DataHandler{}
			p := &storageBindingFixture{schema: ruleengine.BindingSchema{tc.name: {Type: tc.typ}}}
			var panicked bool
			func() { defer func() { panicked = recover() != nil }(); h.SetStorageBindings(p) }()
			if panicked != tc.bad {
				t.Fatalf("panicked=%t want %t", panicked, tc.bad)
			}
			if p.schemaCalls != 1 {
				t.Fatal("Schema called more than once")
			}
			if tc.bad {
				if h.storageBindings != nil {
					t.Fatal("invalid provider stored")
				}
				return
			}
			if h.StorageDeps().Bindings != p || h.StorageDeps().Bindings != p {
				t.Fatal("request services do not share provider")
			}
			if p.schemaCalls != 1 {
				t.Fatal("request read schema")
			}
		})
	}
	h := &DataHandler{}
	h.SetStorageBindings(nil)
	if _, ok := h.StorageDeps().Bindings.(appstorage.DefaultProvider); !ok {
		t.Fatal("nil did not select platform provider")
	}
}
