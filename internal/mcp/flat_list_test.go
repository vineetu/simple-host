package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestFlatListApp(t *testing.T) {
	for _, name := range []string{"ChatGPT", "OPENAI", "Codex CLI", "My chatgpt connector", "OpenAI Codex"} {
		if !flatListApp(name) {
			t.Errorf("did not match %q", name)
		}
	}
	for _, name := range []string{"", "Claude", "Claude Code", "Grok", "code"} {
		if flatListApp(name) {
			t.Errorf("matched %q", name)
		}
	}
}

func TestFlatHostList(t *testing.T) {
	for _, flag := range []string{"off", "on"} {
		for _, key := range []string{"MCP_APP_PHOTO_PICKER", "MCP_APP_MY_SITES", "MCP_APP_SITE_PREVIEW", "MCP_APP_DASHBOARD"} {
			t.Setenv(key, flag)
		}
		s := NewServer(Config{Events: &fakeEventsBackend{}})
		for _, deploy := range []bool{false, true} {
			for _, version := range append([]string{""}, supportedVersions...) {
				for _, app := range []string{"ChatGPT", "Claude"} {
					t.Run(fmt.Sprintf("%s/%s/apps=%s/deploy=%v", app, version, flag, deploy), func(t *testing.T) {
						caller := Caller{AppName: app, DeployOnly: deploy, APIKey: "test"}
						r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}}}`))
						r = r.WithContext(WithCaller(r.Context(), caller))
						r.Header.Set("MCP-Protocol-Version", version)
						if version == protocolVersion {
							r.Header.Set("Mcp-Method", "tools/list")
						}
						rec := httptest.NewRecorder()
						s.ServeHTTP(rec, r)
						res, ok := decode(t, rec)["result"].(map[string]any)
						if !ok {
							t.Fatalf("listing failed: %s", rec.Body.String())
						}
						expected := s.coreTools
						if app == "ChatGPT" {
							expected = nil
							for _, tool := range s.tools {
								if deploy && (tool.Name == "list_notifications" || tool.Name == "remove_notification") {
									continue
								}
								expected = append(expected, flatListTool(tool))
							}
						}
						if version == "2025-03-26" {
							expected = append([]Tool(nil), expected...)
							for i := range expected {
								expected[i].OutputSchema = nil
							}
						}
						expected = appToolMetadata(expected, s.enabledScreens(caller))
						encoded, _ := json.Marshal(expected)
						var want any
						_ = json.Unmarshal(encoded, &want)
						if !reflect.DeepEqual(res["tools"], want) {
							t.Fatal("listing differs from authorized inventory, schemas, or Apps metadata")
						}
						for _, raw := range res["tools"].([]any) {
							tool := raw.(map[string]any)
							if app == "ChatGPT" && (tool["name"] == "find_tools" || tool["name"] == "run_tool") {
								t.Fatal("listed wrapper")
							}
							for _, key := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
								if _, ok := tool["annotations"].(map[string]any)[key].(bool); !ok {
									t.Errorf("%s lacks boolean %s", tool["name"], key)
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestFlatInstructionsAndDescriptionIsolation(t *testing.T) {
	for _, cfg := range []Config{{}, {NoSpamCheck: true}, {SelfHosted: true}, {NoSpamCheck: true, SelfHosted: true, Rewrite: productionHostRewrite(strings.Repeat("long", 40) + ".example")}} {
		s := NewServer(cfg)
		before, _, _ := s.forCaller(Caller{AppName: "Claude"})
		encodedBefore, _ := json.Marshal(before)
		for _, app := range []string{"ChatGPT", "OpenAI", "Codex"} {
			text := s.instructionsFor(Caller{AppName: app})
			for _, avoid := range []string{"find_tools", "run_tool", "unasked", "never mailto:", "offer to tell"} {
				if strings.Contains(text, avoid) {
					t.Errorf("instructions contain %q", avoid)
				}
			}
			if len([]rune(text)) >= 2000 || !strings.Contains(text, "listed individually") || !strings.Contains(text, "When they say yes") {
				t.Fatalf("incorrect instructions (%d characters): %s", len([]rune(text)), text)
			}
			full, _, _ := s.forCaller(Caller{AppName: app})
			for _, tool := range full {
				for _, avoid := range []string{"even before they mention photos", "After building a records form, offer", "never use a mailto:"} {
					if strings.Contains(tool.Description, avoid) {
						t.Errorf("%s contains %q", tool.Name, avoid)
					}
				}
				if tool.Name == "create_upload_link" && !strings.Contains(tool.Description, "when the person wants") {
					t.Error("missing photo intent")
				}
				if tool.Name == "get_page_recipe" && !strings.Contains(tool.Description, "If they ask to be told") {
					t.Error("missing notification intent")
				}
			}
		}
		after, _, _ := s.forCaller(Caller{AppName: "Claude"})
		encodedAfter, _ := json.Marshal(after)
		if string(encodedBefore) != string(encodedAfter) {
			t.Fatal("changed cached Claude descriptions")
		}
		if s.instructionsFor(Caller{AppName: "Claude"}) != instanceText(s.hostInstructions(Instructions())) {
			t.Fatal("changed Claude instructions")
		}
		for _, mode := range []string{CallerModeEvents, CallerModeHackSite} {
			before, _, _ := s.forCaller(Caller{Mode: mode})
			after, _, _ := s.forCaller(Caller{Mode: mode, AppName: "ChatGPT"})
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if string(a) != string(b) || s.instructionsFor(Caller{Mode: mode}) != s.instructionsFor(Caller{Mode: mode, AppName: "ChatGPT"}) {
				t.Fatal("changed Hack inventory or instructions")
			}
		}
	}
}

func TestFlatCachedCalls(t *testing.T) {
	s := newTestServer(&recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/versions": func() (int, string) { return 200, `[{"version":1}]` },
	}})
	caller := Caller{APIKey: "test", AppName: "ChatGPT"}
	for _, modern := range []bool{false, true} {
		direct := sendEvent(t, s, "tools/call", map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}, caller, modern)
		wrapped := sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}}, caller, modern)
		text, _, failed := resultOf(t, wrapped)
		if failed || !reflect.DeepEqual(decode(t, direct), decode(t, wrapped)) {
			t.Fatalf("cached wrapper failed: %s", text)
		}
	}
	for _, name := range []string{"missing", "find_tools", "run_tool"} {
		text, _, failed := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": name}}, caller, true))
		if !failed || !strings.Contains(text, "tool list") || strings.Contains(text, "Call find_tools") {
			t.Fatalf("bad discovery error: %s", text)
		}
	}
	for _, query := range []string{"", "connect_domain"} {
		text, out, failed := resultOf(t, sendEvent(t, s, "tools/call", map[string]any{"name": "find_tools", "arguments": map[string]any{"query": query}}, caller, true))
		if failed || strings.Contains(text, "run_tool") || strings.Contains(out["next"].(string), "run_tool") || len(out["tools"].([]any)) == 0 {
			t.Fatalf("bad cached discovery: %s", text)
		}
		if query != "" && out["tools"].([]any)[0].(map[string]any)["listed"] != true {
			t.Error("flat operation marked unlisted")
		}
	}
}
