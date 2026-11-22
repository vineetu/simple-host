package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	accounts "github.com/vsriram/simple-host/host/access"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/mcp"
)

func TestNotificationMissingKeyMCPDiscovery(t *testing.T) {
	h := &appstorage.Handler{PasscodeKeys: &accounts.PasscodeKeys{}}
	server := mcp.NewServer(mcp.Config{Events: EventsBackend(h)})
	for _, method := range []string{"server/discover", "events/subscribe"} {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": "2026-07-28", "io.modelcontextprotocol/clientCapabilities": map[string]any{}}}})
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", method)
		req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{Owner: "owner", APIKey: "test-key"}))
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, req)
		var result map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if method == "server/discover" {
			out, ok := result["result"].(map[string]any)
			if !ok {
				t.Fatal("discovery failed")
			}
			caps := out["capabilities"].(map[string]any)
			if _, ok := caps["events"]; ok {
				t.Fatal("events advertised without key")
			}
			if _, ok := caps["extensions"]; ok {
				t.Fatal("events extension advertised without key")
			}
		} else {
			out, ok := result["error"].(map[string]any)
			if !ok || out["code"] != float64(mcp.EventCodeForbidden) {
				t.Fatal("subscribe did not return JSON-RPC error")
			}
			data, ok := out["data"].(map[string]any)
			if !ok || data["reason"] != "notifications_not_enabled" {
				t.Fatal("subscribe missing unavailable reason")
			}
		}
	}
}
