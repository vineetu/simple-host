package datatest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestStorageRulesSaveAndList(t *testing.T) {
	s := newShop(t)
	body := `{"kind":"sqlite","rules":{"policies":[{"name":"read","command":"read","using":"true"}]},"tables":{"notes":{"rules":{"policies":[]}}}}`
	saved := s.Must(t, "PUT", "/resources/rules", body, s.Key, 201).JSON(t)
	check := func(r map[string]any) {
		t.Helper()
		if r["rules_status"] != "rules" || r["rules"] == nil || r["tables"].(map[string]any)["notes"] == nil {
			t.Fatalf("rules response: %#v", r)
		}
	}
	check(saved)
	// A null rules field is absent: a passcode-only update keeps both sets.
	updated := s.Must(t, "PUT", "/resources/rules", `{"kind":"sqlite","site_passcode":"off","rules":null}`, s.Key, 200).JSON(t)
	check(updated)
	if updated["site_passcode"] != "off" {
		t.Fatal(updated)
	}
	listing := s.Must(t, "GET", "/resources", nil, s.Key, 200).JSON(t)["resources"].([]any)
	check(listing[0].(map[string]any))
	// A visitor never manages resources.
	s.Must(t, "PUT", "/resources/rules", body, s.Callers["alice"], 403)
}

func TestStorageRulesAPIErrors(t *testing.T) {
	s := newShop(t)
	for _, tc := range []struct{ name, body, code, clause string }{
		{"syntax", `{"kind":"sqlite","rules":{"policies":[{"name":"broken","command":"read","using":"("}]}}`, "invalid_rules", "using"},
		{"preset", `{"kind":"sqlite","preset":"","rules":{"policies":[]}}`, "invalid_resource", ""},
		{"files", `{"kind":"files","rules":{"policies":[{"name":"read","command":"read","using":"key == 'a'"}]}}`, "invalid_rules", "column"},
		{"shape", `{"kind":"sqlite","tables":{"notes":{"rules":{"policies":[]}}}}`, "invalid_rules", "shape"},
		{"malformed", `{} {}`, "invalid_request", ""},
	} {
		r := s.Must(t, "PUT", "/resources/"+tc.name, tc.body, s.Key, 400).JSON(t)
		if r["code"] != tc.code {
			t.Fatalf("%s: %v", tc.name, r)
		}
		if tc.clause != "" && r["rule_error"].(map[string]any)["clause"] != tc.clause {
			t.Fatalf("%s: %v", tc.name, r["rule_error"])
		}
	}
}

func TestStorageRulesAPIBodyLimit(t *testing.T) {
	s := newShop(t)
	expression := `body == "` + strings.Repeat("x", 2038) + `"`
	set := map[string]any{"policies": []any{map[string]any{"name": "a", "command": "read", "using": expression}, map[string]any{"name": "b", "command": "read", "using": expression}}}
	tables := map[string]any{}
	for i := 0; i < 50; i++ {
		tables[fmt.Sprintf("t%d", i)] = map[string]any{"rules": set}
	}
	data, _ := json.Marshal(map[string]any{"kind": "sqlite", "rules": map[string]any{"policies": []any{}}, "tables": tables})
	if len(data) > 256<<10 {
		t.Fatalf("fixture size: %d", len(data))
	}
	body := string(data) + strings.Repeat(" ", (256<<10)-len(data))
	saved := s.Must(t, "PUT", "/resources/large", body, s.Key, 201).JSON(t)
	loaded := s.Must(t, "GET", "/resources", nil, s.Key, 200).JSON(t)["resources"].([]any)[0].(map[string]any)
	if len(saved["tables"].(map[string]any)) != 50 || !reflect.DeepEqual(saved["tables"], loaded["tables"]) {
		t.Fatal("rules did not round trip")
	}
	r := s.Must(t, "PUT", "/resources/large", body+" ", s.Key, 400).JSON(t)
	if r["code"] != "invalid_rules" || r["rule_error"].(map[string]any)["clause"] != "size" {
		t.Fatal(r)
	}
}

// Stored rules that no longer compile are listed as invalid, not dropped.
func TestStorageRulesAPIInvalidStoredState(t *testing.T) {
	s := newShop(t)
	s.Must(t, "PUT", "/resources/broken", `{"kind":"sqlite","rules":{"policies":[]}}`, s.Key, 201)
	if _, err := s.A.Database.Exec(`UPDATE site_storage_resources SET rules='{"policies":[{"name":"bad","command":"read","using":"auth.unknown"}]}' WHERE site_id=$1 AND name='broken'`, s.Sid); err != nil {
		t.Fatal(err)
	}
	item := s.Must(t, "GET", "/resources", nil, s.Key, 200).JSON(t)["resources"].([]any)[0].(map[string]any)
	if item["rules_status"] != "invalid" || len(item["rules_errors"].([]any)) == 0 {
		t.Fatal(item)
	}
}

// A table's own rules override the resource's.
func TestStorageRulesReadTableOverride(t *testing.T) {
	s := newShop(t)
	s.Must(t, "PUT", "/resources/data", `{"kind":"sqlite","rules":{"policies":[{"command":"read","using":"true"}]},"tables":{"closed":{"rules":{"policies":[]}}}}`, s.Key, 201)
	for table, status := range map[string]int{"open": 200, "closed": 403} {
		s.Must(t, "POST", "/sqlite/data/schema", map[string]string{"sql": "CREATE TABLE " + table + "(body TEXT)"}, s.Key, 200)
		s.Must(t, "POST", "/sqlite/data/execute", map[string]string{"sql": "INSERT INTO " + table + " VALUES('secret')"}, s.Key, 200)
		for _, suffix := range []string{"", "/1"} {
			s.Must(t, "GET", "/sqlite/data/tables/"+table+"/rows"+suffix, nil, s.Callers["anon"], status)
		}
	}
}
