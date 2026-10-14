package handler

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestConnectorUploadArchive(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "upload-owner")
	token := a.connect(t, owner, a.registerClient(t, testRedirect), testRedirect)["access_token"].(string)
	mint := func(key string) string {
		t.Helper()
		var r resp
		if key == "" {
			r = a.rpc(t, token, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "archive"}})
		} else {
			r = a.do(t, "POST", "/mcp", jsonBody(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "archive"}}}), map[string]string{"X-API-Key": key, "Content-Type": "application/json"})
		}
		_, out, failed := toolResultOf(t, r)
		if failed {
			t.Fatal("mint failed")
		}
		u, err := url.Parse(out["upload_url"].(string))
		if err != nil {
			t.Fatal(err)
		}
		return u.Path
	}
	path := mint("")
	r := a.do(t, "PUT", path, strings.NewReader(tarGzOf(t, 0)), map[string]string{"Content-Type": "application/gzip"})
	if r.status != http.StatusCreated || r.json(t)["active_version"] != float64(1) {
		t.Fatalf("create: %d %s", r.status, r.body)
	}
	if r = a.do(t, "PUT", path, strings.NewReader(tarGzOf(t, 0)), nil); r.status != 404 || r.json(t)["code"] != "upload_link_invalid" {
		t.Fatal("reuse accepted")
	}
	r = a.do(t, "POST", "/v1/me/keys", jsonBody(map[string]any{"name": "upload", "scope": "deploy"}), map[string]string{"X-API-Key": owner.key})
	if r.status != 201 {
		t.Fatal("key mint failed")
	}
	path = mint(r.json(t)["api_key"].(string))
	r = a.do(t, "POST", path, strings.NewReader(tarGzOf(t, 0)), map[string]string{"Content-Type": "application/gzip"})
	if r.status != 200 || r.json(t)["active_version"] != float64(2) {
		t.Fatalf("deploy-only update: %d %s", r.status, r.body)
	}
	r = a.do(t, "GET", "/v1/sites/archive/versions/2/files/index.html", nil, map[string]string{"X-API-Key": owner.key})
	if r.status != 200 || !strings.Contains(string(r.body), "<h1>big</h1>") {
		t.Fatalf("file: %d %s", r.status, r.body)
	}
}
