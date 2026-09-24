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
	for _, want := range []string{"who_am_i", "list_sites", "create_site", "update_site", "get_site", "list_versions", "rollback_site", "delete_site", "set_visibility", "get_state", "update_state", "read_collection"} {
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

func TestCreateAndUpdateSite(t *testing.T) {
	created := func() (int, string) {
		return 201, `{"id":"9b1c","user_id":"u-1","name":"blog","active_version":1,"site_url":"https://sites.simple-host.app/ann/blog/","created_at":"2026-09-24T10:00:00Z","updated_at":"2026-09-24T10:00:00Z"}`
	}
	exists := func() (int, string) { return 409, `{"error":"site already exists"}` }
	missing := func() (int, string) { return 404, `{"error":"site not found"}` }
	updated := func() (int, string) {
		return 200, `{"id":"9b1c","user_id":"u-1","name":"blog","active_version":4,"site_url":"https://sites.simple-host.app/ann/blog/","updated_at":"2026-09-24T10:00:00Z"}`
	}
	files := map[string]any{"index.html": "<h1>hi</h1>"}

	// create_site: POST only, and never falls back to overwriting.
	up := &recordingUpstream{answers: map[string]func() (int, string){"POST /v1/sites/blog/files": exists, "PUT /v1/sites/blog/files": updated}}
	text, _, isErr := resultOf(t, send(t, newTestServer(up), toolCall("create_site", map[string]any{"site": "blog", "files": files}), nil, true))
	if !isErr || len(up.requests) != 1 || !strings.Contains(text, "update_site") {
		t.Fatalf("create overwrote or retried: %s (%d requests)", text, len(up.requests))
	}
	up = &recordingUpstream{answers: map[string]func() (int, string){"POST /v1/sites/blog/files": created}}
	rec := send(t, newTestServer(up), toolCall("create_site", map[string]any{"site": "blog", "files": files}), nil, true)
	text, structured, isErr := resultOf(t, rec)
	if isErr || structured["active_version"].(float64) != 1 || !strings.Contains(text, "https://sites.simple-host.app/ann/blog/") {
		t.Fatalf("create: %s", text)
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(up.bodies[0]), &sent)
	if sent["files"].(map[string]any)["index.html"] != "<h1>hi</h1>" {
		t.Errorf("files not forwarded: %s", up.bodies[0])
	}
	// Internal identifiers and bookkeeping times never reach the model.
	for _, leak := range []string{"9b1c", "u-1", "2026-09-24T10:00:00Z", "user_id", "updated_at"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("create_site result carries %q: %s", leak, rec.Body.String())
		}
	}

	// update_site: PUT only, and never creates.
	up = &recordingUpstream{answers: map[string]func() (int, string){"PUT /v1/sites/blog/files": missing, "POST /v1/sites/blog/files": created}}
	text, _, isErr = resultOf(t, send(t, newTestServer(up), toolCall("update_site", map[string]any{"site": "blog", "files": files}), nil, true))
	if !isErr || len(up.requests) != 1 || !strings.Contains(text, "create_site") {
		t.Fatalf("update created or retried: %s (%d requests)", text, len(up.requests))
	}
	up = &recordingUpstream{answers: map[string]func() (int, string){"PUT /v1/sites/blog/files": updated}}
	_, structured, isErr = resultOf(t, send(t, newTestServer(up), toolCall("update_site", map[string]any{"site": "blog", "files": files}), nil, true))
	if isErr || structured["active_version"].(float64) != 4 {
		t.Fatalf("update: %v", structured)
	}
}

// Every tool states all three hints the plugin directory reviews, and the
// values are the ones justified in openai-plugin/SUBMISSION.md.
func TestAnnotationsMatchBehaviour(t *testing.T) {
	type hints struct{ readOnly, destructive, openWorld bool }
	want := map[string]hints{
		"who_am_i":          {true, false, false},
		"list_sites":        {true, false, false},
		"get_site":          {true, false, false},
		"read_site_file":    {true, false, false},
		"list_versions":     {true, false, false},
		"get_state":         {true, false, false},
		"list_collections":  {true, false, false},
		"read_collection":   {true, false, false},
		"domain_status":     {true, false, false},
		"site_analytics":    {true, false, false},
		"create_site":       {false, false, true},
		"update_site":       {false, true, true},
		"rollback_site":     {false, false, true},
		"delete_site":       {false, true, false},
		"rename_site":       {false, false, true},
		"set_visibility":    {false, false, true},
		"update_state":      {false, true, true},
		"add_to_collection": {false, true, true},
		"connect_domain":    {false, false, true},

		"set_collection_privacy": {false, false, true},
	}
	tools := Tools()
	if len(tools) != len(want) {
		t.Errorf("%d tools, %d with expected hints: update both lists and SUBMISSION.md", len(tools), len(want))
	}
	for _, tool := range tools {
		w, ok := want[tool.Name]
		if !ok {
			t.Errorf("%s: no expected hints", tool.Name)
			continue
		}
		for key, v := range map[string]bool{"readOnlyHint": w.readOnly, "destructiveHint": w.destructive, "openWorldHint": w.openWorld} {
			got, present := tool.Annotations[key].(bool)
			if !present || got != v {
				t.Errorf("%s: %s = %v (present %v), want %v", tool.Name, key, tool.Annotations[key], present, v)
			}
		}
	}
}

