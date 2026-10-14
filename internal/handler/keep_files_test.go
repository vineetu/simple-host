package handler

import (
	"context"
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/internal/wpimport"
)

func TestKeepOtherFiles(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "keep-files")
	headers := map[string]string{"X-API-Key": owner.key, "Content-Type": "application/json"}
	image := []byte{0x89, 'P', 'N', 'G', 0, 1, 2, 0xff}
	r := a.do(t, "POST", "/v1/sites/big/files", jsonBody(map[string]any{"files": map[string]string{"index.html": "home", "about.html": "old", "remove.html": "drop"}, "files_base64": map[string]string{"photo.png": base64.StdEncoding.EncodeToString(image)}}), headers)
	if r.status != 201 {
		t.Fatal(r.status, string(r.body))
	}
	r = a.do(t, "PUT", "/v1/sites/big/files?keep=1", jsonBody(map[string]any{"files": map[string]string{"about.html": "new"}, "remove_files": []string{"remove.html"}}), headers)
	if r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
	for _, file := range []struct{ path, want string }{{"index.html", "home"}, {"about.html", "new"}, {"photo.png", string(image)}} {
		r = a.do(t, "GET", "/v1/sites/big/versions/2/files/"+file.path, nil, headers)
		if r.status != 200 || string(r.body) != file.want {
			t.Fatal(file.path, r.status, string(r.body))
		}
	}
	if r = a.do(t, "GET", "/v1/sites/big/versions/2/files/remove.html", nil, headers); r.status != 404 {
		t.Fatal("removed file kept")
	}
	for _, tc := range []struct {
		path   string
		remove []string
	}{{"/v1/sites/big/files", []string{"about.html"}}, {"/v1/sites/big/files?keep=1", []string{"../outside"}}, {"/v1/sites/big/files?keep=1", []string{`a\..\outside`}}} {
		r = a.do(t, "PUT", tc.path, jsonBody(map[string]any{"files": map[string]string{"index.html": "new"}, "remove_files": tc.remove}), headers)
		if r.status != 400 {
			t.Fatal(tc.path, r.status, string(r.body))
		}
	}
	// Keeping on a missing site preserves today's missing-site response.
	r = a.do(t, "PUT", "/v1/sites/missing/files?keep=1", jsonBody(map[string]any{"files": map[string]string{"about.html": "new"}}), headers)
	if r.status != 404 {
		t.Fatal(r.status, string(r.body))
	}
	// A site without a live version still accepts the ordinary complete first version.
	if _, err := a.database.Exec(`UPDATE sites SET active_version = 0 WHERE name = 'big' AND user_id = (SELECT id FROM users WHERE username = $1)`, owner.email); err != nil {
		t.Fatal(err)
	}
	r = a.do(t, "PUT", "/v1/sites/big/files?keep=1", jsonBody(map[string]any{"files": map[string]string{"index.html": "fresh"}}), headers)
	if r.status != 200 {
		t.Fatal(r.status, string(r.body))
	}
}
func TestImportRealDeploy(t *testing.T) {
	a := newConnectorApp(t)
	owner := a.newPerson(t, "import-owner")
	client := a.registerClient(t, testRedirect)
	token := a.connect(t, owner, client, testRedirect)["access_token"].(string)
	fakeImport(t, func(ctx context.Context, in wpimport.Input) (*wpimport.Result, error) {
		if len(in.WXR) != 1 {
			t.Error("missing WXR")
		}
		b, err := io.ReadAll(in.WXR[0])
		if err != nil || !strings.Contains(string(b), "<rss") {
			t.Error("XML unreadable")
		}
		return &wpimport.Result{Files: map[string][]byte{"index.html": []byte("<h1>Moved</h1>"), "about.html": []byte("<p>About</p>")}, Report: wpimport.Report{Pages: 2}}, nil
	})
	r := a.rpc(t, token, "tools/call", map[string]any{"name": "create_import_link", "arguments": map[string]any{"site": "moved"}})
	_, out, failed := toolResultOf(t, r)
	if failed {
		t.Fatal(string(r.body))
	}
	u, _ := url.Parse(out["import_url"].(string))
	b, ct := importMultipart(t, testWXR)
	r = a.do(t, "POST", "/v1/imports/"+strings.TrimPrefix(u.Path, "/import/"), b, map[string]string{"Content-Type": ct})
	if r.status != 202 {
		t.Fatal(r.status, string(r.body))
	}
	id := out["import_id"].(string)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r = a.do(t, "GET", "/v1/imports/status/"+id, nil, nil)
		state := r.json(t)["state"]
		if state == "failed" {
			t.Fatal(string(r.body))
		}
		if state == "done" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if r.json(t)["state"] != "done" {
		t.Fatal("did not publish", string(r.body))
	}
	for _, file := range []string{"index.html", "about.html"} {
		r = a.do(t, "GET", "/v1/sites/moved/versions/1/files/"+file, nil, map[string]string{"X-API-Key": owner.key})
		if r.status != 200 {
			t.Fatal(file, r.status, string(r.body))
		}
	}
	r = a.rpc(t, token, "tools/call", map[string]any{"name": "get_import_report", "arguments": map[string]any{"import_id": id}})
	_, out, failed = toolResultOf(t, r)
	if failed || out["report"].(map[string]any)["pages"] != float64(2) {
		t.Fatal(string(r.body))
	}
	// Disconnecting the creating app kills another outstanding import link.
	r = a.rpc(t, token, "tools/call", map[string]any{"name": "create_import_link", "arguments": map[string]any{"site": "moved"}})
	_, out, failed = toolResultOf(t, r)
	if failed {
		t.Fatal(string(r.body))
	}
	u, _ = url.Parse(out["import_url"].(string))
	a.do(t, "DELETE", "/v1/me/connections/"+client, nil, map[string]string{"X-API-Key": owner.key})
	b, ct = importMultipart(t, testWXR)
	r = a.do(t, "POST", "/v1/imports/"+strings.TrimPrefix(u.Path, "/import/"), b, map[string]string{"Content-Type": ct})
	if r.status != 403 || r.json(t)["code"] != "import_link_signed_out" {
		t.Fatal(r.status, string(r.body))
	}
}
