package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRegistryExportsPreserveDeclarations(t *testing.T) {
	limits := DefaultLimits()
	limits.DeletedRetention *= 2
	cases := []struct {
		name     string
		got      []Tool
		families []familyTools
	}{
		{"Tools", Tools(), hostFamilies(DefaultLimits())},
		{"ToolsWithLimits", ToolsWithLimits(limits), hostFamilies(limits)},
		{"HackTools", HackTools(), hackFamilies()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want []Tool
			for _, family := range tc.families {
				want = append(want, family.tools...)
			}
			gotJSON, err := json.Marshal(tc.got)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatal("registry export filtered or changed its declarations")
			}
		})
	}
}

func TestBrainRegistryInventory(t *testing.T) {
	query := func(string, map[string]any) (string, map[string]any, error) { return "answer", map[string]any{}, nil }
	s := NewBrainServer(BrainConfig{Query: query})
	want := BrainTools(query)
	entries := s.inventory()
	if len(entries) != len(want) {
		t.Fatalf("registered %d tools, want %d", len(entries), len(want))
	}
	for _, entry := range entries {
		if entry.family != famAdmin || entry.feature != string(famAdmin) || entry.edition != editionBrain {
			t.Errorf("%s has wrong registry metadata", entry.tool.Name)
		}
	}
	full, bare, byName := s.forCaller(Caller{})
	gotJSON, _ := json.Marshal(full)
	wantJSON, _ := json.Marshal(want)
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatal("brain listing changed")
	}
	if len(bare) != len(want) || len(byName) != len(want) {
		t.Fatal("brain listing and dispatch disagree")
	}
	for i, tool := range want {
		if bare[i].OutputSchema != nil || byName[tool.Name].run == nil {
			t.Errorf("%s missing bare listing or dispatch", tool.Name)
		}
	}
}
