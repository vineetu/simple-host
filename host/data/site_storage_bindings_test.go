package data

import (
	"context"
	"testing"

	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

type storageBindingFixture struct {
	NoBindings
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
		{"auth.id", ruleengine.TypeText, true}, {"auth.teams", ruleengine.TypeTextList, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{}
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
			if h.StorageDeps().Bindings != p {
				t.Fatal("request services do not share provider")
			}
		})
	}
	h := &Handler{}
	h.SetStorageBindings(nil)
	// No provider: the storage service answers with the platform's own
	// (data.DefaultProvider), chosen where it is used.
	if h.StorageDeps().Bindings != nil {
		t.Fatal("nil did not leave the platform provider")
	}
}
