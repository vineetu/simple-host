package mcp

import "testing"

func TestRegistryRejectsDuplicateEditionName(t *testing.T) {
	r := &toolRegistry{}
	r.add(editionHost, famSites, []Tool{{Name: "duplicate"}})
	r.add(editionHack, famHackEvents, []Tool{{Name: "duplicate"}})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate name within an edition did not panic")
		}
	}()
	r.add(editionHost, famAccess, []Tool{{Name: "duplicate"}})
}
