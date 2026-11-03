package mcp

import (
	"strings"
	"testing"
)

// The author of a version is shown in list_versions' text only: the
// structured output keeps the schema clients cached, which refuses unknown
// keys. A version without an author says nothing about one.
func TestListVersionsAuthorInTextOnly(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/sites/blog/versions": func() (int, string) {
		return 200, `[{"version_number":1,"created_at":"2026-09-01T00:00:00Z","is_active":true,"status":"active","author":""},` +
			`{"version_number":2,"created_at":"2026-09-02T00:00:00Z","is_active":false,"status":"ready","author":"Priya from design"}]`
	}}}
	text, structured, failed := resultOf(t, send(t, newTestServer(up), toolCall("list_versions", map[string]any{"site": "blog"}), nil, true))
	if failed {
		t.Fatal(text)
	}
	if !strings.Contains(text, `"author": "Priya from design"`) || strings.Count(text, `"author"`) != 1 {
		t.Fatalf("text does not name the one author once: %s", text)
	}
	if !strings.Contains(text, `"not_yet_live": true`) {
		t.Fatalf("text lost not_yet_live: %s", text)
	}
	for _, raw := range structured["versions"].([]any) {
		if _, has := raw.(map[string]any)["author"]; has {
			t.Fatalf("structured output carries author: %v", structured)
		}
	}
	if err := ValidateOutput(outputSchemasWithLimits(DefaultLimits())["list_versions"], structured, nil); err != nil {
		t.Fatal(err)
	}
}

// update_site passes author to the deploy route as a query parameter, after
// publish and keep, URL-encoded; without author nothing is sent, and long
// ones are refused before any request.
func TestUpdateSiteAuthorQuery(t *testing.T) {
	up := &recordingUpstream{answers: map[string]func() (int, string){"PUT /v1/sites/blog/files": func() (int, string) {
		return 200, `{"name":"blog","site_url":"https://blog.person.simple-host.app/","active_version":2}`
	}}}
	s := newTestServer(up)
	files := map[string]any{"index.html": "new"}
	if _, _, failed := resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog", "files": files, "publish": false, "author": "Priya & co"}), nil, true)); failed {
		t.Fatal("author refused")
	}
	if got := up.requests[0].URL.RawQuery; got != "publish=false&author=Priya+%26+co" {
		t.Fatal(got)
	}
	if text, _, failed := resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog", "files": files}), nil, true)); failed {
		t.Fatal("plain update refused:", text)
	}
	if got := up.requests[1].URL.RawQuery; got != "" {
		t.Fatal("author sent without being given:", got)
	}
	text, _, failed := resultOf(t, send(t, s, toolCall("update_site", map[string]any{"site": "blog", "files": files, "author": strings.Repeat("a", 81)}), nil, true))
	if !failed || !strings.Contains(text, "80") || len(up.requests) != 2 {
		t.Fatalf("long author accepted: %s (%d requests)", text, len(up.requests))
	}
}
