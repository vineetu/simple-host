package hackmcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/mcp"
)

// The helpers below speak to the tool server the way a client does: over
// ServeHTTP, as one caller, in one protocol revision.

// rpcAs sends one JSON-RPC request as caller. The newest revision carries its
// version and capabilities on every request; ui adds the MCP Apps extension.
func rpcAs(t *testing.T, s *mcp.Server, caller mcp.Caller, version, method string, params map[string]any, ui bool) *httptest.ResponseRecorder {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	modern := version == mcp.ProtocolVersion
	if modern {
		extensions := map[string]any{}
		if ui {
			extensions["io.modelcontextprotocol/ui"] = map[string]any{}
		}
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    version,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{"extensions": extensions},
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	r.RemoteAddr = "203.0.113.7:5555"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("MCP-Protocol-Version", version)
	if modern {
		r.Header.Set("Mcp-Method", method)
		if name, ok := params["name"].(string); ok && method == "tools/call" {
			r.Header.Set("Mcp-Name", name)
		}
	}
	r = r.WithContext(mcp.WithCaller(r.Context(), caller))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, r)
	return rec
}

// initializeAs is an initialize-era handshake. It is also how such a client
// declares MCP Apps support, which the server remembers for the connection.
func initializeAs(t *testing.T, s *mcp.Server, caller mcp.Caller, ui bool) map[string]any {
	t.Helper()
	extensions := map[string]any{}
	if ui {
		extensions["io.modelcontextprotocol/ui"] = map[string]any{}
	}
	rec := rpcAs(t, s, caller, "2025-06-18", "initialize", map[string]any{
		"protocolVersion": "2025-06-18", "capabilities": map[string]any{"extensions": extensions},
		"clientInfo": map[string]any{"name": "fixture", "version": "1"},
	}, ui)
	out, ok := decode(t, rec)["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize: %s", rec.Body.String())
	}
	return out
}

// instructionsAs is the instructions this caller's connection is given.
func instructionsAs(t *testing.T, s *mcp.Server, caller mcp.Caller) string {
	t.Helper()
	text, _ := initializeAs(t, s, caller, false)["instructions"].(string)
	return text
}

// wireTool is one tool as tools/list sends it.
type wireTool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
	Meta         map[string]any `json:"_meta"`
}

// toolsAs is tools/list for this caller, in listing order and by name.
func toolsAs(t *testing.T, s *mcp.Server, caller mcp.Caller, ui bool) ([]wireTool, map[string]wireTool) {
	t.Helper()
	initializeAs(t, s, caller, ui)
	rec := rpcAs(t, s, caller, "2025-06-18", "tools/list", nil, ui)
	var reply struct {
		Result struct {
			Tools []wireTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("tools/list (%d): %s", rec.Code, rec.Body.String())
	}
	byName := make(map[string]wireTool, len(reply.Result.Tools))
	for _, tool := range reply.Result.Tools {
		byName[tool.Name] = tool
	}
	return reply.Result.Tools, byName
}

// callAs is one tools/call as caller, in an initialize-era revision.
func callAs(t *testing.T, s *mcp.Server, caller mcp.Caller, name string, args map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	if args == nil {
		args = map[string]any{}
	}
	return rpcAs(t, s, caller, "2025-06-18", "tools/call", map[string]any{"name": name, "arguments": args}, false)
}

// wireVersions are the revisions the server answers, newest first.
var wireVersions = []string{"2026-07-28", "2025-11-25", "2025-06-18", "2025-03-26"}

// contributionSnapshot hashes the wire bytes of tools/list and the
// instructions, including listing order and schemas, for every connection
// kind and protocol revision. The hashes in testdata/contribution-wire.json
// were captured before Simple Hack's tools and screens left the tool server
// (D3-S1a) and have not been re-recorded since: each is the SHA-256 of
// {"List":<the tools/list response>,"Instructions":<the instructions>}.
func contributionSnapshot(t *testing.T, noSpamCheck bool) map[string]string {
	t.Helper()
	out := map[string]string{}
	callers := map[string]mcp.Caller{
		"host": {}, "deploy": {DeployOnly: true}, "flat": {AppName: "ChatGPT"},
		"flat-deploy": {AppName: "ChatGPT", DeployOnly: true},
		"personal":    {Mode: mcp.CallerModeEvents}, "selected": {Mode: mcp.CallerModeEvents, GrantID: "grant", TeamAPIKey: "team"},
		"unselected": {Mode: mcp.CallerModeEvents, GrantID: "grant"}, "team": {Mode: mcp.CallerModeGroupSite},
	}
	for _, screens := range []string{"off", "on"} {
		for _, flag := range []string{"MCP_APP_HACK_JUDGE", "MCP_APP_HACK_ORGANISER", "MCP_APP_MY_SITES", "MCP_APP_DASHBOARD", "MCP_APP_PASSCODE"} {
			t.Setenv(flag, screens)
		}
		for _, rules := range []string{"off", "on"} {
			t.Setenv("STORAGE_RULES", rules)
			for _, events := range []bool{false, true} {
				product := Contribution()
				cfg := mcp.Config{APIHost: "connector.example.test", ServerName: "fixture", Version: "fixture", Contribution: &product, NoSpamCheck: noSpamCheck}
				if events {
					cfg.Events = wireEvents{}
				}
				s := mcp.NewServer(cfg)
				for name, c := range callers {
					c.Handle = "fixture"
					c.APIKey = "fixture"
					for _, version := range wireVersions {
						for _, ui := range []bool{false, true} {
							instructions := initializeAs(t, s, c, ui)["instructions"].(string)
							rec := rpcAs(t, s, c, version, "tools/list", nil, ui)
							if rec.Code != http.StatusOK {
								t.Fatalf("tools/list %s %s (%d): %s", name, version, rec.Code, rec.Body.String())
							}
							data, err := json.Marshal(struct {
								List         json.RawMessage
								Instructions string
							}{json.RawMessage(strings.TrimSuffix(rec.Body.String(), "\n")), instructions})
							if err != nil {
								t.Fatal(err)
							}
							key := fmt.Sprintf("%s/screens=%s/rules=%s/events=%t/%s/ui=%t", name, screens, rules, events, version, ui)
							out[key] = fmt.Sprintf("%x", sha256.Sum256(data))
						}
					}
				}
			}
		}
	}
	return out
}

// wireEvents turns the Events tools and capability on; listing never calls it.
type wireEvents struct{ mcp.EventsBackend }

func TestContributionWireUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name, file  string
		noSpamCheck bool
	}{
		{"spam check", "testdata/contribution-wire.json", false},
		// A server with no spam check (a self-hosted box starts this way).
		// Recorded with this same test from the commit before the screens
		// moved (D3-S1b), for Simple Hack's connection kinds.
		{"no spam check", "testdata/contribution-wire-nospam.json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := contributionSnapshot(t, tc.noSpamCheck)
			data, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			var want map[string]string
			if err := json.Unmarshal(data, &want); err != nil {
				t.Fatal(err)
			}
			if tc.noSpamCheck {
				// Only Simple Hack's connection kinds are pinned in this file.
				for key := range got {
					if kind, _, _ := strings.Cut(key, "/"); kind == "host" || kind == "deploy" || kind == "flat" || kind == "flat-deploy" {
						delete(got, key)
					}
				}
			}
			if len(got) != len(want) {
				t.Fatalf("snapshots: got %d want %d", len(got), len(want))
			}
			for key, hash := range got {
				if hash != want[key] {
					t.Errorf("wire changed for %s: got %s want %s", key, hash, want[key])
				}
			}
		})
	}
}
