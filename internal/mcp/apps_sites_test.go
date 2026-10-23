package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMySitesGates(t *testing.T) {
	s := newTestServer(nil)
	t.Setenv("MCP_APP_PHOTO_PICKER", "off")
	t.Setenv("MCP_APP_MY_SITES", "off")
	before, bare, _ := s.forCaller(Caller{})
	original, _ := json.Marshal(before)
	originalBare, _ := json.Marshal(bare)
	for _, sites := range []string{"", "off", "unknown", "on", "accounts:alice"} {
		for _, dashboard := range []string{"off", "on", "accounts:bob"} {
			t.Setenv("MCP_APP_MY_SITES", sites)
			t.Setenv("MCP_APP_DASHBOARD", dashboard)
			for _, handle := range []string{"alice", "bob", ""} {
				caller := Caller{Handle: handle}
				enabled := appFlag(sites, handle)
				full, bare, _ := s.forCaller(caller)
				if len(full) != 20 {
					t.Fatal("inventory grew")
				}
				for _, list := range [][]Tool{full, bare} {
					for _, tool := range list {
						if (tool.Meta["ui"] != nil) != (enabled && tool.Name == "list_sites") {
							t.Fatalf("metadata: %s", tool.Name)
						}
					}
				}
				if !enabled {
					got, _ := json.Marshal(full)
					gotBare, _ := json.Marshal(bare)
					if string(got) != string(original) || string(gotBare) != string(originalBare) {
						t.Fatal("off inventory changed")
					}
				}
				dashboardOn := enabled && appFlag(dashboard, handle)
				wantResources := 0
				if enabled {
					wantResources++
				}
				if dashboardOn {
					wantResources++
				}
				if len(s.appResources(caller)) != wantResources {
					t.Fatal("resource gate")
				}
				dashboardRead := s.readApp(request{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"uri":"ui://simple-host/dashboard"}`)}, caller)
				if (dashboardRead.Error == nil) != dashboardOn {
					t.Fatal("dashboard resource gate")
				}
				req := request{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"uri":"ui://simple-host/my-sites"}`)}
				response := s.readApp(req, caller)
				if !enabled {
					if response.Error == nil {
						t.Fatal("off read allowed")
					}
					continue
				}
				result := response.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
				if result["mimeType"] != appMIME {
					t.Fatal("mime")
				}
				page := result["text"].(string)
				if strings.Contains(page, `data-dashboard="true"`) != dashboardOn {
					t.Fatal("dashboard gate")
				}
				csp := result["_meta"].(map[string]any)["ui"].(map[string]any)["csp"].(map[string][]string)
				if len(csp["connectDomains"]) != 0 || len(csp["resourceDomains"]) != 0 || len(csp["frameDomains"]) != 0 {
					t.Fatal(csp)
				}
				if strings.Contains(page, "SHFrameURL") || strings.Contains(page, "_sh_preview") {
					t.Fatal("My sites includes the preview helper")
				}
				if dashboardOn {
					content := dashboardRead.Result.(map[string]any)["contents"].([]any)[0].(map[string]any)
					dashboardCSP := content["_meta"].(map[string]any)["ui"].(map[string]any)["csp"].(map[string][]string)
					if len(dashboardCSP["frameDomains"]) != 0 || content["text"] != page {
						t.Fatal("dashboard must share the tile-only My sites page")
					}
				}
			}
		}
	}
	for _, mode := range []string{CallerModeEvents, CallerModeHackSite} {
		if s.mySitesEnabled(Caller{Mode: mode}) || s.dashboardEnabled(Caller{Mode: mode}) {
			t.Fatal("Hack enabled")
		}
	}
}

func TestMySitesAndAnalyticsKeepText(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sites", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"shop","site_url":"https://shop.alice.simple-host.app/","visibility":"public","access":"specific","passcode_protected":false,"updated_at":"2026-10-23T10:00:00Z"}]`))
	})
	mux.HandleFunc("GET /v1/sites/shop/analytics", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"range_days":7,"totals":{},"last_24h":{},"daily":[{"day":"2026-10-23","person":{"visitors":2}}]}`))
	})
	s := newTestServer(mux)
	c := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "test", Handle: "alice"}}
	t.Setenv("MCP_APP_MY_SITES", "off")
	t.Setenv("MCP_APP_DASHBOARD", "on")
	for _, name := range []string{"list_sites", "site_analytics"} {
		t.Setenv("MCP_APP_MY_SITES", "off")
		off, err := s.byName[name].run(c, map[string]any{"site": "shop"})
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("MCP_APP_MY_SITES", "on")
		on, err := s.byName[name].run(c, map[string]any{"site": "shop"})
		if err != nil {
			t.Fatal(err)
		}
		if off.Text != on.Text {
			t.Fatalf("%s text changed", name)
		}
		if name == "list_sites" {
			item := on.Structured["sites"].([]any)[0].(map[string]any)
			if item["display"].(map[string]any)["access"] != "specific" {
				t.Fatal(item)
			}
			if strings.Contains(off.Text, "display") {
				t.Fatal("display leaked to text")
			}
			if err := ValidateOutput(mySitesOutputSchema(), on.Structured, nil); err != nil {
				t.Fatal(err)
			}
		} else if off.Meta != nil || on.Meta["simple-host/daily"] == nil {
			t.Fatal("daily metadata gate")
		}
	}
}
