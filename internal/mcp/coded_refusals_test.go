package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// CodedError/RefusalCode are the cross-package bridge internal/handler uses
// for refusals it makes itself (the visitor tool bridge, which runs outside
// this package). The code must round-trip exactly, and every code either
// side defines needs a hint or analytics silently records tool_error.
func TestCodedErrorBridgeRoundTrips(t *testing.T) {
	err := CodedError("site_locked", "this site asks for a passcode [site_locked]")
	if RefusalCode(err) != "site_locked" {
		t.Fatalf("code lost: %v", err)
	}
	wrapped := fmt.Errorf("context: %w", err)
	if RefusalCode(wrapped) != "site_locked" {
		t.Fatalf("code lost through wrapping: %v", wrapped)
	}
	if RefusalCode(errors.New("plain error")) != "" {
		t.Fatal("plain error reported a code")
	}
	for _, code := range []string{"passcode_locked", "unknown_tool", "recently_deleted", "site_not_found", "site_private"} {
		if codeHint(code) == "" {
			t.Errorf("no hint for %s, so analytics would record it as tool_error", code)
		}
	}
}

// run_tool's "Unknown tool" refusal now carries a code, so AgentCat's
// generic tool_error bucket stops absorbing it.
func TestRunToolUnknownToolIsCoded(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	text, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", map[string]any{"name": "rename_sit"}), nil, true))
	if !isErr || !strings.Contains(text, "Unknown tool") || !strings.Contains(text, "[unknown_tool]") {
		t.Fatalf("not coded: %s", text)
	}
}

// create_site's "name belongs to Recently deleted" refusal now carries its
// code instead of a hand-written duplicate of codeHints["recently_deleted"].
func TestCreateSiteRecentlyDeletedIsCoded(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"POST /v1/sites/blog/files": func() (int, string) {
			return 409, `{"error":"site recently deleted","code":"recently_deleted","recently_deleted":true}`
		},
	}}
	text, _, isErr := resultOf(t, send(t, newTestServer(up), toolCall("create_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "<h1>hi</h1>"}}), nil, true))
	if !isErr || !strings.Contains(text, "Recently deleted") || !strings.Contains(text, "restore_site") || !strings.Contains(text, "[recently_deleted]") {
		t.Fatalf("recently_deleted message: %s", text)
	}
}

// recvAnalyticsErrorCode waits for one analytics POST and reads back its
// error.message, which is the code analytics actually recorded -- not the
// text the model saw. Using analytics (rather than the displayed text) is
// the only way a revert to errors.New instead of codedError/CodedError
// would show up as a test failure: both forms can display identical text.
func recvAnalyticsErrorCode(t *testing.T, bodies <-chan []byte) string {
	t.Helper()
	select {
	case body := <-bodies:
		var got struct {
			Error struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("analytics body not JSON: %s", body)
		}
		return got.Error.Message
	case <-time.After(2 * time.Second):
		t.Fatal("analytics event not sent")
		return ""
	}
}

func TestRunToolUnknownToolAnalyticsCodeNotToolError(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	s := NewServer(Config{AnalyticsProjectID: "unknown-tool-test", ServerName: "simple-host", Version: "1", Upstream: &recordingUpstream{}, APIHost: "simple-host.app"})
	t.Cleanup(func() { close(s.analytics.queue) })
	send(t, s, toolCall("run_tool", map[string]any{"name": "rename_sit"}), nil, true)
	if code := recvAnalyticsErrorCode(t, bodies); code != "unknown_tool" {
		t.Fatalf("analytics code = %q, want unknown_tool", code)
	}
}

func TestCreateSiteRecentlyDeletedAnalyticsCodeNotToolError(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"POST /v1/sites/blog/files": func() (int, string) {
			return 409, `{"error":"site recently deleted","code":"recently_deleted","recently_deleted":true}`
		},
	}}
	s := NewServer(Config{AnalyticsProjectID: "recently-deleted-test", ServerName: "simple-host", Version: "1", Upstream: up, APIHost: "simple-host.app"})
	t.Cleanup(func() { close(s.analytics.queue) })
	send(t, s, toolCall("create_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "<h1>hi</h1>"}}), nil, true)
	if code := recvAnalyticsErrorCode(t, bodies); code != "recently_deleted" {
		t.Fatalf("analytics code = %q, want recently_deleted", code)
	}
}

// A visitor-tool refusal made with CodedError (the internal/handler bridge)
// is also recognised by analytics under its real code, not tool_error.
func TestVisitorToolRefusalAnalyticsCodeNotToolError(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	s := NewServer(Config{AnalyticsProjectID: "visitor-refusal-test", VisitorTool: func(_ *http.Request, _ Caller, _ string, _ map[string]any) (map[string]any, error) {
		return nil, CodedError("site_locked", "site_locked: this site asks for a passcode; the person can open it in their browser and enter it there, or call again with passcode set if they already gave it to you")
	}})
	t.Cleanup(func() { close(s.analytics.queue) })
	text, _, isErr := resultOf(t, send(t, s, toolCall("visitor_read_site", map[string]any{"address": "site.example"}), nil, true))
	if !isErr {
		t.Fatalf("expected a refusal: %s", text)
	}
	if code := recvAnalyticsErrorCode(t, bodies); code != "site_locked" {
		t.Fatalf("analytics code = %q, want site_locked", code)
	}
}

// storage_sql_query, storage_sql_execute and storage_sql_schema (Host and
// the Hack event-website copies) say upfront that PRAGMA is not available
// and what to use instead, so a model does not have to hit the refusal
// first (SIM-359, AgentCat sql_not_available review).
func TestSQLToolDescriptionsNamePragmaAlternative(t *testing.T) {
	want := map[string]bool{
		"storage_sql_query": false, "storage_sql_execute": false, "storage_sql_schema": false,
		"hack_event_storage_sql_query": false, "hack_event_storage_sql_execute": false, "hack_event_storage_sql_schema": false,
	}
	for _, tool := range append(append([]Tool{}, Tools()...), HackTools()...) {
		if _, tracked := want[tool.Name]; tracked {
			if !strings.Contains(tool.Description, "PRAGMA") || !strings.Contains(tool.Description, "sqlite_schema") {
				t.Errorf("%s description lacks the PRAGMA hint: %s", tool.Name, tool.Description)
			}
			want[tool.Name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("%s not found in the tool inventory", name)
		}
	}
}
