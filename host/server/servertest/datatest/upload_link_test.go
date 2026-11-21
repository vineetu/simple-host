package datatest

import (
	"github.com/vsriram/simple-host/host/server/servertest"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestConnectorUploadArchive(t *testing.T) {
	a := servertest.NewConnectorApp(t)
	owner := a.NewPerson(t, "upload-owner")
	clientID := a.RegisterClient(t, servertest.TestRedirect)
	token := a.Connect(t, owner, clientID, servertest.TestRedirect)["access_token"].(string)
	mint := func(key string) string {
		t.Helper()
		var r servertest.Resp
		if key == "" {
			r = a.Rpc(t, token, "tools/call", map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "archive", "archive": true}})
		} else {
			r = a.Do(t, "POST", "/mcp", servertest.JSONBody(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "create_upload_link", "arguments": map[string]any{"site": "archive", "archive": true}}}), map[string]string{"X-API-Key": key, "Content-Type": "application/json"})
		}
		_, out, failed := servertest.ToolResultOf(t, r)
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
	r := a.Do(t, "PUT", path, strings.NewReader(servertest.TarGzOf(t, 0)), map[string]string{"Content-Type": "application/gzip"})
	if r.Status != http.StatusCreated || r.JSON(t)["active_version"] != float64(1) {
		t.Fatalf("create: %d %s", r.Status, r.Body)
	}
	if r = a.Do(t, "PUT", path, strings.NewReader(servertest.TarGzOf(t, 0)), nil); r.Status != 404 || r.JSON(t)["code"] != "upload_link_invalid" {
		t.Fatal("reuse accepted")
	}
	r = a.Do(t, "POST", "/v1/me/keys", servertest.JSONBody(map[string]any{"name": "upload", "scope": "deploy"}), map[string]string{"X-API-Key": owner.Key})
	if r.Status != 201 {
		t.Fatal("key mint failed")
	}
	keyID := r.JSON(t)["id"].(string)
	apiKey := r.JSON(t)["api_key"].(string)
	path = mint(apiKey)
	r = a.Do(t, "POST", path, strings.NewReader(servertest.TarGzOf(t, 0)), map[string]string{"Content-Type": "application/gzip"})
	if r.Status != 200 || r.JSON(t)["active_version"] != float64(2) {
		t.Fatalf("deploy-only update: %d %s", r.Status, r.Body)
	}
	r = a.Do(t, "GET", "/v1/sites/archive/versions/2/files/index.html", nil, map[string]string{"X-API-Key": owner.Key})
	if r.Status != 200 || !strings.Contains(string(r.Body), "<h1>big</h1>") {
		t.Fatalf("file: %d %s", r.Status, r.Body)
	}
	assertEnded := func(path string) {
		t.Helper()
		r := a.Do(t, "PUT", path, strings.NewReader(servertest.TarGzOf(t, 0)), nil)
		if r.Status != 403 || r.JSON(t)["code"] != "upload_link_signed_out" {
			t.Fatalf("ended link: %d %s", r.Status, r.Body)
		}
		var version, versions int
		if err := a.Database.QueryRow(`SELECT s.active_version, (SELECT count(*) FROM versions v WHERE v.site_id = s.id) FROM sites s JOIN users u ON u.id = s.user_id WHERE u.username = $1 AND s.name = 'archive'`, owner.Email).Scan(&version, &versions); err != nil {
			t.Fatal(err)
		}
		if version != 2 || versions != 2 {
			t.Fatalf("ended link deployed: active=%d versions=%d", version, versions)
		}
	}
	path = mint("")
	if r := a.Do(t, "DELETE", "/v1/me/connections/"+clientID, nil, map[string]string{"X-API-Key": owner.Key}); r.Status != 204 {
		t.Fatalf("disconnect: %d %s", r.Status, r.Body)
	}
	assertEnded(path)
	path = mint(apiKey)
	if r := a.Do(t, "DELETE", "/v1/me/keys/"+keyID, nil, map[string]string{"X-API-Key": owner.Key}); r.Status != 204 {
		t.Fatalf("revoke: %d %s", r.Status, r.Body)
	}
	assertEnded(path)
}
