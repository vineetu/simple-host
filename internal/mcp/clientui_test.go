package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestInitializeRemembersUISupport(t *testing.T) {
	t.Setenv("MCP_APP_PHOTO_PICKER", "on")
	s := newTestServer(nil)
	link := func(c Caller) map[string]any {
		raw, _ := json.Marshal(map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "blog"}})
		reply := s.callTool(httptest.NewRequest("POST", "/mcp", nil), request{Params: raw}, c, false)
		b, _ := json.Marshal(reply.Result)
		var result map[string]any
		_ = json.Unmarshal(b, &result)
		return result["structuredContent"].(map[string]any)
	}
	initialize := func(c Caller, caps string) {
		req := request{Method: "initialize", Params: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":` + caps + `,"clientInfo":{"name":"t","version":"1"}}`)}
		if reply := s.dispatch(httptest.NewRequest("POST", "/mcp", nil), req, c); reply.Error != nil {
			t.Fatalf("initialize failed: %v", reply.Error)
		}
	}
	picker := func(out map[string]any) bool {
		return strings.Contains(out["next"].(string), "photo picker is open") && strings.Contains(out["photos_url"].(string), "&account=") && !strings.Contains(out["photos_url"].(string), "#t=")
	}

	apps := Caller{Handle: "alice", APIKey: "key-a", OAuthGrantID: "grant-apps"}
	text := Caller{Handle: "alice", APIKey: "key-b", OAuthGrantID: "grant-text"}
	unknown := Caller{Handle: "alice", APIKey: "key-c"}
	initialize(apps, `{"extensions":{"io.modelcontextprotocol/ui":{"mimeTypes":["text/html;profile=mcp-app"]}}}`)
	initialize(text, `{}`)
	if !picker(link(apps)) {
		t.Error("a client that declared MCP Apps did not get the picker")
	}
	if out := link(text); picker(out) || !strings.Contains(out["photos_url"].(string), "#t=") {
		t.Error("a client without MCP Apps did not get the photos link")
	}
	if !picker(link(unknown)) {
		t.Error("an unknown connection changed from the picker default")
	}
	// A grant keeps its answer when its access token changes.
	if !picker(link(Caller{Handle: "alice", APIKey: "key-a2", OAuthGrantID: "grant-apps"})) {
		t.Error("the grant's answer was lost with a new access token")
	}
	// A later initialize replaces the answer.
	initialize(apps, `{}`)
	if picker(link(apps)) {
		t.Error("a new initialize did not replace the remembered answer")
	}
}

func TestClientUIMemoryIsBounded(t *testing.T) {
	var m clientUIMemory
	now := time.Now()
	for i := 0; i < clientUIMaxEntries+5; i++ {
		m.remember(Caller{APIKey: "k" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + time.Duration(i).String()}, true, now.Add(time.Duration(i)))
	}
	if len(m.entries) > clientUIMaxEntries {
		t.Fatalf("memory grew to %d entries", len(m.entries))
	}
	if _, known := m.lookup(Caller{APIKey: "never"}, now); known {
		t.Error("unknown caller reported as known")
	}
	m.remember(Caller{APIKey: "old"}, true, now.Add(-clientUITTL-time.Hour))
	if _, known := m.lookup(Caller{APIKey: "old"}, now); known {
		t.Error("expired entry still answered")
	}
}
