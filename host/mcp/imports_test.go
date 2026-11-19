package mcp

import (
	"net/http"
	"strings"
	"testing"
)

func TestUpdateSiteKeepArgumentByCallerMode(t *testing.T) {
	s := newTestServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for _, caller := range []Caller{{Mode: CallerModeEvents}, {Mode: CallerModeGroupSite}} {
		tools, _, _ := s.forCaller(caller)
		for _, tool := range tools {
			if tool.Name == "update_site" {
				if tool.InputSchema["properties"].(map[string]any)["keep_other_files"] == nil {
					t.Fatalf("caller mode %q missing keep_other_files", caller.Mode)
				}
			}
		}
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
