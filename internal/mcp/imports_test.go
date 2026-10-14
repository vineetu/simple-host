package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestImportToolsAndHashedLinks(t *testing.T) {
	s := newTestServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	if len(s.tools) != 53 || len(s.hackSiteTools) != 38 {
		t.Fatal("tool counts", len(s.tools), len(s.hackSiteTools))
	}
	for _, caller := range []Caller{{Mode: CallerModeEvents}, {Mode: CallerModeHackSite}} {
		tools, _, _ := s.forCaller(caller)
		for _, tool := range tools {
			if tool.Name == "create_import_link" || tool.Name == "get_import_report" {
				t.Fatal("import tool on Hack")
			}
			if tool.Name == "update_site" {
				if tool.InputSchema["properties"].(map[string]any)["keep_other_files"] == nil {
					t.Fatal("Hack missing partial updates")
				}
			}
		}
	}
	out, err := createImportLink(&call{server: s, caller: Caller{APIKey: "key", Owner: "owner"}}, map[string]any{"site": "blog"})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(out.Structured["import_url"].(string))
	token := strings.TrimPrefix(u.Path, "/import/")
	id := out.Structured["import_id"].(string)
	if raw, err := hex.DecodeString(id); err != nil || len(raw) != 16 {
		t.Fatal("ID is not 128 bits")
	}
	if len(s.imports) != 1 {
		t.Fatal("not stored")
	}
	link := s.imports[sha256.Sum256([]byte(token))]
	if link.ID != id || link.Owner != "owner" {
		t.Fatal("hash storage")
	}
	if _, status := s.ReserveImport(token); status != 0 {
		t.Fatal(status)
	}
	if _, status := s.ReserveImport(token); status != 409 {
		t.Fatal(status)
	}
	s.ReleaseImport(token, false)
	if _, status := s.ReserveImport(token); status != 0 {
		t.Fatal("release", status)
	}
	s.ReleaseImport(token, true)
	if _, status := s.ReserveImport(token); status != 404 {
		t.Fatal("spent", status)
	}
	for i := 0; i < 21; i++ {
		_, err := createImportLink(&call{server: s, caller: Caller{APIKey: fmt.Sprint(i), Owner: "owner"}}, map[string]any{"site": "blog"})
		if (i == 20) != (err != nil) {
			t.Fatal("cap across credentials", i, err)
		}
	}
	if _, err := createUploadLink(&call{server: s, caller: Caller{APIKey: "key", Owner: "owner"}}, map[string]any{"site": "blog"}); err != nil {
		t.Fatal("import links counted against upload cap", err)
	}
	for hash, link := range s.imports {
		link.Expires = time.Now().Add(-time.Minute)
		s.imports[hash] = link
	}
	if _, err := createImportLink(&call{server: s, caller: Caller{APIKey: "key", Owner: "owner"}}, map[string]any{"site": "blog"}); err != nil || len(s.imports) != 1 {
		t.Fatal("expiry sweep", err)
	}
}
func TestUpdateSiteKeepArguments(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){"PUT /v1/sites/blog/files": func() (int, string) {
		return 200, `{"name":"blog","site_url":"https://blog.person.simple-host.app/","active_version":2}`
	}}}
	s := newTestServer(up)
	_, _, failed := resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog", "files": map[string]any{"about.html": "new"}, "keep_other_files": true, "remove_files": []string{"old.html"}, "publish": false}), nil, true))
	if failed {
		t.Fatal("patch refused")
	}
	if len(up.requests) < 1 {
		t.Fatal("no deploy")
	}
	req := up.requests[0]
	if req.URL.RawQuery != "publish=false&keep=1" || !strings.Contains(up.bodies[0], `"remove_files":["old.html"]`) {
		t.Fatal(req.URL.String(), up.bodies)
	}
	_, _, failed = resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog", "files": map[string]any{"index.html": "new"}, "remove_files": []string{"old.html"}}), nil, true))
	if !failed {
		t.Fatal("remove without keep accepted")
	}
}