// Tool results carry what a person needs, not the server's bookkeeping.
func TestToolResultsCarryNoInternalIdentifiers(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/me": func() (int, string) {
			return 200, `{"id":"u-123","username":"a@example.com","handle":"ann","is_admin":false}`
		},
		"GET /v1/sites": func() (int, string) {
			return 200, `[{"id":"s-9","user_id":"u-123","name":"blog","active_version":2,"site_url":"https://sites.simple-host.app/ann/blog/","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","visibility":"public","owner_username":"a@example.com"}]`
		},
		"GET /v1/sites/blog/versions": func() (int, string) {
			return 200, `[{"version_number":1,"status":"ready","created_at":"2026-01-01T00:00:00Z","is_active":false,"id":"v-1","site_id":"s-9"},{"version_number":2,"status":"ready","created_at":"2026-01-02T00:00:00Z","is_active":true}]`
		},
		"GET /v1/sites/blog/collections": func() (int, string) {
			return 200, `{"collections":[{"name":"rsvps","count":3,"last_at":"2026-01-03T00:00:00Z"}]}`
		},
		"GET /v1/u/ann/sites/blog/collections/rsvps": func() (int, string) {
			return 200, `{"items":[{"id":4411,"data":{"name":"Ann"},"created_at":"2026-01-03T00:00:00Z"}],"next":4411}`
		},
		"POST /v1/u/ann/sites/blog/collections/rsvps": func() (int, string) {
			return 201, `{"id":4412,"data":{"name":"Bo"},"created_at":"2026-01-04T00:00:00Z"}`
		},
		"GET /v1/sites/blog/domain": func() (int, string) {
			return 200, `{"domain":"rsvp.example.com","status":"pending","bound_at":"2026-01-05T00:00:00Z","expires_at":"2026-01-06T00:00:00Z","took_over_from":"other-site","dns":{"type":"CNAME","host":"rsvp.example.com","value":"sites.simple-host.app"}}`
		},
	}}
	s := newTestServer(up)
	calls := []struct {
		tool string
		args map[string]any
	}{
		{"who_am_i", map[string]any{}},
		{"list_sites", map[string]any{}},
		{"list_versions", map[string]any{"site": "blog"}},
		{"list_collections", map[string]any{"site": "blog"}},
		{"read_collection", map[string]any{"site": "blog", "collection": "rsvps"}},
		{"add_to_collection", map[string]any{"site": "blog", "collection": "rsvps", "item": map[string]any{"name": "Bo"}}},
		{"domain_status", map[string]any{"site": "blog"}},
	}
	for _, c := range calls {
		rec := send(t, s, toolCall(c.tool, c.args), nil, true)
		var envelope struct {
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
		body := string(envelope.Result) // the JSON-RPC envelope's own "id" is not the tool's
		if _, _, isErr := resultOf(t, rec); isErr {
			t.Errorf("%s failed: %s", c.tool, body)
			continue
		}
		for _, leak := range []string{"u-123", "s-9", "v-1", `"id"`, "user_id", "site_id", "4412", "updated_at", "last_at", "bound_at", "expires_at", "other-site", "is_admin", "owner_username"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s result carries %q: %s", c.tool, leak, body)
			}
		}
	}
	// What a person needs is still there.
	_, structured, _ := resultOf(t, send(t, s, toolCall("read_collection", map[string]any{"site": "blog", "collection": "rsvps"}), nil, true))
	items := structured["items"].([]any)
	if items[0].(map[string]any)["data"].(map[string]any)["name"] != "Ann" || structured["next"] != "4411" {
		t.Errorf("read_collection lost the data or the paging cursor: %v", structured)
	}
	_, structured, _ = resultOf(t, send(t, s, toolCall("domain_status", map[string]any{"site": "blog"}), nil, true))
	if structured["status"] != "pending" || structured["dns_record"].(map[string]any)["value"] != "sites.simple-host.app" {
		t.Errorf("domain_status lost the DNS record: %v", structured)
	}
}

func TestArgumentValidationHappensBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"create_site", map[string]any{"site": "blog", "files": map[string]any{"about.html": "x"}}, "index.html"},
		{"update_site", map[string]any{"site": "Bad Name!", "files": map[string]any{"index.html": "x"}}, "not a valid site name"},
		{"create_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "x"}, "mode": "replace"}, "unexpected argument"},
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
