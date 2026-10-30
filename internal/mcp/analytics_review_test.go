package mcp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// F6 follow-up (2026-10-30): a passcode repeated in the intent stays out of
// AgentCat when run_tool's arguments arrive as a JSON string, and when a
// numeric passcode has seven or more digits.
func reviewSecretRun(t *testing.T, tool string, args map[string]any, secret, name string) {
	t.Helper()
	s := newTestServer(&recordingUpstream{})
	s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
	s.byName[tool] = Tool{Name: tool, InputSchema: object(map[string]any{"passcode": str("secret")}), run: func(_ *call, _ map[string]any) (output, error) {
		return output{Text: "ok"}, nil
	}}
	var req request
	if err := json.Unmarshal([]byte(toolCall(name, args)), &req); err != nil {
		t.Fatal(err)
	}
	s.callTool(httptest.NewRequest("POST", "/mcp", nil), req, Caller{AppName: "Claude"}, false)
	select {
	case e := <-s.analytics.queue:
		raw, _ := json.Marshal(e)
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatalf("secret reached analytics: %s", raw)
		}
	default:
		t.Fatal("no analytics event")
	}
}

func TestAnalyticsRunToolStringArgumentsSecret(t *testing.T) {
	reviewSecretRun(t, "visitor_read_site", map[string]any{"name": "visitor_read_site", "arguments": `{"passcode":"839201"}`, "context": "unlocking the site with 839201"}, "839201", "run_tool")
}

func TestAnalyticsLongNumericSecret(t *testing.T) {
	reviewSecretRun(t, "set_site_passcode", map[string]any{"passcode": 12345678, "context": "setting the passcode to 12345678"}, "12345678", "set_site_passcode")
}
