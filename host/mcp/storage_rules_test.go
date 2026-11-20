package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	appstorage "github.com/vsriram/simple-host/host/app/storage"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

func TestStorageRulesText(t *testing.T) {
	for _, noSpam := range []bool{false, true} {
		s := NewServer(testConfig(Config{NoSpamCheck: noSpam}))
		// The platform's own connections; a product's are tested with the product.
		for _, caller := range []Caller{{}, {DeployOnly: true}, {AppName: "ChatGPT"}} {
			after, bare, byName := s.forCaller(caller)
			if len(after) != len(bare) {
				t.Fatal("tool inventory shape changed")
			}
			for i, tool := range after {
				name := tool.Name
				if name != "storage_set_resource" && name != "storage_list_resources" && name != "storage_sql_query" && name != "storage_sql_schema" {
					continue
				}
				if bare[i].Description != tool.Description || byName[tool.Name].Description != tool.Description {
					t.Errorf("stale variant: %s", tool.Name)
				}
				if name == "storage_sql_schema" && !strings.Contains(tool.Description, "Do not declare visitor_id: the server ensures the indexed author column when rules reference visitor_id or allow ordinary visitors to add.") {
					t.Errorf("%s lacks conditional author-column guidance", tool.Name)
				}
				// The rules wording teaches no preset menu.
				dataText := strings.Split(tool.Description, "For kind: \"files\"")[0]
				for _, choice := range []string{"preset public", "preset private", "preset records", "preset personal", "preset inbox", "preset wall", "preset board", "The presets (", "whatever the preset", "whose preset"} {
					if strings.Contains(dataText, choice) {
						t.Errorf("%s teaches %q", tool.Name, choice)
					}
				}
				if name == "storage_list_resources" {
					for _, phrase := range []string{"rules_status (rules or invalid)", "per-table rules only for overrides", "Uploads in a files resource are listed by"} {
						if !strings.Contains(tool.Description, phrase) {
							t.Errorf("%s lacks %q", tool.Name, phrase)
						}
					}
				}
				if name == "storage_set_resource" {
					for _, phrase := range []string{"Deny by default", "Clause table", "read | required | refused", "add | refused | required", "edit | required | optional, defaults to using", "delete | required | refused", "all | required", `For kind: "files"`, `File rules use path and visitor_id`, "Uploads are never spam-checked", "Presets were removed and their fields are refused", "For every resource kind, site_passcode applies alongside rules", "Omission defaults to inherit"} {
						if !strings.Contains(tool.Description, phrase) {
							t.Errorf("%s lacks %q", tool.Name, phrase)
						}
					}
					if !strings.Contains(tool.Description, "Create the resource with storage_set_resource, then create its tables with storage_sql_schema only for SQLite") {
						t.Errorf("incorrect resource creation instructions: %s", tool.Name)
					}
					count := 0
					for _, line := range strings.Split(tool.Description, "\n") {
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
					if count != 3 {
						t.Errorf("want three examples, got %d", count)
					}
				}
			}
			if text := s.instructionsFor(caller); !strings.Contains(text, "Deny by default") {
				t.Errorf("instructions lack rules for %+v", caller)
			}
			search := s.searchFor(caller)
			for _, entry := range search.tools {
				if entry.tool.Name == "storage_set_resource" && !strings.Contains(entry.tool.Description, "Deny by default") {
					t.Error("stale discovery text")
				}
			}
			for _, code := range []string{"invalid_rules", "rules_invalid", "query_too_expensive", "storage_failed"} {
				if codeHint(code) == "" {
					t.Errorf("missing rule hint: %s", code)
				}
			}
		}
	}
}

func TestStorageRulesInstructionsMaxHostname(t *testing.T) {
	defer NewServer(testConfig(Config{}))
	host := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61)
	for _, noSpam := range []bool{false, true} {
		s := NewServer(testConfig(Config{SelfHosted: true, NoSpamCheck: noSpam, Rewrite: productionHostRewrite(host)}))
		for _, app := range []string{"", "ChatGPT", "OpenAI", "Codex"} {
			text := s.instructionsFor(Caller{AppName: app})
			if n := len([]rune(text)); n >= 2000 {
				t.Errorf("app=%q noSpam=%t: instructions too long: %d", app, noSpam, n)
			}
		}
	}
}

