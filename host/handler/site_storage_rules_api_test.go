package handler

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// R2-S3 ruling 3 requires rules_unavailable for a body or table rules field
// while the flag is off, with the service owning that refusal.
func TestStorageRulesAPIFlagOff(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	s := newPresetSite(t)
	for _, tc := range []struct {
		name, body string
	}{
		{"body", `{"kind":"sqlite","rules":{"policies":[]}}`},
		{"table", `{"kind":"sqlite","tables":{"notes":{"rules":{"policies":[]}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := s.must(t, "PUT", "/resources/"+tc.name, tc.body, s.key, 400)
			if r.json(t)["code"] != "rules_unavailable" {
				t.Fatalf("want rules_unavailable, got %s", r.body)
			}
		})
	}
}

func TestStorageRulesAPIOmittedTablePreset(t *testing.T) {
	for _, flag := range []string{"off", "on"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("STORAGE_RULES", flag)
			s := newPresetSite(t)
			r := s.must(t, "PUT", "/resources/defaults", `{"kind":"sqlite","tables":{"notes":{}}}`, s.key, 201)
			table := r.json(t)["tables"].(map[string]any)["notes"].(map[string]any)
			if table["preset"] != "private" {
				t.Fatalf("omitted table preset no longer defaults to private: %s", r.body)
			}
		})
	}
}

func TestStorageRulesAPIResponses(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
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
	for _, flag := range []string{"on", "off"} {
		t.Setenv("STORAGE_RULES", flag)
		listing := s.must(t, "GET", "/resources", nil, s.key, 200).json(t)["resources"].([]any)
		if len(listing) != 1 {
			t.Fatal(listing)
		}
		assertRules(listing[0].(map[string]any))
	}
}

func TestStorageRulesRecoveryExports(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	s := newPresetSite(t)
	preset := s.must(t, "PUT", "/resources/private", `{"kind":"sqlite","tables":{"notes":{}}}`, s.key, 201).json(t)
	delete(preset, "warnings")
	t.Setenv("STORAGE_RULES", "on")
	saved := s.must(t, "PUT", "/resources/rules", `{"kind":"sqlite","rules":{"policies":[{"name":"read","command":"read","using":"true"}]},"tables":{"notes":{"rules":{"policies":[{"name":"owner","command":"all","using":"auth.role == \"owner\"","check":"auth.role == \"owner\""}]}},"closed":{"rules":{"policies":[]}}}}`, s.key, 201).json(t)

	for _, flag := range []string{"on", "off"} {
		t.Run(flag, func(t *testing.T) {
			t.Setenv("STORAGE_RULES", flag)
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
							if !reflect.DeepEqual(resource, preset) {
								t.Fatalf("preset export changed: %#v; want %#v", resource, preset)
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
		})
	}
}

func TestStorageRulesAPIErrors(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
	for _, tc := range []struct{ name, body, code, clause, table string }{
		{"syntax", `{"kind":"sqlite","rules":{"policies":[{"name":"broken","command":"read","using":"("}]}}`, "invalid_rules", "using", ""},
		{"shape", `{"kind":"sqlite","preset":"","rules":{"policies":[]}}`, "invalid_rules", "shape", ""},
		{"files", `{"kind":"files","rules":{"policies":[]}}`, "invalid_rules", "shape", ""},
		{"table", `{"kind":"sqlite","rules":{"policies":[]},"tables":{"notes":{"preset":"private","rules":{"policies":[]}}}}`, "invalid_rules", "shape", "notes"},
		{"postgres", `{"kind":"kv","rules":{"policies":[]}}`, "rules_unavailable", "", ""},
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

func TestStorageRulesAPIPresetBytes(t *testing.T) {
	t.Setenv("STORAGE_RULES", "off")
	s := newPresetSite(t)
	r := s.must(t, "PUT", "/resources/private", `{"kind":"sqlite","tables":{"notes":{}}}`, s.key, 201)
	// Frozen pre-rules representation, including key order and the final newline.
	want := `{"access":{"read":"owner","add":"owner","edit":"owner","delete":"owner"},"approve_first":false,"kind":"sqlite","name":"private","preset":"private","read":"owner","site_passcode":"inherit","tables":{"notes":{"access":{"read":"owner","add":"owner","edit":"owner","delete":"owner"},"preset":"private"}},"warnings":["table notes does not exist yet; its preset applies once it is created"],"write":"owner","write_mode":"full"}` + "\n"
	if string(r.body) != want {
		t.Fatalf("preset response changed:\n%s\nwant:\n%s", r.body, want)
	}
	t.Setenv("STORAGE_RULES", "on")
	on := s.must(t, "PUT", "/resources/private", `{"kind":"sqlite","tables":{"notes":{}}}`, s.key, 200).json(t)
	if on["rules_status"] != "preset" {
		t.Fatal(on)
	}
	delete(on, "rules_status")
	off := r.json(t)
	if !reflect.DeepEqual(on, off) {
		t.Fatalf("flag changed preset fields: %#v vs %#v", on, off)
	}
}

func TestStorageRulesAPIBodyLimit(t *testing.T) {
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
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
	t.Setenv("STORAGE_RULES", "on")
	s := newPresetSite(t)
	s.must(t, "PUT", "/resources/broken", `{"kind":"sqlite","rules":{"policies":[]}}`, s.key, 201)
	// Simulate a pre-rules writer overwriting the fill while keeping rules.
	if _, err := s.a.database.Exec(`UPDATE site_storage_resources SET preset_base='private', rules='{"policies":[{"name":"bad","command":"read","using":"auth.unknown"}]}' WHERE site_id=$1 AND name='broken'`, s.sid); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"on", "off"} {
		t.Setenv("STORAGE_RULES", flag)
		listing := s.must(t, "GET", "/resources", nil, s.key, 200).json(t)["resources"].([]any)
		item := listing[0].(map[string]any)
		if item["rules_status"] != "invalid" || len(item["rules_errors"].([]any)) == 0 {
			t.Fatal(item)
		}
		if _, ok := item["preset"]; ok {
			t.Fatal("invalid rules represented as a preset")
		}
	}
}
