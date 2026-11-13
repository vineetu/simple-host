package model

import (
	"encoding/json"
	"testing"
)

func rulesTestResource() Resource {
	deny := AccessMatrix{WhoNobody, WhoNobody, WhoNobody, WhoNobody}
	return Resource{
		Name: "data", Kind: "sqlite", Read: WhoOwner, Write: WhoOwner, WriteMode: "add",
		Access: &deny, PresetBase: "rules", Rules: json.RawMessage(`{"version":1,"policies":[]}`),
		Tables: map[string]TableRule{"posts": {Matrix: deny, PresetBase: "rules", Rules: json.RawMessage(`{"version":1,"policies":[]}`)}},
	}
}

func TestResourceRulesStatus(t *testing.T) {
	tests := []struct {
		name, want string
		change     func(*Resource)
	}{
		{"filled", "rules", func(*Resource) {}},
		{"no tables", "rules", func(r *Resource) { r.Tables = nil }},
		{"legacy", "preset", func(r *Resource) { *r = Resource{} }},
		{"preset", "preset", func(r *Resource) { r.Rules = nil; r.PresetBase = "private" }},
		{"missing rules", "invalid", func(r *Resource) { r.Rules = nil }},
		{"missing matrix", "invalid", func(r *Resource) { r.Access = nil }},
		{"read policy", "invalid", func(r *Resource) { r.Read = WhoAnyone }},
		{"write policy", "invalid", func(r *Resource) { r.Write = WhoAnyone }},
		{"write mode", "invalid", func(r *Resource) { r.WriteMode = "full" }},
		{"preset base", "invalid", func(r *Resource) { r.PresetBase = "private" }},
		{"approve first", "invalid", func(r *Resource) { r.ApproveFirst = true }},
		{"matrix read", "invalid", func(r *Resource) { r.Access.Read = WhoOwner }},
		{"matrix add", "invalid", func(r *Resource) { r.Access.Add = WhoOwner }},
		{"matrix edit", "invalid", func(r *Resource) { r.Access.Edit = WhoOwner }},
		{"matrix delete", "invalid", func(r *Resource) { r.Access.Delete = WhoOwner }},
		{"structural only", "rules", func(r *Resource) { r.Rules = json.RawMessage(`{"not_a_set":true}`) }},
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
	for _, test := range []struct {
		name   string
		change func(*TableRule)
	}{
		{"rules", func(r *TableRule) { r.Rules = nil }},
		{"preset base", func(r *TableRule) { r.PresetBase = "private" }},
		{"read", func(r *TableRule) { r.Matrix.Read = WhoOwner }},
		{"add", func(r *TableRule) { r.Matrix.Add = WhoOwner }},
		{"edit", func(r *TableRule) { r.Matrix.Edit = WhoOwner }},
		{"delete", func(r *TableRule) { r.Matrix.Delete = WhoOwner }},
	} {
		t.Run("table/"+test.name, func(t *testing.T) {
			r := rulesTestResource()
			table := r.Tables["posts"]
			test.change(&table)
			r.Tables["posts"] = table
			if got := r.RulesStatus(); got != "invalid" {
				t.Fatalf("RulesStatus = %q, want invalid", got)
			}
			r.TableRange = func(visit func(string, TableRule)) { visit("posts", table) }
			r.Tables = nil
			if got := r.RulesStatus(); got != "invalid" {
				t.Fatalf("callback RulesStatus = %q, want invalid", got)
			}
		})
	}
}

func TestResourceRulesFingerprint(t *testing.T) {
	r := rulesTestResource()
	original := r.Fingerprint()
	r.ConvertedFrom = json.RawMessage(`{"preset":"private"}`)
	if r.Fingerprint() != original {
		t.Fatal("conversion history changed fingerprint")
	}
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