func TestStorageRulesRefusedResourceHints(t *testing.T) {
	// The site tool here; a product's copy of it is tested with the product.
	for _, event := range []bool{false} {
		for _, kind := range []string{"files", "sqlite", "kv"} {
			for _, code := range []string{"invalid_resource", "storage_busy", "invalid_rules"} {
				t.Run(fmt.Sprintf("event=%t/%s/%s", event, kind, code), func(t *testing.T) {
					tool, path := "storage_set_resource", "/v1/sites/test/storage/resources/data"
					caller := Caller{APIKey: "test-key"}
					args := map[string]any{"site": "test", "name": "data", "body": map[string]any{"kind": kind, "site_passcode": "invalid"}}
					if code == "invalid_rules" {
						args["body"] = map[string]any{"kind": kind, "rules": map[string]any{"version": 1, "policies": []any{}}}
					}
					s := newTestServer(&recordingUpstream{answers: map[string]func() (int, string){
						"PUT " + path: func() (int, string) {
							return 400, fmt.Sprintf(`{"error":"settings refused","code":%q}`, code)
						},
					}})
					req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCall(tool, args)))
					req = req.WithContext(WithCaller(req.Context(), caller))
					rec := httptest.NewRecorder()
					s.ServeHTTP(rec, req)
					text, _, refused := resultOf(t, rec)
					if !refused || !strings.Contains(text, "["+code+"]") {
						t.Fatalf("expected storage refusal: %s", text)
					}
					if hint, ok := storageRulesCodeHints[code]; ok && !strings.Contains(text, hint) {
						t.Fatalf("missing data rule hint: %s", text)
					}
				})
			}
		}
	}
}

func TestStorageRulesValidationRefusal(t *testing.T) {
	s := newTestServer(&recordingUpstream{answers: map[string]func() (int, string){
		"PUT /v1/sites/test/storage/resources/data": func() (int, string) {
			return 400, `{"error":"unknown binding auth.rol","code":"invalid_rules","rule_error":{"policy":"read","clause":"using","position":0,"message":"unknown binding auth.rol"}}`
		},
	}})
	rec := send(t, s, toolCall("storage_set_resource", map[string]any{
		"site": "test", "name": "data", "body": map[string]any{"kind": "sqlite", "rules": map[string]any{"version": 1, "policies": []any{}}},
	}), nil, true)
	text, _, refused := resultOf(t, rec)
	want := "storage_set_resource failed (HTTP 400): unknown binding auth.rol [invalid_rules]\nThe rules save was refused. Correct the rules using the error message above, then save again."
	if !refused || text != want {
		t.Fatalf("validation refusal:\ngot %s\nwant %s", text, want)
	}
}

func TestStorageRulesDiscoveryIndexesDescriptions(t *testing.T) {
	s := NewServer(testConfig(Config{}))
	for _, caller := range []Caller{{}, {DeployOnly: true}, {AppName: "ChatGPT"}, {Mode: CallerModeEvents}} {
		after := s.searchFor(caller)
		for _, query := range []string{"restrictive", "permissive"} {
			out := after.find(query, 1)
			items := out.Structured["tools"].([]any)
			if len(items) != 1 {
				t.Fatalf("%s returned catalogue: %s", query, out.Text)
			}
			item := items[0].(map[string]any)
			if item["name"] != "storage_set_resource" || !strings.Contains(fmt.Sprint(item["description"]), "Deny by default") {
				t.Fatalf("%s did not find rules tool: %s", query, out.Text)
			}
		}
	}
}
