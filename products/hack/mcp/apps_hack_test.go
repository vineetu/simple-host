package hackmcp

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// The tool server's own screens (My sites and the rest), as one of Simple
// Hack's connections meets them, over the wire.

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

// A Hack connection gets My sites (and the dashboard) only on a personal
// connection whose flag is on for it, never the photo picker or site preview,
// and with the screens off its tools/list is unchanged.
func TestHackAppsGatesAndUnchangedInventory(t *testing.T) {
	s := newTestServer(nil)
	for _, caller := range []mcp.Caller{
		{Mode: mcp.CallerModeEvents, APIKey: "fixture"},
		{Mode: mcp.CallerModeEvents, GrantID: "grant", Handle: "alice", APIKey: "fixture"},
	} {
		t.Setenv("MCP_APP_MY_SITES", "off")
		t.Setenv("MCP_APP_DASHBOARD", "off")
		baseline := listingBytes(t, s, caller)
		t.Setenv("MCP_APP_PHOTO_PICKER", "on")
		t.Setenv("MCP_APP_SITE_PREVIEW", "on")
		for _, flag := range []string{"off", "accounts:bob", "on"} {
			t.Setenv("MCP_APP_MY_SITES", flag)
			for _, dashboard := range []string{"off", "on"} {
				t.Setenv("MCP_APP_DASHBOARD", dashboard)
				enabled := caller.GrantID != "" && mcp.AppFlag(flag, caller.Handle)
				if !enabled && listingBytes(t, s, caller) != baseline {
					t.Fatal("disabled tools/list changed")
				}
				var listed struct {
					Result struct {
						Tools []wireTool `json:"tools"`
					} `json:"result"`
				}
				if err := json.Unmarshal(rpcAs(t, s, caller, "2025-06-18", "tools/list", nil, false).Body.Bytes(), &listed); err != nil {
					t.Fatal(err)
				}
				for _, tool := range listed.Result.Tools {
					ui, _ := tool.Meta["ui"].(map[string]any)
					if (ui["resourceUri"] != nil) != (enabled && tool.Name == "list_sites") {
						t.Fatalf("unexpected UI: %s", tool.Name)
					}
				}
				for id, uri := range platformScreenURIs {
					reply := decode(t, rpcAs(t, s, caller, "2025-06-18", "resources/read", map[string]any{"uri": uri}, false))
					allowed := enabled && (id == "my-sites" || (id == "dashboard" && dashboard == "on"))
					if (reply["error"] == nil) != allowed {
						t.Fatalf("read gate: %s flag=%s dashboard=%s", id, flag, dashboard)
					}
				}
			}
		}
	}
}
