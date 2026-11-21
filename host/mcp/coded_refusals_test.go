package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// CodedError/RefusalCode are the cross-package bridge host/handler uses
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
		if resourceCodeHint(code, "") == "" {
			t.Errorf("no hint for %s, so analytics would record it as tool_error", code)
		}
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
	s := NewServer(testConfig(Config{AnalyticsProjectID: "unknown-tool-test", ServerName: "simple-host", Version: "1", Upstream: &recordingUpstream{}, APIHost: "simple-host.app"}))
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
	s := NewServer(testConfig(Config{AnalyticsProjectID: "recently-deleted-test", ServerName: "simple-host", Version: "1", Upstream: up, APIHost: "simple-host.app"}))
	t.Cleanup(func() { close(s.analytics.queue) })
	send(t, s, toolCall("create_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "<h1>hi</h1>"}}), nil, true)
	if code := recvAnalyticsErrorCode(t, bodies); code != "recently_deleted" {
		t.Fatalf("analytics code = %q, want recently_deleted", code)
	}
}

// A visitor-tool refusal made with CodedError (the host/handler bridge)
// is also recognised by analytics under its real code, not tool_error.
func TestVisitorToolRefusalAnalyticsCodeNotToolError(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	s := NewServer(testConfig(Config{AnalyticsProjectID: "visitor-refusal-test", VisitorTool: func(_ *http.Request, _ Caller, _ string, _ map[string]any) (map[string]any, error) {
		return nil, CodedError("site_locked", "site_locked: this site asks for a passcode; the person can open it in their browser and enter it there, or call again with passcode set if they already gave it to you")
	}}))

	t.Cleanup(func() { close(s.analytics.queue) })
	text, _, isErr := resultOf(t, send(t, s, toolCall("visitor_read_site", map[string]any{"address": "site.example"}), nil, true))
	if !isErr {
		t.Fatalf("expected a refusal: %s", text)
	}
	if code := recvAnalyticsErrorCode(t, bodies); code != "site_locked" {
		t.Fatalf("analytics code = %q, want site_locked", code)
	}
}
