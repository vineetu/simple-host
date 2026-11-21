package hackmcp

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// The judge and organiser screens, their show tools and their app-only twins
// are offered only to a Hack connection whose screen flag is on for it, that
// declared MCP Apps UI support, and that is not a ChatGPT/OpenAI/Codex app.
// The judge and organiser flags are independent.

const (
	judgeScreenURI     = "ui://simple-hack/judge"
	organiserScreenURI = "ui://simple-hack/organiser"
)

type screenGateCase struct {
	name                     string
	judgeFlag, organiserFlag string
	caller                   mcp.Caller
	ui                       bool
	wantJudge, wantOrganiser bool
}

func screenGateCases() []screenGateCase {
	events := func(key, app string) mcp.Caller {
		return mcp.Caller{APIKey: key, Mode: mcp.CallerModeEvents, Handle: "alice", AppName: app}
	}
	return []screenGateCase{
		{"judge flag only", "on", "off", events("k-judge", ""), true, true, false},
		{"organiser flag only", "off", "on", events("k-organiser", ""), true, false, true},
		{"flag for another account", "accounts:bob", "accounts:alice", events("k-accounts", "Claude"), true, false, true},
		{"no UI declared", "on", "on", events("k-noui", ""), false, false, false},
		{"ChatGPT", "on", "on", events("k-chatgpt", "ChatGPT"), true, false, false},
		{"Codex", "on", "on", events("k-codex", "codex-cli"), true, false, false},
		{"Simple Host connection", "on", "on", mcp.Caller{APIKey: "k-host", Handle: "alice"}, true, false, false},
	}
}

func TestHackScreenResourcesGate(t *testing.T) {
	for _, tc := range screenGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_HACK_JUDGE", tc.judgeFlag)
			t.Setenv("MCP_APP_HACK_ORGANISER", tc.organiserFlag)
			s := newTestServer(organiserUpstream())
			initializeAs(t, s, tc.caller, tc.ui)
			listed := map[string]bool{}
			resources, _ := decode(t, rpcAs(t, s, tc.caller, "2025-06-18", "resources/list", nil, false))["result"].(map[string]any)["resources"].([]any)
			for _, raw := range resources {
				listed[raw.(map[string]any)["uri"].(string)] = true
			}
			for uri, want := range map[string]bool{judgeScreenURI: tc.wantJudge, organiserScreenURI: tc.wantOrganiser} {
				reply := decode(t, rpcAs(t, s, tc.caller, "2025-06-18", "resources/read", map[string]any{"uri": uri}, false))
				if listed[uri] != want || (reply["error"] == nil) != want {
					t.Fatalf("%s: listed=%v readable=%v, want %v", uri, listed[uri], reply["error"] == nil, want)
				}
			}
		})
	}
}

func TestHackScreenToolsListingGate(t *testing.T) {
	judgeTools := append([]string{"hack_show_judge"}, hackAppOnlyJudgeNames...)
	organiserTools := append([]string{"hack_show_organiser"}, hackAppOnlyOrganiserNames...)
	for _, tc := range screenGateCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_HACK_JUDGE", tc.judgeFlag)
			t.Setenv("MCP_APP_HACK_ORGANISER", tc.organiserFlag)
			s := newTestServer(organiserUpstream())
			_, listed := toolsAs(t, s, tc.caller, tc.ui)
			for _, group := range []struct {
				names []string
				want  bool
			}{{judgeTools, tc.wantJudge}, {organiserTools, tc.wantOrganiser}, {hackAppOnlySharedNames, tc.wantJudge || tc.wantOrganiser}} {
				for _, name := range group.names {
					tool, ok := listed[name]
					if ok != group.want {
						t.Errorf("%s listed=%v, want %v", name, ok, group.want)
					}
					// A twin is for the screen, never the model.
					if ok && strings.HasPrefix(name, "hack_app_") {
						ui, _ := tool.Meta["ui"].(map[string]any)
						if vis, _ := ui["visibility"].([]any); len(vis) != 1 || vis[0] != "app" {
							t.Errorf("%s visibility = %v, want [app]", name, ui["visibility"])
						}
					}
				}
			}
		})
	}
	// The newest revision declares UI support on each request instead.
	t.Setenv("MCP_APP_HACK_JUDGE", "on")
	t.Setenv("MCP_APP_HACK_ORGANISER", "on")
	s := newTestServer(organiserUpstream())
	caller := mcp.Caller{APIKey: "k-modern", Mode: mcp.CallerModeEvents, Handle: "alice"}
	for _, ui := range []bool{false, true} {
		found := 0
		for _, tool := range listedTools(t, rpcAs(t, s, caller, mcp.ProtocolVersion, "tools/list", nil, ui)) {
			if tool["name"] == "hack_show_judge" || tool["name"] == "hack_show_organiser" {
				found++
			}
		}
		want := 0
		if ui {
			want = 2
		}
		if found != want {
			t.Fatalf("modern ui=%v: %d show tools listed", ui, found)
		}
	}
}

// A direct call is refused the same way: on a text-only connection, or with
// the screen's flag off.
func TestHackShowToolsRefuseNonUIAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		tool, flag string
		args       map[string]any
	}{
		{"hack_show_judge", "MCP_APP_HACK_JUDGE", map[string]any{"view": "queue", "slug": "mockjam"}},
		{"hack_show_organiser", "MCP_APP_HACK_ORGANISER", map[string]any{"view": "overview", "slug": "mockjam"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			s := newTestServer(organiserUpstream())
			tool := serverTool(t, s, tc.tool)
			c := organiserCaller("alice", "")
			t.Setenv(tc.flag, "on")
			if _, err := mcp.CallTool(tool, mcp.NewCall(s, httptest.NewRequest("POST", "/mcp", nil), c).WithUICapable(false), tc.args); err == nil {
				t.Fatal("text-only connection allowed")
			}
			t.Setenv(tc.flag, "off")
			if _, err := mcp.CallTool(tool, mcp.NewCall(s, httptest.NewRequest("POST", "/mcp", nil), c).WithUICapable(true), tc.args); err == nil {
				t.Fatal("ran with its flag off")
			}
		})
	}
}
