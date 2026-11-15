package hackmcp

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/vsriram/simple-host/internal/mcp"
)

// The helpers below are this package's copies of the tool server's own test
// helpers (internal/mcp/server_test.go), built with Hack's contribution.

func newTestServer(up http.Handler) *mcp.Server {
	product := Contribution()
	// Keep the screens the server was given, for the tests that read a
	// screen or run a screen tool directly (screensOf).
	var screens *hackScreens
	product.Apps = func(ctx mcp.AppContext) mcp.AppContribution {
		screens = &hackScreens{ctx: ctx}
		return hackApps(ctx)
	}
	s := mcp.NewServer(mcp.Config{
		Contribution: &product,
		Upstream:     up, APIHost: "simple-host.app", ContentOrigin: "https://sites.simple-host.app",
		SiteBases:    []string{"simple-host.app"},
		SkillVersion: "9.9.9", ServerName: "simple-host", Version: "9.9.9",
	})
	serverScreens.Store(s, screens)
	return s
}

// serverScreens maps a server built by newTestServer to its screens.
var serverScreens sync.Map

// screensOf is the product's screens as s was given them: the same API host
// and the tool server's own screen metadata.
func screensOf(s *mcp.Server) *hackScreens {
	screens, _ := serverScreens.Load(s)
	return screens.(*hackScreens)
}

// serverTool finds one declaration a connection can call, by name, among the
// base tools and the app-only family built for s. A declaration hidden from
// the connector is not found, as in the server's own lookup.
func serverTool(t *testing.T, s *mcp.Server, name string) mcp.Tool {
	t.Helper()
	tool, ok := findServerTool(s, name)
	if !ok {
		t.Fatalf("no tool named %s", name)
	}
	return tool
}

func findServerTool(s *mcp.Server, name string) (mcp.Tool, bool) {
	callable := false
	for _, entry := range mcp.Inventory(Contribution()) {
		if entry.Edition == "hack" && entry.Name == name && !entry.Hidden {
			callable = true
		}
	}
	if !callable {
		return mcp.Tool{}, false
	}
	base := Tools()
	for _, tool := range append(append([]mcp.Tool{}, base...), screensOf(s).hackAppFamilyTools(base)...) {
		if tool.Name == name {
			return tool, true
		}
	}
	return mcp.Tool{}, false
}

func send(t *testing.T, s *mcp.Server, body string, headers map[string]string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if authed {
		req = req.WithContext(mcp.WithCaller(req.Context(), mcp.Caller{APIKey: "user-key"}))
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON (%d): %s", rec.Code, rec.Body.String())
	}
	return out
}

func toolCall(name string, args map[string]any) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	return string(b)
}

func resultOf(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]any, bool) {
	t.Helper()
	out := decode(t, rec)
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %s", rec.Body.String())
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	structured, _ := res["structuredContent"].(map[string]any)
	return text, structured, res["isError"].(bool)
}

// Existing dispatch fixtures exercise the base event/entry schema declarations.
func hackOutputSchemas() map[string]map[string]any {
	schemas := map[string]map[string]any{}
	for _, tool := range append(hackBaseEventTools(), hackBaseEntryTools()...) {
		schemas[tool.Name] = tool.OutputSchema
	}
	return schemas
}

var hackRouteBodyDescriptions = func() map[string]string {
	descriptions := map[string]string{}
	for _, route := range hackOrganiserRoutes() {
		if route.bodyDescription != "" {
			descriptions[route.name] = route.bodyDescription
		}
	}
	return descriptions
}()

// toolNamed finds one declaration, hidden ones included.
func toolNamed(t *testing.T, name string) mcp.Tool {
	t.Helper()
	for _, tool := range Tools() {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("no tool named %s", name)
	return mcp.Tool{}
}
