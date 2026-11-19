package handler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStorageRulesAPIResponses(t *testing.T) {
	s := newStorageTestSite(t)
	body := `{"kind":"sqlite","rules":{"policies":[{"name":"read","command":"read","using":"true"}]},"tables":{"notes":{"rules":{"policies":[]}}}}`
	saved := s.must(t, "PUT", "/resources/rules", body, s.key, 201).json(t)
	assertRules := func(resource map[string]any) {
		t.Helper()
		if resource["rules_status"] != "rules" || resource["rules"] == nil || len(resource["rules_errors"].([]any)) != 0 {
			t.Fatalf("incomplete rules response: %#v", resource)
		}
		for _, key := range []string{"preset", "access", "read", "add", "edit", "delete", "write", "write_mode", "approve_first", "based_on", "legacy_note"} {
			if _, ok := resource[key]; ok {
				t.Errorf("rules response contains %s", key)
			}
		}
		table := resource["tables"].(map[string]any)["notes"].(map[string]any)
		if len(table) != 1 || table["rules"] == nil {
			t.Fatalf("table response: %#v", table)
		}
	}
	assertRules(saved)
	// A null rules field is absent, so a passcode-only update preserves both sets.
	updated := s.must(t, "PUT", "/resources/rules", `{"kind":"sqlite","site_passcode":"off","rules":null}`, s.key, 200).json(t)
	assertRules(updated)
	if updated["site_passcode"] != "off" {
		t.Fatal(updated)
	}
	listing := s.must(t, "GET", "/resources", nil, s.key, 200).json(t)["resources"].([]any)
	if len(listing) != 1 {
		t.Fatal(listing)
	}
	assertRules(listing[0].(map[string]any))
}

func TestStorageRulesRecoveryExports(t *testing.T) {
	s := newStorageTestSite(t)
	private := s.must(t, "PUT", "/resources/private", `{"kind":"sqlite"}`, s.key, 201).json(t)
	delete(private, "warnings")
	saved := s.must(t, "PUT", "/resources/rules", `{"kind":"sqlite","rules":{"policies":[{"name":"read","command":"read","using":"true"}]},"tables":{"notes":{"rules":{"policies":[{"name":"owner","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}},"closed":{"rules":{"policies":[]}}}}`, s.key, 201).json(t)

	for _, tc := range []struct {
		name, path string
		files      func(*testing.T, []byte) map[string]string
	}{
		{"site", "/v1/sites/shop/export.tar.gz", tarFiles},
		{"account", "/v1/me/export.zip", zipFiles},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.a.at(t, "GET", pcSiteDomain, tc.path, nil, s.key)
			if r.status != 200 {
				t.Fatalf("export: %d %s", r.status, r.body)
			}
			var archive struct {
				Resources []map[string]any `json:"resources"`
			}
			if err := json.Unmarshal([]byte(fileUnder(t, tc.files(t, r.body), "shop/storage/resources.json")), &archive); err != nil {
				t.Fatal(err)
			}
			if len(archive.Resources) != 2 {
				t.Fatalf("exported resources: %#v", archive.Resources)
			}
			for _, resource := range archive.Resources {
				switch resource["name"] {
				case "private":
					if !reflect.DeepEqual(resource, private) {
						t.Fatalf("owner-only export changed: %#v; want %#v", resource, private)
					}
				case "rules":
					if resource["rules_status"] != "rules" || !reflect.DeepEqual(resource["rules"], saved["rules"]) || !reflect.DeepEqual(resource["tables"], saved["tables"]) {
						t.Fatalf("export lost rules: %#v; saved %#v", resource, saved)
					}
					for _, field := range []string{"preset", "access", "read", "write", "write_mode", "approve_first"} {
						if _, ok := resource[field]; ok {
							t.Errorf("rules export contains preset field %s", field)
						}
					}
				default:
					t.Fatalf("unexpected resource: %#v", resource)
				}
			}
		})
	}
}

