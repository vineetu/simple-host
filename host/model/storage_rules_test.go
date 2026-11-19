package model

import (
	"encoding/json"
	"testing"
)

func rulesTestResource() Resource {
	return Resource{
		Name: "data", Kind: "sqlite", Rules: json.RawMessage(`{"version":1,"policies":[]}`),
		Tables: map[string]TableRule{"posts": {Rules: json.RawMessage(`{"version":1,"policies":[]}`)}},
	}
}

func TestResourceRulesStatus(t *testing.T) {
	tests := []struct {
		name, want string
		change     func(*Resource)
	}{
		{"rules", "rules", func(*Resource) {}},
		{"no tables", "rules", func(r *Resource) { r.Tables = nil }},
		{"null rules", "rules", func(r *Resource) { *r = Resource{Kind: "sqlite"} }},
		{"files", "rules", func(r *Resource) { r.Kind, r.Tables = "files", nil }},
		{"files without rules", "rules", func(r *Resource) { *r = Resource{Kind: "files"} }},
		{"structural only", "rules", func(r *Resource) { r.Rules = json.RawMessage(`{"not_a_set":true}`) }},
		{"table without rules", "invalid", func(r *Resource) { r.Tables["posts"] = TableRule{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := rulesTestResource()
			test.change(&r)
			if got := r.RulesStatus(); got != test.want {
				t.Fatalf("RulesStatus = %q, want %q", got, test.want)
			}
		})
	}
	r := rulesTestResource()
	r.TableRange = func(visit func(string, TableRule)) { visit("posts", TableRule{}) }
	r.Tables = nil
	if got := r.RulesStatus(); got != "invalid" {
		t.Fatalf("callback RulesStatus = %q, want invalid", got)
	}
}

// A resource with no stored rules is owner only, whatever its kind.
func TestEffectiveRulesWithoutRules(t *testing.T) {
	for _, kind := range []string{"kv", "sqlite", "files"} {
		if got := (Resource{Kind: kind}).EffectiveRules(""); string(got) != string(OwnerOnlyRules) {
			t.Fatalf("%s: %s", kind, got)
		}
	}
}

func TestResourceRulesFingerprint(t *testing.T) {
	r := rulesTestResource()
	original := r.Fingerprint()
	for _, value := range []json.RawMessage{nil, json.RawMessage(`{"version":1,"policies":[{"name":"public_read","command":"read","using":"true"}]}`)} {
		changed := rulesTestResource()
		changed.Rules = value
		if changed.Fingerprint() == original {
			t.Fatal("resource rules missing from fingerprint")
		}
		changed = rulesTestResource()
		table := changed.Tables["posts"]
		table.Rules = value
		changed.Tables["posts"] = table
		if changed.Fingerprint() == original {
			t.Fatal("table rules missing from fingerprint")
		}
	}
}
