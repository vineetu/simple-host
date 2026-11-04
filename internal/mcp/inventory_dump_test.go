package mcp

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestInventoryDump is an opt-in compatibility snapshot for the cleanup merge.
func TestInventoryDump(t *testing.T) {
	dir := os.Getenv("MCP_INVENTORY_DUMP")
	if dir == "" {
		t.Skip("MCP_INVENTORY_DUMP is not set")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, events := range []bool{false, true} {
		for _, noSpam := range []bool{false, true} {
			for _, kind := range []string{"default", "deploy", "flat", "passcode", "analytics", "apps", "hack-events", "hack-grant", "hack-site"} {
				t.Run(fmt.Sprintf("events-%t-spam-%t-%s", events, noSpam, kind), func(t *testing.T) {
					for _, flag := range []string{"MCP_APP_PASSCODE", "MCP_APP_MY_SITES", "MCP_APP_DASHBOARD", "MCP_APP_PHOTO_PICKER", "MCP_APP_SITE_PREVIEW"} {
						t.Setenv(flag, "")
					}
					cfg := Config{NoSpamCheck: noSpam}
					if events {
						cfg.Events = &fakeEventsBackend{}
					}
					c := Caller{APIKey: "snapshot", Handle: "snapshot"}
					switch kind {
					case "deploy":
						c.DeployOnly = true
					case "flat":
						c.AppName = "ChatGPT"
					case "passcode":
						t.Setenv("MCP_APP_PASSCODE", "on")
					case "analytics":
						cfg.AnalyticsProjectID = "snapshot"
					case "apps":
						for _, flag := range []string{"MCP_APP_MY_SITES", "MCP_APP_DASHBOARD", "MCP_APP_PHOTO_PICKER", "MCP_APP_SITE_PREVIEW"} {
							t.Setenv(flag, "on")
						}
					case "hack-events":
						c.Mode = CallerModeEvents
					case "hack-grant":
						c.Mode = CallerModeEvents
						c.GrantID = "snapshot"
					case "hack-site":
						c.Mode = CallerModeHackSite
					}
					s := NewServer(cfg)
					_, _, byName := s.forCaller(c)
					names := make([]string, 0, len(byName))
					for name := range byName {
						names = append(names, name)
					}
					sort.Strings(names)
					lists := map[string]json.RawMessage{}
					for _, version := range []string{protocolVersion, "2025-03-26"} {
						body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
						if version == protocolVersion {
							body = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + protocolVersion + `","io.modelcontextprotocol/clientCapabilities":{}}}}`
						}
						r := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
						r.Header.Set("MCP-Protocol-Version", version)
						r.Header.Set("Mcp-Method", "tools/list")
						r = r.WithContext(WithCaller(r.Context(), c))
						rec := httptest.NewRecorder()
						s.ServeHTTP(rec, r)
						var reply struct {
							Result json.RawMessage `json:"result"`
						}
						if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || len(reply.Result) == 0 {
							t.Fatalf("listing: %s", rec.Body.String())
						}
						lists[version] = reply.Result
					}
					data, err := json.MarshalIndent(struct {
						Lists    map[string]json.RawMessage `json:"lists"`
						Callable []string                   `json:"callable"`
					}{lists, names}, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(filepath.Join(dir, fmt.Sprintf("events-%t-spam-%t-%s.json", events, noSpam, kind)), data, 0644); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
}
