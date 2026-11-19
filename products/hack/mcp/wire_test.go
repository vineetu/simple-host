package hackmcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
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

// wireEvents turns the Events tools and capability on; listing never calls it.
type wireEvents struct{ mcp.EventsBackend }
