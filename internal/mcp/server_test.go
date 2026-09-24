package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recordingUpstream stands in for the application router: it records what
// the tools send and answers from a script keyed by "METHOD path".
type recordingUpstream struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
	answers  map[string]func() (int, string)
}

func (u *recordingUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	u.mu.Lock()
	u.requests = append(u.requests, r)
	u.bodies = append(u.bodies, string(body))
	u.mu.Unlock()
	status, out := 404, `{"error":"site not found"}`
	if f, ok := u.answers[r.Method+" "+r.URL.Path]; ok {
		status, out = f()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(out))
}

func newTestServer(up http.Handler) *Server {
	return NewServer(Config{
		Upstream: up, APIHost: "simple-host.app", ContentOrigin: "https://sites.simple-host.app",
		SkillVersion: "9.9.9", ServerName: "simple-host", Version: "9.9.9",
	})
}

func send(t *testing.T, s *Server, body string, headers map[string]string, authed bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if authed {
		req = req.WithContext(WithCaller(req.Context(), Caller{APIKey: "user-key"}))
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

func TestNoCallerIsRefused(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	rec := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, nil, false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestInitializeNegotiatesAndCarriesInstructions(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	for requested, want := range map[string]string{
		"2025-06-18": "2025-06-18",
		"2025-03-26": "2025-03-26",
		"2025-11-25": "2025-11-25",
		"2024-11-05": latestInitializeVersion,
		"1999-01-01": latestInitializeVersion,
	} {
		rec := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+requested+`","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, nil, true)
		out := decode(t, rec)
		res := out["result"].(map[string]any)
		if res["protocolVersion"] != want {
			t.Errorf("requested %s: got %v, want %s", requested, res["protocolVersion"], want)
		}
		if !strings.Contains(res["instructions"].(string), "RELATIVE links") {
			t.Error("instructions missing the relative-links rule")
		}
		if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
			t.Error("tools capability not declared")
		}
	}
}

func TestNotificationsAndResponsesGet202(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	for _, body := range []string{
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`,
		`{"jsonrpc":"2.0","id":5,"result":{}}`,
	} {
		rec := send(t, s, body, map[string]string{"MCP-Protocol-Version": "2025-06-18"}, true)
		if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
			t.Errorf("%s: %d %q", body, rec.Code, rec.Body.String())
		}
	}
}

func TestMalformedMessages(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	cases := map[string]float64{
		`not json`: codeParseError,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`:  codeInvalidRequest,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`:    codeInvalidRequest,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`: codeInvalidRequest,
	}
	for body, want := range cases {
		rec := send(t, s, body, nil, true)
		errObj, _ := decode(t, rec)["error"].(map[string]any)
		if errObj == nil || errObj["code"].(float64) != want {
			t.Errorf("%s: %s", body, rec.Body.String())
		}
	}
	rec := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"nope/nope"}`, map[string]string{"MCP-Protocol-Version": "2025-06-18"}, true)
	if rec.Code != http.StatusOK || decode(t, rec)["error"].(map[string]any)["code"].(float64) != codeMethodNotFound {
		t.Errorf("unknown method for an initialize-era client: %d %s", rec.Code, rec.Body.String())
	}
	rec = send(t, s, `{"jsonrpc":"2.0","id":1,"method":"ping"}`, map[string]string{"MCP-Protocol-Version": "2019-01-01"}, true)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unsupported version header accepted: %d", rec.Code)
	}
}

func TestGetAndDeleteAre405(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		req := httptest.NewRequest(m, "/mcp", nil)
		req = req.WithContext(WithCaller(req.Context(), Caller{APIKey: "k"}))
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d", m, rec.Code)
		}
	}
}

