package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestFindToolsRanking(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	cases := []struct{ query, want string }{
		{"put my site on my own domain", "connect_domain"},
		{"shorter link for my site", "connect_domain"},
		{"password protect my site", "set_site_passcode"},
		{"undo my last change", "rollback_site"},
		{"how many visitors this week", "site_analytics"},
		{"webhook when someone books", "list_notifications"},
		{"move my wordpress site", "create_import_link"},
		{"download a backup of my site", "export_site"},
		{"hide my site from my public page", "set_visibility"},
		{"take my site offline", "set_site_offline"},
		{"bring back a deleted site", "restore_site"},
		{"who can read my saved data", "storage_list_resources"},
		{"how much storage space is left", "storage_get_usage"},
		{"photos visitors uploaded", "storage_list_file_objects"},
		{"rename my site", "rename_site"},
		{"remove the passcode", "remove_site_passcode"},
		{"remove a viewer", "revoke_site_viewer"},
		{"set my home page", "set_home_page"},
		{"preview an older version", "preview_version"},
		{"check my domain dns", "domain_status"},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := s.search.rank(tc.query, 5)
			names := []string{}
			for _, match := range got {
				names = append(names, match.tool.Name)
			}
			if len(got) == 0 || got[0].tool.Name != tc.want {
				t.Fatalf("got %v, want %s first", names, tc.want)
			}
		})
	}
	for _, tool := range s.tools {
		if got := s.search.rank(tool.Name, 1); len(got) != 1 || got[0].tool.Name != tool.Name {
			t.Errorf("exact name %s did not rank first", tool.Name)
		}
		if !slices.Contains(coreToolNames, tool.Name) && len(toolAliases[tool.Name]) < 2 {
			t.Errorf("%s needs aliases", tool.Name)
		}
	}
}

func TestFindToolsTiesKeepInventoryOrder(t *testing.T) {
	search := newToolSearch([]Tool{{Name: "first", Description: "example"}, {Name: "second", Description: "example"}})
	matches := search.rank("example", 2)
	if len(matches) != 2 || matches[0].tool.Name != "first" || matches[1].tool.Name != "second" {
		t.Fatal("equal scores did not preserve inventory order")
	}
}

