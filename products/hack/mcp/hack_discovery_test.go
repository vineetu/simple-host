package hackmcp

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

var discoveryCaller = mcp.Caller{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant"}

func listedNames(t *testing.T, s *mcp.Server, caller mcp.Caller) []string {
	t.Helper()
	var names []string
	for _, tool := range listedTools(t, rpcAs(t, s, caller, "2025-06-18", "tools/list", nil, false)) {
		names = append(names, tool["name"].(string))
	}
	return names
}

func findTools(t *testing.T, s *mcp.Server, caller mcp.Caller, query string, limit int) []string {
	t.Helper()
	rec := rpcAs(t, s, caller, "2025-06-18", "tools/call", map[string]any{"name": "find_tools", "arguments": map[string]any{"query": query, "limit": limit}}, false)
	_, structured, isErr := resultOf(t, rec)
	if isErr {
		t.Fatalf("find_tools %q: %s", query, rec.Body.String())
	}
	var names []string
	for _, item := range structured["tools"].([]any) {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

// A Claude-style connection lists the core tools, find_tools and run_tool;
// ChatGPT still lists every tool and neither discovery tool.
func TestHackDiscoveryShortList(t *testing.T) {
	s := newTestServer(nil)
	short := listedNames(t, s, discoveryCaller)
	want := append(slices.Clone(hackCoreTools), "find_tools", "run_tool")
	slices.Sort(short)
	slices.Sort(want)
	if !slices.Equal(short, want) {
		t.Fatalf("short list = %v\nwant %v", short, want)
	}
	flat := listedNames(t, s, mcp.Caller{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant", AppName: "ChatGPT"})
	if slices.Contains(flat, "find_tools") || slices.Contains(flat, "run_tool") || len(flat) <= 2*len(short) {
		t.Fatalf("ChatGPT lists %d tools, discovery included: %t", len(flat), slices.Contains(flat, "find_tools"))
	}
	for _, name := range hackCoreTools {
		if !slices.Contains(flat, name) {
			t.Errorf("core tool %s is not a tool of this connection", name)
		}
	}
	text := instructionsAs(t, s, discoveryCaller)
	if !strings.Contains(text, hackMoreToolsInstructions) {
		t.Fatalf("short-list instructions do not say how to reach the other tools: %s", text)
	}
	if text := instructionsAs(t, s, mcp.Caller{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant", AppName: "ChatGPT"}); strings.Contains(text, "find_tools") {
		t.Fatalf("ChatGPT is told about find_tools: %s", text)
	}
}

// Every alias names a tool of the connection, and find_tools leads common jobs
// to the tool that does them.
func TestHackDiscoveryFindsJobs(t *testing.T) {
	s := newTestServer(nil)
	flat := listedNames(t, s, mcp.Caller{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant", AppName: "ChatGPT"})
	for name := range hackToolAliases {
		if !slices.Contains(flat, name) {
			t.Errorf("hackToolAliases names %s, not a tool of this connection", name)
		}
	}
	for query, want := range map[string]string{
		"approve an application":         "hack_decide_application",
		"email all teams":                "hack_message_team",
		"assign judges to entries":       "hack_generate_assignments",
		"change the event dates":         "hack_update_event",
		"download the scores":            "hack_export_scores",
		"find a team to join":            "hack_list_open_teams",
		"vote for a project":             "hack_vote",
		"leave the event":                "hack_preview_leave_event",
		"invite a co-organiser":          "hack_create_organiser_invite",
		"undo a website change":          "rollback_site",
		"new join link":                  "hack_regenerate_code",
		"how is judging going":           "hack_get_judging_dashboard",
		"sign-up form page code":         "hack_get_page_recipe",
		"give our team site a domain":    "connect_domain",
		"upload a screenshot of project": "hack_set_entry_screenshot",
	} {
		if got := findTools(t, s, discoveryCaller, query, 3); !slices.Contains(got, want) {
			t.Errorf("find_tools %q = %v, want %s among them", query, got, want)
		}
	}
}

// find_tools offers nothing the full list would withhold: the screen tools
// (listed only to an app that draws them) are never search results.
func TestHackDiscoveryDoesNotWiden(t *testing.T) {
	s := newTestServer(nil)
	catalog := findTools(t, s, discoveryCaller, "", 10)
	flat := listedNames(t, s, mcp.Caller{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant", AppName: "ChatGPT"})
	for _, name := range catalog {
		if !slices.Contains(flat, name) {
			t.Errorf("find_tools offers %s, which the full list withholds", name)
		}
		if strings.HasPrefix(name, "hack_app_") || strings.HasPrefix(name, "hack_show_") {
			t.Errorf("find_tools offers screen tool %s", name)
		}
	}
	for _, name := range findTools(t, s, discoveryCaller, "show the judge view app load queue", 10) {
		if strings.HasPrefix(name, "hack_app_") || strings.HasPrefix(name, "hack_show_") {
			t.Errorf("find_tools offers screen tool %s", name)
		}
	}
}

// run_tool reaches an unlisted tool through the same call, so the server's
// refusal is the same as for a direct call.
func TestHackRunToolSameChecks(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"GET /v1/hack/events/spring/applications": jsonReply(403, `{"error":"organisers only","code":"not_organiser"}`),
	}}
	s := newTestServer(up)
	direct, _, directErr := resultOf(t, rpcAs(t, s, discoveryCaller, "2025-06-18", "tools/call", map[string]any{"name": "hack_get_applications", "arguments": map[string]any{"slug": "spring"}}, false))
	wrapped, _, wrappedErr := resultOf(t, rpcAs(t, s, discoveryCaller, "2025-06-18", "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "hack_get_applications", "arguments": map[string]any{"slug": "spring"}}}, false))
	if !directErr || !wrappedErr || direct != wrapped || !strings.Contains(direct, "not_organiser") {
		t.Fatalf("direct %t %q, run_tool %t %q", directErr, direct, wrappedErr, wrapped)
	}
	text, _, isErr := resultOf(t, rpcAs(t, s, discoveryCaller, "2025-06-18", "tools/call", map[string]any{"name": "run_tool", "arguments": map[string]any{"name": "hack_no_such_tool"}}, false))
	if !isErr || !strings.Contains(text, "find_tools") {
		t.Fatalf("unknown tool through run_tool: %q", text)
	}
}
