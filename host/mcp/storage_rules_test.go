package mcp

import (
	"strings"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/data"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// The rules examples storage_set_resource teaches must be valid rules, or a
// model copying them gets a refusal.
func TestStorageRulesText(t *testing.T) {
	for _, noSpam := range []bool{false, true} {
		s := NewServer(testConfig(Config{NoSpamCheck: noSpam}))
		_, _, byName := s.forCaller(Caller{})
		count := 0
		for _, line := range strings.Split(byName["storage_set_resource"].Description, "\n") {
			if !strings.HasPrefix(line, `{"version"`) {
				continue
			}
			set, errs := ruleengine.DecodeSet([]byte(line))
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			if errs := ruleengine.ValidateSyntax(set, appstorage.PlatformSchema(!noSpam), ruleengine.DefaultLimits()); len(errs) != 0 {
				t.Fatal(errs)
			}
			count++
		}
		if count == 0 {
			t.Error("no rules examples found")
		}
	}
}

func TestStorageRulesRefusedResourceHints(t *testing.T) {
	path := "/v1/sites/test/storage/resources/data"
	s := newTestServer(&recordingUpstream{answers: map[string]func() (int, string){
		"PUT " + path: func() (int, string) {
			return 400, `{"error":"unknown binding auth.rol","code":"invalid_rules","rule_error":{"policy":"read","clause":"using","position":0,"message":"unknown binding auth.rol"}}`
		},
	}})
	rec := send(t, s, toolCall("storage_set_resource", map[string]any{
		"site": "test", "name": "data", "body": map[string]any{"kind": "sqlite", "rules": map[string]any{"version": 1, "policies": []any{}}},
	}), nil, true)
	text, _, refused := resultOf(t, rec)
	if !refused || !strings.Contains(text, "[invalid_rules]") || !strings.Contains(text, "unknown binding auth.rol") {
		t.Fatalf("expected the coded rules refusal with its rule error: %s", text)
	}
}
