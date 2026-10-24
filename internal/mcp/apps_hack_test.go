package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackAppsGatesAndUnchangedInventory(t *testing.T) {
	s := newTestServer(nil)
	for _, caller := range []Caller{
		{Mode: CallerModeEvents},
		{Mode: CallerModeEvents, GrantID: "grant", Handle: "alice"},
		{Mode: CallerModeHackSite, Handle: "alice"},
	} {
		// Cached inventories are the original, undecorated tools/list contract.
		t.Setenv("MCP_APP_MY_SITES", "off")
		t.Setenv("MCP_APP_DASHBOARD", "off")
		full, bare, _ := s.forCaller(caller)
		baseline, _ := json.Marshal([][]Tool{full, bare})
		for _, flag := range []string{"", "off", "unknown", "accounts:bob", "accounts:alice", "on"} {
			t.Setenv("MCP_APP_MY_SITES", flag)
			for _, dashboard := range []string{"off", "on"} {
				t.Setenv("MCP_APP_DASHBOARD", dashboard)
				// These must never expose the archive link or a preview to a Hack frame.
				t.Setenv("MCP_APP_PHOTO_PICKER", "on")
				t.Setenv("MCP_APP_SITE_PREVIEW", "on")
				enabled := (caller.Mode == CallerModeHackSite || caller.GrantID != "") && appFlag(flag, caller.Handle)
				full, bare, _ := s.forCaller(caller)
				if !enabled {
					got, _ := json.Marshal([][]Tool{full, bare})
					if string(got) != string(baseline) {
						t.Fatal("disabled tools/list changed")
					}
				}
				for _, list := range [][]Tool{full, bare} {
					for _, tool := range list {
						if (tool.Meta["ui"] != nil) != (enabled && tool.Name == "list_sites") {
							t.Fatalf("unexpected UI: %s", tool.Name)
						}
					}
				}
				count := 0
				if enabled {
					count++
					if dashboard == "on" {
						count++
					}
				}
				if len(s.appResources(caller)) != count {
					t.Fatal("resource gate")
				}
				for _, screen := range appScreens {
					params, _ := json.Marshal(map[string]string{"uri": screen.URI})
					response := s.readApp(request{ID: json.RawMessage(`1`), Params: params}, caller)
					allowed := enabled && (screen.ID == "my-sites" || (screen.ID == "dashboard" && dashboard == "on"))
					if (response.Error == nil) != allowed {
						t.Fatalf("read gate: %s", screen.ID)
					}
					if allowed {
						content := response.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
						page := content["text"].(string)
						if !strings.Contains(page, "Simple Hack") || !strings.Contains(page, "SHHackSites") || strings.Contains(page, "__HACK__") {
							t.Fatal("Hack resource not rendered")
						}
						csp := content["_meta"].(map[string]any)["ui"].(map[string]any)["csp"].(map[string][]string)
						for _, domains := range csp {
							if len(domains) != 0 {
								t.Fatal("Hack screen has external origins")
							}
						}
					}
				}
			}
		}
	}
}

func TestHackAppsResultsOffAreByteIdentical(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"team-site","site_url":"https://team.event.example/","updated_at":"2026-10-24T10:00:00Z"}]`))
	})
	mux.HandleFunc("GET /v1/sites/team-site/analytics", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"range_days":7,"totals":{},"last_24h":{},"daily":[]}`))
	})
	s := newTestServer(mux)
	for _, mode := range []string{CallerModeEvents, CallerModeHackSite} {
		c := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{Mode: mode, GrantID: "grant", Handle: "alice", APIKey: "fixture"}}
		for _, name := range []string{"list_sites", "site_analytics"} {
			t.Setenv("MCP_APP_MY_SITES", "off")
			t.Setenv("MCP_APP_DASHBOARD", "off")
			baseline, err := s.byName[name].run(c, map[string]any{"site": "team-site"})
			if err != nil {
				t.Fatal(err)
			}
			original, _ := json.Marshal(baseline)
			for _, flag := range []string{"on", "", "off", "accounts:bob"} {
				t.Setenv("MCP_APP_MY_SITES", flag)
				t.Setenv("MCP_APP_DASHBOARD", "on")
				got, err := s.byName[name].run(c, map[string]any{"site": "team-site"})
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(got)
				if flag != "on" && string(encoded) != string(original) {
					t.Fatalf("%s disabled result changed", name)
				}
				if got.Text != baseline.Text {
					t.Fatalf("%s text changed", name)
				}
			}
		}
	}
}

func TestHackAppsUnselectedListNeverReplaysPersonalCredential(t *testing.T) {
	requests := 0
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(403) }))
	caller := Caller{Mode: CallerModeEvents, GrantID: "grant", APIKey: "personal-fixture"}
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("MCP-Protocol-Version", "2025-06-18")
	req := request{ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"list_sites","arguments":{}}`)}
	t.Setenv("MCP_APP_MY_SITES", "off")
	original, _ := json.Marshal(s.dispatch(r, req, caller))
	for _, flag := range []string{"on", "off", "unknown", ""} {
		t.Setenv("MCP_APP_MY_SITES", flag)
		got, _ := json.Marshal(s.dispatch(r, req, caller))
		if flag == "on" {
			if strings.Contains(string(got), `"isError":true`) || !strings.Contains(string(got), `"sites":[]`) {
				t.Fatal("unselected App entry point failed")
			}
		} else if string(got) != string(original) {
			t.Fatal("unselected flags-off result changed")
		}
	}
	if requests != 0 {
		t.Fatal("unselected list replayed a site request")
	}
}