func TestStorageRulesAPIErrors(t *testing.T) {
	s := newStorageTestSite(t)
	for _, tc := range []struct{ name, body, code, clause, table string }{
		{"syntax", `{"kind":"sqlite","rules":{"policies":[{"name":"broken","command":"read","using":"("}]}}`, "invalid_rules", "using", ""},
		{"preset", `{"kind":"sqlite","preset":"","rules":{"policies":[]}}`, "invalid_resource", "", ""},
		{"files", `{"kind":"files","rules":{"policies":[{"name":"read","command":"read","using":"key == 'a'"}]}}`, "invalid_rules", "column", "files"},
		{"table", `{"kind":"sqlite","rules":{"policies":[]},"tables":{"notes":{"preset":"private","rules":{"policies":[]}}}}`, "invalid_resource", "", ""},
		{"shape", `{"kind":"sqlite","tables":{"notes":{"rules":{"policies":[]}}}}`, "invalid_rules", "shape", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.must(t, "PUT", "/resources/"+tc.name, tc.body, s.key, 400).json(t)
			if r["code"] != tc.code {
				t.Fatal(r)
			}
			if tc.clause == "" {
				return
			}
			diagnostic := r["rule_error"].(map[string]any)
			if diagnostic["clause"] != tc.clause || diagnostic["table"] != tc.table || diagnostic["message"] == "" || diagnostic["position"] == nil || diagnostic["policy"] == nil {
				t.Fatal(diagnostic)
			}
			all := r["rules_errors"].([]any)
			if len(all) == 0 || !reflect.DeepEqual(all[0], diagnostic) {
				t.Fatal(r)
			}
		})
	}
	for _, body := range []string{`{`, `{} {}`, `{"kind":"sqlite","tables":{"notes":5}}`} {
		r := s.must(t, "PUT", "/resources/malformed", body, s.key, 400).json(t)
		if r["code"] != "invalid_request" {
			t.Fatal(r)
		}
	}
}

func TestStorageRulesAPIBodyLimit(t *testing.T) {
	s := newStorageTestSite(t)
	expression := `body == "` + strings.Repeat("x", 2038) + `"`
	if len(expression) != 2048 {
		t.Fatal(len(expression))
	}
	set := map[string]any{"policies": []any{map[string]any{"name": "a", "command": "read", "using": expression}, map[string]any{"name": "b", "command": "read", "using": expression}}}
	tables := map[string]any{}
	for i := 0; i < 50; i++ {
		tables[fmt.Sprintf("t%d", i)] = map[string]any{"rules": set}
	}
	data, err := json.Marshal(map[string]any{"kind": "sqlite", "rules": map[string]any{"policies": []any{}}, "tables": tables})
	if err != nil || len(data) > 256<<10 {
		t.Fatalf("fixture size: %d, %v", len(data), err)
	}
	body := string(data) + strings.Repeat(" ", (256<<10)-len(data))
	saved := s.must(t, "PUT", "/resources/large", body, s.key, 201).json(t)
	if len(saved["tables"].(map[string]any)) != 50 {
		t.Fatal("lost table rules")
	}
	list := s.must(t, "GET", "/resources", nil, s.key, 200).json(t)["resources"].([]any)
	loaded := list[0].(map[string]any)
	if !reflect.DeepEqual(saved["tables"], loaded["tables"]) || !reflect.DeepEqual(saved["rules"], loaded["rules"]) {
		t.Fatal("rules did not round trip")
	}
	for _, excess := range []int{1, 1024} {
		r := s.must(t, "PUT", "/resources/large", body+strings.Repeat(" ", excess), s.key, 400).json(t)
		if r["code"] != "invalid_rules" || r["rule_error"].(map[string]any)["clause"] != "size" {
			t.Fatal(r)
		}
	}
}

func TestStorageRulesAPIInvalidStoredState(t *testing.T) {
	s := newStorageTestSite(t)
	s.must(t, "PUT", "/resources/broken", `{"kind":"sqlite","rules":{"policies":[]}}`, s.key, 201)
	// Stored rules that no longer compile.
	if _, err := s.a.database.Exec(`UPDATE site_storage_resources SET rules='{"policies":[{"name":"bad","command":"read","using":"auth.unknown"}]}' WHERE site_id=$1 AND name='broken'`, s.sid); err != nil {
		t.Fatal(err)
	}
	listing := s.must(t, "GET", "/resources", nil, s.key, 200).json(t)["resources"].([]any)
	item := listing[0].(map[string]any)
	if item["rules_status"] != "invalid" || len(item["rules_errors"].([]any)) == 0 {
		t.Fatal(item)
	}
}