func TestModernRevisionHeaderRules(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"` + protocolVersion + `","io.modelcontextprotocol/clientCapabilities":{}}`
	ok := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+meta+`}}`,
		map[string]string{"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "tools/list"}, true)
	if ok.Code != 200 || decode(t, ok)["result"].(map[string]any)["resultType"] != "complete" {
		t.Fatalf("modern tools/list: %d %s", ok.Code, ok.Body.String())
	}
	missing := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+meta+`}}`,
		map[string]string{"MCP-Protocol-Version": protocolVersion}, true)
	if missing.Code != http.StatusBadRequest {
		t.Errorf("missing Mcp-Method accepted: %d", missing.Code)
	}
	mismatch := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{`+meta+`}}`,
		map[string]string{"MCP-Protocol-Version": protocolVersion, "Mcp-Method": "tools/call"}, true)
	if mismatch.Code != http.StatusBadRequest {
		t.Errorf("mismatched Mcp-Method accepted: %d", mismatch.Code)
	}
	// An initialize-era client gets a plain result, without the stateless
	// revision's extra fields.
	legacy := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "2025-06-18"}, true)
	if _, has := decode(t, legacy)["result"].(map[string]any)["resultType"]; has {
		t.Error("legacy tools/list carries resultType")
	}
}

func TestEveryToolIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range Tools() {
		if seen[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Description == "" || tool.InputSchema["type"] != "object" || tool.run == nil || tool.Annotations == nil {
			t.Errorf("tool %s is incomplete", tool.Name)
		}
	}
	for _, want := range []string{"who_am_i", "list_sites", "deploy_site", "get_site", "list_versions", "rollback_site", "delete_site", "set_visibility", "get_state", "update_state", "read_collection"} {
		if !seen[want] {
			t.Errorf("missing tool %s", want)
		}
	}
	for _, tool := range Tools() {
		if tool.Name == "delete_site" {
			if tool.Annotations["destructiveHint"] != true || !strings.Contains(tool.Description, "IRREVERSIBLE") {
				t.Error("delete_site is not marked destructive")
			}
		}
	}
}

func TestToolsCallUpstreamAsTheCaller(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/me": func() (int, string) { return 200, `{"username":"a@example.com","handle":"ann"}` },
	}}
	s := newTestServer(up)
	rec := send(t, s, toolCall("who_am_i", map[string]any{}), nil, true)
	text, structured, isErr := resultOf(t, rec)
	if isErr || structured["handle"] != "ann" || !strings.Contains(text, "https://sites.simple-host.app/ann") {
		t.Fatalf("who_am_i: %s", rec.Body.String())
	}
	r := up.requests[0]
	if r.Header.Get("X-API-Key") != "user-key" || r.Host != "simple-host.app" || r.RemoteAddr != "203.0.113.7:5555" || r.Header.Get("X-Skill-Version") != "9.9.9" {
		t.Errorf("upstream request not made as the caller: key=%q host=%q addr=%q", r.Header.Get("X-API-Key"), r.Host, r.RemoteAddr)
	}
	if strings.Contains(rec.Body.String(), "user-key") {
		t.Error("the caller's key leaked into a tool result")
	}
}

func TestDeployModes(t *testing.T) {
	created := func() (int, string) {
		return 201, `{"name":"blog","active_version":1,"site_url":"https://sites.simple-host.app/ann/blog/"}`
	}
	exists := func() (int, string) { return 409, `{"error":"site already exists"}` }
	updated := func() (int, string) {
		return 200, `{"name":"blog","active_version":4,"site_url":"https://sites.simple-host.app/ann/blog/"}`
	}
	files := map[string]any{"index.html": "<h1>hi</h1>"}

	// auto: create, and fall back to replace only on 409.
	up := &recordingUpstream{answers: map[string]func() (int, string){"POST /v1/sites/blog/files": exists, "PUT /v1/sites/blog/files": updated}}
	text, structured, isErr := resultOf(t, send(t, newTestServer(up), toolCall("deploy_site", map[string]any{"site": "blog", "files": files}), nil, true))
	if isErr || structured["active_version"].(float64) != 4 || structured["created"] != false || !strings.Contains(text, "https://sites.simple-host.app/ann/blog/") {
		t.Fatalf("auto: %s", text)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(up.bodies[1]), &sent)
	if sent["files"].(map[string]any)["index.html"] != "<h1>hi</h1>" {
		t.Errorf("files not forwarded: %s", up.bodies[1])
	}

	// create: never falls back.
	up = &recordingUpstream{answers: map[string]func() (int, string){"POST /v1/sites/blog/files": exists, "PUT /v1/sites/blog/files": updated}}
	text, _, isErr = resultOf(t, send(t, newTestServer(up), toolCall("deploy_site", map[string]any{"site": "blog", "mode": "create", "files": files}), nil, true))
	if !isErr || len(up.requests) != 1 || !strings.Contains(text, "409") {
		t.Fatalf("create overwrote or retried: %s (%d requests)", text, len(up.requests))
	}

	up = &recordingUpstream{answers: map[string]func() (int, string){"POST /v1/sites/blog/files": created}}
	_, structured, isErr = resultOf(t, send(t, newTestServer(up), toolCall("deploy_site", map[string]any{"site": "blog", "mode": "create", "files": files}), nil, true))
	if isErr || structured["created"] != true {
		t.Fatalf("create: %v", structured)
	}
}

func TestArgumentValidationHappensBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"deploy_site", map[string]any{"site": "blog", "files": map[string]any{"about.html": "x"}}, "index.html"},
		{"deploy_site", map[string]any{"site": "Bad Name!", "files": map[string]any{"index.html": "x"}}, "not a valid site name"},
		{"deploy_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "x"}, "mode": "overwrite"}, "mode must be"},
		{"delete_site", map[string]any{"site": "blog", "confirm_name": "blogg"}, "nothing was deleted"},
		{"rollback_site", map[string]any{"site": "blog", "version": 1.5}, "whole number"},
		{"update_state", map[string]any{"site": "blog"}, "exactly one of ops or replace"},
		{"read_site_file", map[string]any{"site": "blog"}, "path is required"},
	}
	for _, c := range cases {
		up := &recordingUpstream{}
		text, _, isErr := resultOf(t, send(t, newTestServer(up), toolCall(c.tool, c.args), nil, true))
		if !isErr || !strings.Contains(text, c.want) || len(up.requests) != 0 {
			t.Errorf("%s %v: isErr=%v text=%q requests=%d", c.tool, c.args, isErr, text, len(up.requests))
		}
	}
}

func TestRefusedRESTCallBecomesReadableToolError(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"DELETE /v1/sites/theirs": func() (int, string) { return 404, `{"error":"site not found"}` },
	}}
	text, _, isErr := resultOf(t, send(t, newTestServer(up), toolCall("delete_site", map[string]any{"site": "theirs", "confirm_name": "theirs"}), nil, true))
	if !isErr || !strings.Contains(text, "HTTP 404") || !strings.Contains(text, "list_sites") {
		t.Fatalf("got %q", text)
	}
}

func TestStateToolsUseTheCallersHandleAndContentOrigin(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/me":                                  func() (int, string) { return 200, `{"handle":"ann"}` },
		"PATCH /v1/u/ann/sites/party/state":           func() (int, string) { return 200, `{"count":3}` },
		"GET /v1/u/ann/sites/party/collections/rsvps": func() (int, string) { return 200, `{"items":[]}` },
	}}
	s := newTestServer(up)
	_, structured, isErr := resultOf(t, send(t, s, toolCall("update_state", map[string]any{"site": "party", "ops": []any{map[string]any{"op": "inc", "path": "count", "by": 1}}}), nil, true))
	if isErr || structured["state"].(map[string]any)["count"].(float64) != 3 {
		t.Fatalf("update_state: %v", structured)
	}
	last := up.requests[len(up.requests)-1]
	if last.Header.Get("Origin") != "https://sites.simple-host.app" {
		t.Errorf("state write missing content Origin: %q", last.Header.Get("Origin"))
	}
	_, _, isErr = resultOf(t, send(t, s, toolCall("read_collection", map[string]any{"site": "party", "collection": "rsvps", "limit": 5}), nil, true))
	if isErr || up.requests[len(up.requests)-1].URL.Query().Get("limit") != "5" {
		t.Fatal("read_collection did not pass limit")
	}
}