func TestCoreListingByRevision(t *testing.T) {
	for _, events := range []bool{false, true} {
		cfg := Config{}
		if events {
			cfg.Events = &fakeEventsBackend{}
		}
		s := NewServer(cfg)
		for _, deploy := range []bool{false, true} {
			for _, version := range append([]string{""}, supportedVersions...) {
				t.Run(fmt.Sprintf("events=%v/deploy=%v/%s", events, deploy, version), func(t *testing.T) {
					caller := Caller{APIKey: "test", DeployOnly: deploy}
					req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}}}`))
					req = req.WithContext(WithCaller(req.Context(), caller))
					req.Header.Set("MCP-Protocol-Version", version)
					if version == protocolVersion {
						req.Header.Set("Mcp-Method", "tools/list")
					}
					rec := httptest.NewRecorder()
					s.ServeHTTP(rec, req)
					res, ok := decode(t, rec)["result"].(map[string]any)
					if !ok {
						t.Fatalf("listing failed: %s", rec.Body.String())
					}
					names := []string{}
					for _, raw := range res["tools"].([]any) {
						tool := raw.(map[string]any)
						name := tool["name"].(string)
						names = append(names, name)
						_, schema := tool["outputSchema"]
						if schema != (version != "2025-03-26" && name != "run_tool") {
							t.Errorf("%s outputSchema = %v", name, schema)
						}
						ann := tool["annotations"].(map[string]any)
						if ann["title"] != tool["title"] {
							t.Errorf("%s annotation title differs", name)
						}
						if name == "run_tool" && (ann["readOnlyHint"] != false || ann["destructiveHint"] != true) {
							t.Error("unsafe run_tool hints")
						}
					}
					if !reflect.DeepEqual(names, coreToolNames) || len(names) != 20 {
						t.Fatalf("listed %v", names)
					}
					if version == protocolVersion && res["resultType"] != "complete" {
						t.Error("missing modern resultType")
					}
					_, _, byName := s.forCaller(caller)
					want := len(s.tools) + 2
					if deploy && events {
						want -= 2
					}
					if len(byName) != want {
						t.Fatalf("inventory has %d, want %d", len(byName), want)
					}
				})
			}
		}
	}
}

func TestFindToolsResults(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	for _, query := range []string{"", "zzzzzz", "connect_domain", "create_site"} {
		text, structured, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": query}), nil, true))
		if isErr {
			t.Fatal(text)
		}
		if err := ValidateOutput(s.byName["find_tools"].OutputSchema, structured, nil); err != nil {
			t.Fatal(err)
		}
		items := structured["tools"].([]any)
		if query == "" || query == "zzzzzz" {
			expected := []string{}
			for _, tool := range s.tools {
				if !slices.Contains(coreToolNames, tool.Name) {
					expected = append(expected, tool.Name)
				}
			}
			names := []string{}
			for _, raw := range items {
				item := raw.(map[string]any)
				if len(item) != 2 {
					t.Fatalf("catalog has more than names and titles: %v", item)
				}
				names = append(names, item["name"].(string))
			}
			if !reflect.DeepEqual(names, expected) {
				t.Fatalf("catalog order: %v", names)
			}
			if query != "" && !strings.Contains(text, "Nothing matched") {
				t.Error("no-match explanation missing")
			}
		} else {
			item := items[0].(map[string]any)
			if item["name"] != query || item["listed"] != (query == "create_site") || item["arguments"] == nil || item["description"] == nil || item["outputSchema"] != nil {
				t.Fatalf("bad match: %v", item)
			}
			if len(items) > 3 {
				t.Error("default limit ignored")
			}
		}
	}
	text, _, _ := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "password protect my site"}), nil, true))
	if !strings.Contains(text, "set_site_passcode (run_tool)") || !strings.Contains(text, "passcode (string, required)") {
		t.Errorf("passcode tool text lacks name or required argument: %s", text)
	}
	for _, limit := range []any{0, 11, 1.5, "5", nil} {
		_, _, isErr := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "site", "limit": limit}), nil, true))
		if !isErr {
			t.Errorf("accepted limit %v", limit)
		}
	}
	_, out, _ := resultOf(t, send(t, s, toolCall("find_tools", map[string]any{"query": "site", "limit": 1}), nil, true))
	if len(out["tools"].([]any)) != 1 {
		t.Error("limit ignored")
	}
	if !reflect.DeepEqual(searchTerms("uploads publishing changed settings"), searchTerms("upload publish change setting")) {
		t.Error("stemming differs")
	}
}

func TestRunToolDispatch(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/versions": func() (int, string) { return 200, `[{"version":1}]` },
	}}
	s := newTestServer(up)
	for _, modern := range []bool{false, true} {
		caller := Caller{APIKey: "test-key"}
		direct := sendEvent(t, s, "tools/call", map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}, caller, modern)
		wrapped := sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}}, caller, modern)
		text, _, isErr := resultOf(t, wrapped)
		if isErr || !reflect.DeepEqual(decode(t, direct), decode(t, wrapped)) {
			t.Fatalf("wrapped differs: %s / %s", text, direct.Body.String())
		}
	}
	for _, tc := range []struct {
		args   map[string]any
		want   string
		schema bool
	}{
		{map[string]any{"name": "run_tool"}, "cannot call", false},
		{map[string]any{"name": "find_tools"}, "cannot call", false},
		{map[string]any{"name": "rename_sit"}, "rename_site", false},
		{map[string]any{"name": "list_versions", "arguments": []any{}}, "arguments must be an object", true},
		{map[string]any{"name": "list_versions", "arguments": nil}, "arguments must be an object", true},
		{map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog", "extra": true}}, "unexpected argument", true},
		{map[string]any{"name": "list_versions"}, "site is required", true},
		{map[string]any{"name": "list_versions", "arguments": map[string]any{"site": 12}}, "must be a string", true},
		{map[string]any{"name": "get_state"}, "no longer offered", false},
	} {
		text, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", tc.args), nil, true))
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("%v: %s", tc.args, text)
		}
		if tc.schema {
			schema, _ := json.Marshal(s.byName["list_versions"].InputSchema)
			if !strings.Contains(text, string(schema)) {
				t.Errorf("missing compact schema: %s", text)
			}
		}
		if tc.args["name"] == "rename_sit" && !strings.Contains(text, "find_tools") {
			t.Error("missing find_tools advice")
		}
	}
	s.byName["no_args"] = Tool{Name: "no_args", InputSchema: noArgs(), run: func(_ *call, args map[string]any) (output, error) {
		if len(args) != 0 {
			t.Error("omitted arguments not empty")
		}
		return output{Text: "ok"}, nil
	}}
	text, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", map[string]any{"name": "no_args"}), nil, true))
	if isErr || text != "ok" {
		t.Fatalf("omitted arguments: %s", text)
	}
}

func TestDiscoveryCallerInventories(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	caller := Caller{APIKey: "test", DeployOnly: true}
	for _, name := range []string{"list_notifications", "remove_notification"} {
		text, _, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": name}}, caller, false))
		if !isErr || !strings.Contains(text, "Unknown tool") {
			t.Fatalf("deploy call: %s", text)
		}
	}
	for _, query := range []string{"", "webhook when someone books", "remove_notification"} {
		_, structured, isErr := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "find_tools", "arguments": map[string]any{"query": query}}, caller, false))
		if isErr {
			t.Fatal("deploy search failed")
		}
		for _, raw := range structured["tools"].([]any) {
			name := raw.(map[string]any)["name"]
			if name == "list_notifications" || name == "remove_notification" {
				t.Error("deploy search leaked notifications")
			}
		}
	}
	for _, caller := range []Caller{{Mode: CallerModeEvents}, {Mode: CallerModeEvents, GrantID: "grant"}, {Mode: CallerModeHackSite}} {
		full, bare, byName := s.forCaller(caller)
		expected := append([]Tool{}, s.hackTools...)
		if caller.Mode == CallerModeHackSite {
			expected = s.hackSiteTools
		} else if caller.GrantID != "" {
			for _, tool := range s.hackSiteTools {
				if tool.Name != "get_page_recipe" {
					expected = append(expected, tool)
				}
			}
		}
		if len(full) != len(expected) || len(bare) != len(expected) {
			t.Fatal("Hack inventory count changed")
		}
		for i, tool := range expected {
			if full[i].Name != tool.Name || bare[i].Name != tool.Name {
				t.Fatal("Hack order changed")
			}
		}
		for _, name := range []string{"run_tool", "find_tools"} {
			if _, ok := byName[name]; ok {
				t.Errorf("Hack has %s", name)
			}
			caller.APIKey = "test"
			eventErrorCode(t, sendEvent(t, s, "tools/call", map[string]any{"name": name, "arguments": map[string]any{"name": "list_notifications", "query": "webhook"}}, caller, false), codeInvalidParams)
		}
	}
	brain := NewBrainServer(BrainConfig{Query: func(string, map[string]any) (string, map[string]any, error) { return "", nil, nil }})
	full, _, byName := brain.forCaller(Caller{})
	if len(full) != 7 || byName["run_tool"].Name != "" || byName["find_tools"].Name != "" {
		t.Error("brain inventory changed")
	}
}

func TestInstructionsLength(t *testing.T) {
	defer NewServer(Config{})
	for _, tc := range []struct {
		name string
		cfg  Config
	}{
		{"hosted", Config{}}, {"NoSpamCheck", Config{NoSpamCheck: true}}, {"SelfHosted", Config{SelfHosted: true}},
		{"other instance", Config{SelfHosted: true, NoSpamCheck: true, Rewrite: strings.NewReplacer("simple-host.app", "websites.example.org").Replace, SupportContact: "support@example.org"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := NewServer(tc.cfg).instructionsFor(Caller{})
			t.Logf("instructions: %d characters (%d bytes)", len([]rune(text)), len(text))
			if len(text) >= 2000 {
				t.Fatalf("instructions too long: %d", len(text))
			}
			if strings.Contains(text, "{claude_reconnect}") || strings.Contains(text, "{deleted_retention}") {
				t.Error("unfilled placeholder")
			}
			if strings.Contains(text, "comments, wall,") == tc.cfg.NoSpamCheck {
				t.Error("comments topic does not match configuration")
			}
		})
	}
}

func TestSignedOutReconnect(t *testing.T) {
	defer NewServer(Config{})
	for _, self := range []bool{false, true} {
		for _, code := range []string{"key_expired", "key_expired_idle", "invalid_api_key", ""} {
			up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/me": func() (int, string) { return 401, `{"error":"connection expired","code":"` + code + `"}` }}}
			cfg := Config{Upstream: up, SelfHosted: self}
			if self {
				cfg.Rewrite = strings.NewReplacer("simple-host.app", "box.example").Replace
			}
			s := NewServer(cfg)
			text, _, isErr := resultOf(t, send(t, s, toolCall("who_am_i", nil), nil, true))
			for _, want := range []string{"connection expired", "https://chatgpt.com/plugins", "Add custom MCP server", "never share an API key"} {
				if !isErr || !strings.Contains(text, want) {
					t.Errorf("%v/%s lacks %q: %s", self, code, want, text)
				}
			}
			if self {
				if strings.Contains(text, "simple-host.app") || !strings.Contains(text, "Add custom connector") || !strings.Contains(text, "https://box.example/mcp") {
					t.Errorf("wrong self reconnect: %s", text)
				}
			} else if !strings.Contains(text, claudeReconnectHosted) {
				t.Error("missing hosted Claude reconnect")
			}
		}
	}
}
