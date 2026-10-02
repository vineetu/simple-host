package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestStorageToolsForwardRESTAndKeepBinaryOutOfResults(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/storage/usage": func() (int, string) {
			return 200, `{"used_bytes":7,"limit_bytes":1000000,"remaining_bytes":999993,"breakdown":{"kv_bytes":1,"sqlite_bytes":2,"files_bytes":4}}`
		},
		"PUT /v1/sites/blog/storage/kv/settings/keys/feature":           func() (int, string) { return 200, `{"key":"feature","value":null}` },
		"POST /v1/sites/blog/storage/sqlite/report/query":               func() (int, string) { return 200, `{"columns":["n"],"rows":[[1]]}` },
		"PUT /v1/sites/blog/storage/files/assets/objects/reports/a.pdf": func() (int, string) { return 201, `{"path":"reports/a.pdf"}` },
		"POST /v1/sites/blog/storage/files/assets/download-link": func() (int, string) {
			return 200, `{"url":"https://simple-host.app/v1/storage-download?token=opaque","expires_in":600}`
		},
		"GET /v1/sites/blog/storage/kv/settings/keys/secret": func() (int, string) { return 403, `{"error":"not allowed","code":"forbidden"}` },
	}}
	s := newTestServer(up)
	cases := []struct {
		tool                    string
		args                    map[string]any
		path, body, contentType string
	}{
		{"storage_get_usage", map[string]any{"site": "blog"}, "GET /v1/sites/blog/storage/usage", "", ""},
		{"storage_put_kv", map[string]any{"site": "blog", "name": "settings", "key": "feature", "value": nil}, "PUT /v1/sites/blog/storage/kv/settings/keys/feature", `{"value":null}`, "application/json"},
		{"storage_sql_query", map[string]any{"site": "blog", "name": "report", "sql": "SELECT ?", "params": []any{1}}, "POST /v1/sites/blog/storage/sqlite/report/query", `{"params":[1],"sql":"SELECT ?"}`, "application/json"},
		{"storage_put_file", map[string]any{"site": "blog", "name": "assets", "path": "reports/a.pdf", "content_base64": "AAEC", "content_type": "application/pdf"}, "PUT /v1/sites/blog/storage/files/assets/objects/reports/a.pdf", string([]byte{0, 1, 2}), "application/pdf"},
		{"storage_file_download_link", map[string]any{"site": "blog", "name": "assets", "path": "reports/a.pdf"}, "POST /v1/sites/blog/storage/files/assets/download-link", `{"path":"reports/a.pdf"}`, "application/json"},
	}
	for i, tc := range cases {
		_, structured, isErr := resultOf(t, send(t, s, toolCall(tc.tool, tc.args), nil, true))
		if isErr || structured == nil {
			t.Fatalf("%s returned error or no structured data", tc.tool)
		}
		req := up.requests[i]
		if req.Method+" "+req.URL.Path != tc.path || up.bodies[i] != tc.body || req.Header.Get("Content-Type") != tc.contentType {
			t.Errorf("%s: got %s %s, body %q, content type %q", tc.tool, req.Method, req.URL.Path, up.bodies[i], req.Header.Get("Content-Type"))
		}
		if _, err := json.Marshal(structured); err != nil {
			t.Fatal(err)
		}
	}
	_, structured, isErr := resultOf(t, send(t, s, toolCall("storage_file_download_link", cases[4].args), nil, true))
	if isErr || strings.Contains(jsonText(structured), "content_base64") {
		t.Fatal("download link returned binary content")
	}
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_get_kv", map[string]any{"site": "blog", "name": "settings", "key": "secret"}), nil, true))
	if !isErr || !strings.Contains(text, "HTTP 403") || !strings.Contains(text, "forbidden") {
		t.Fatalf("REST refusal was not preserved: %s", text)
	}
}

func TestStorageToolsExcludedFromSelectedHackTeam(t *testing.T) {
	s := newTestServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) }))
	full, _, byName := s.forCaller(Caller{APIKey: "person", Mode: CallerModeEvents, GrantID: "grant", TeamAPIKey: "team"})
	for _, tool := range full {
		if strings.HasPrefix(tool.Name, "storage_") {
			t.Errorf("Hack grant lists %s", tool.Name)
		}
	}
	if _, ok := byName["storage_sql_schema"]; ok {
		t.Fatal("Hack grant can call storage schema tool")
	}
	if _, ok := s.byName["storage_sql_schema"]; !ok {
		t.Fatal("Host connection lost storage schema tool")
	}
}

func TestStorageToolRejectsOversizedInlineFile(t *testing.T) {
	s := newTestServer(&recordingUpstream{})
	args := map[string]any{"site": "blog", "name": "assets", "path": "large.bin", "content_type": "application/octet-stream", "content_base64": strings.Repeat("A", 1400000)}
	text, _, isErr := resultOf(t, send(t, s, toolCall("storage_put_file", args), nil, true))
	if !isErr || !strings.Contains(text, "direct REST") {
		t.Fatalf("oversized upload not refused: %s", text)
	}
}

func TestStorageListForwardsPagination(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/storage/kv/items/keys": func() (int, string) { return 200, `{"items":[],"next_after":""}` },
	}}
	_, _, isErr := resultOf(t, send(t, newTestServer(up), toolCall("storage_list_kv_keys", map[string]any{
		"site": "blog", "name": "items", "prefix": "a/", "after": "a/1", "limit": 10,
	}), nil, true))
	if isErr || len(up.requests) != 1 {
		t.Fatal("list did not reach REST")
	}
	q := up.requests[0].URL.Query()
	if q.Get("prefix") != "a/" || q.Get("after") != "a/1" || q.Get("limit") != "10" {
		t.Fatalf("pagination lost: %v", q)
	}
}
