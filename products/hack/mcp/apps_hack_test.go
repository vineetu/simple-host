package hackmcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// The tool server's own screens (My sites and the rest), as one of Simple
// Hack's connections meets them. Everything here goes over the wire.

// platformScreenURIs are the tool server's own screens.
var platformScreenURIs = map[string]string{
	"passcode": "ui://simple-host/passcode", "dashboard": "ui://simple-host/dashboard", "my-sites": "ui://simple-host/my-sites",
	"photo-picker": "ui://simple-host/photo-picker", "site-preview": "ui://simple-host/site-preview",
}

// listingBytes is tools/list as this caller gets it, with and without output
// schemas (the oldest revision has none).
func listingBytes(t *testing.T, s *mcp.Server, caller mcp.Caller) string {
	t.Helper()
	full := rpcAs(t, s, caller, "2025-06-18", "tools/list", nil, false)
	bare := rpcAs(t, s, caller, "2025-03-26", "tools/list", nil, false)
	if full.Code != http.StatusOK || bare.Code != http.StatusOK {
		t.Fatalf("tools/list: %d %d", full.Code, bare.Code)
	}
	return full.Body.String() + bare.Body.String()
}

func TestHackAppsGatesAndUnchangedInventory(t *testing.T) {
	s := newTestServer(nil)
	for _, caller := range []mcp.Caller{
		{Mode: mcp.CallerModeEvents, APIKey: "fixture"},
		{Mode: mcp.CallerModeEvents, GrantID: "grant", Handle: "alice", APIKey: "fixture"},
		{Mode: mcp.CallerModeGroupSite, Handle: "alice", APIKey: "fixture"},
	} {
		// The listing with the screens off is the original, undecorated tools/list contract.
		t.Setenv("MCP_APP_MY_SITES", "off")
		t.Setenv("MCP_APP_DASHBOARD", "off")
		baseline := listingBytes(t, s, caller)
		for _, flag := range []string{"", "off", "unknown", "accounts:bob", "accounts:alice", "on"} {
			t.Setenv("MCP_APP_MY_SITES", flag)
			for _, dashboard := range []string{"off", "on"} {
				t.Setenv("MCP_APP_DASHBOARD", dashboard)
				// These must never expose the archive link or a preview to a Hack frame.
				t.Setenv("MCP_APP_PHOTO_PICKER", "on")
				t.Setenv("MCP_APP_SITE_PREVIEW", "on")
				enabled := (caller.Mode == mcp.CallerModeGroupSite || caller.GrantID != "") && mcp.AppFlag(flag, caller.Handle)
				if !enabled && listingBytes(t, s, caller) != baseline {
					t.Fatal("disabled tools/list changed")
				}
				for _, version := range []string{"2025-06-18", "2025-03-26"} {
					var listed struct {
						Result struct {
							Tools []wireTool `json:"tools"`
						} `json:"result"`
					}
					if err := json.Unmarshal(rpcAs(t, s, caller, version, "tools/list", nil, false).Body.Bytes(), &listed); err != nil {
						t.Fatal(err)
					}
					for _, tool := range listed.Result.Tools {
						// Only the screen's own resourceUri decoration is what
						// this loop guards -- never attached outside list_sites
						// here. A hack_app_* twin's _meta.ui (SIM-344 phase 2,
						// Grok security review) is a different, unconditional
						// field (visibility, never resourceUri) under the same
						// "ui" key, so it is not what this invariant is about.
						ui, _ := tool.Meta["ui"].(map[string]any)
						if (ui["resourceUri"] != nil) != (enabled && tool.Name == "list_sites") {
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
				resources, _ := decode(t, rpcAs(t, s, caller, "2025-06-18", "resources/list", nil, false))["result"].(map[string]any)["resources"].([]any)
				if len(resources) != count {
					t.Fatalf("resource gate: %d listed, want %d", len(resources), count)
				}
				for id, uri := range platformScreenURIs {
					reply := decode(t, rpcAs(t, s, caller, "2025-06-18", "resources/read", map[string]any{"uri": uri}, false))
					allowed := enabled && (id == "my-sites" || (id == "dashboard" && dashboard == "on"))
					if (reply["error"] == nil) != allowed {
						t.Fatalf("read gate: %s", id)
					}
					if allowed {
						content := reply["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
						page := content["text"].(string)
						if !strings.Contains(page, "Simple Hack") || !strings.Contains(page, "SHHackSites") || strings.Contains(page, "__HACK__") {
							t.Fatal("Hack resource not rendered")
						}
						csp := content["_meta"].(map[string]any)["ui"].(map[string]any)["csp"].(map[string]any)
						if len(csp) == 0 {
							t.Fatal("Hack screen has no content policy")
						}
						for _, domains := range csp {
							if len(domains.([]any)) != 0 {
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
	for _, mode := range []string{mcp.CallerModeEvents, mcp.CallerModeGroupSite} {
		// A personal connection reaches the website tools through its selected team.
		caller := mcp.Caller{Mode: mode, GrantID: "grant", Handle: "alice", APIKey: "fixture", TeamAPIKey: "team-fixture"}
		for _, name := range []string{"list_sites", "site_analytics"} {
			args := map[string]any{}
			if name == "site_analytics" {
				args["site"] = "team-site"
			}
			t.Setenv("MCP_APP_MY_SITES", "off")
			t.Setenv("MCP_APP_DASHBOARD", "off")
			baseline := callAs(t, s, caller, name, args)
			baselineText, _, failed := resultOf(t, baseline)
			if failed {
				t.Fatalf("%s %s: %s", mode, name, baselineText)
			}
			for _, flag := range []string{"on", "", "off", "accounts:bob"} {
				t.Setenv("MCP_APP_MY_SITES", flag)
				t.Setenv("MCP_APP_DASHBOARD", "on")
				got := callAs(t, s, caller, name, args)
				if flag != "on" && got.Body.String() != baseline.Body.String() {
					t.Fatalf("%s disabled result changed", name)
				}
				if text, _, failed := resultOf(t, got); failed || text != baselineText {
					t.Fatalf("%s text changed", name)
				}
			}
		}
	}
}

func TestHackAppsUnselectedListNeverReplaysPersonalCredential(t *testing.T) {
	requests := 0
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(403) }))
	caller := mcp.Caller{Mode: mcp.CallerModeEvents, GrantID: "grant", APIKey: "personal-fixture"}
	t.Setenv("MCP_APP_MY_SITES", "off")
	original := callAs(t, s, caller, "list_sites", nil).Body.String()
	for _, flag := range []string{"on", "off", "unknown", ""} {
		t.Setenv("MCP_APP_MY_SITES", flag)
		got := callAs(t, s, caller, "list_sites", nil).Body.String()
		if flag == "on" {
			if strings.Contains(got, `"isError":true`) || !strings.Contains(got, `"sites":[]`) {
				t.Fatal("unselected App entry point failed")
			}
		} else if got != original {
			t.Fatal("unselected flags-off result changed")
		}
	}
	if requests != 0 {
		t.Fatal("unselected list replayed a site request")
	}
}

// The two answers a personal connection with no team selected gets, word for
// word (Contribution.NoTarget).
func TestHackNoTeamSelectedAnswers(t *testing.T) {
	s := newTestServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("no-team call reached REST") }))
	caller := mcp.Caller{Mode: mcp.CallerModeEvents, GrantID: "grant", APIKey: "person", Handle: "fixture"}
	for _, tc := range []struct {
		name, flag, tool string
		args             map[string]any
		listable         bool
	}{
		{name: "listable", flag: "on", tool: "list_sites", listable: true},
		{name: "screen off", flag: "off", tool: "list_sites"},
		{name: "other tool", flag: "on", tool: "get_site", args: map[string]any{"site": "fixture"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_MY_SITES", tc.flag)
			text, structured, failed := resultOf(t, callAs(t, s, caller, tc.tool, tc.args))
			if tc.listable {
				if failed || text != "No team selected. Call hack_get_my_teams to choose a team, or hack_list_events for your event websites." || len(structured["sites"].([]any)) != 0 || structured["count"] != float64(0) {
					t.Fatalf("listable answer: %q %v failed=%t", text, structured, failed)
				}
				return
			}
			if !failed || structured != nil || text != "Choose a team with hack_select_team before publishing its site." {
				t.Fatalf("answer: %q %v failed=%t", text, structured, failed)
			}
		})
	}
}
