package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestHackWebMCPUsesRESTPermissions(t *testing.T) {
	a := newTeamSiteApp(t)
	org, member, outsider := a.newPerson(t, "web-mcp-org"), a.newPerson(t, "web-mcp-member"), a.newPerson(t, "web-mcp-outsider")
	slug := a.makeEvent(t, org)
	a.join(t, slug, member, org)
	call := func(key, name string, args map[string]any) (string, map[string]any, bool) {
		t.Helper()
		r := a.do(t, http.MethodPost, "/mcp", jsonBody(map[string]any{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": name, "arguments": args},
		}), map[string]string{"X-API-Key": key, "Content-Type": "application/json", "MCP-Protocol-Version": "2025-06-18"})
		return toolResultOf(t, r)
	}
	wantOK := func(key, name string, args map[string]any) map[string]any {
		t.Helper()
		text, data, bad := call(key, name, args)
		if bad || data == nil {
			t.Fatalf("%s failed: %s", name, text)
		}
		return data
	}
	wantDenied := func(key, name string, args map[string]any, code string) {
		t.Helper()
		text, _, bad := call(key, name, args)
		if !bad || !strings.Contains(text, code) {
			t.Fatalf("%s: wanted %s, got %s", name, code, text)
		}
	}
	if state := wantOK(org.key, "hack_get_event_website", map[string]any{"slug": slug}); state["mode"] != "builtin" || state["has_custom_page"] != false {
		t.Fatalf("default website: %v", state)
	}
	wantDenied(member.key, "hack_get_event_website", map[string]any{"slug": slug}, "event_not_found")
	wantDenied(outsider.key, "hack_publish_event_website", map[string]any{"slug": slug, "files": map[string]any{"index.html": "<h1>out</h1>"}}, "event_not_found")
	wantDenied(org.key, "hack_publish_event_website", map[string]any{"slug": slug, "files": map[string]any{"other.html": "no index"}}, "index.html")
	wantDenied(org.key, "hack_set_event_website_mode", map[string]any{"slug": slug, "mode": "custom"}, "website_not_published")
	published := wantOK(org.key, "hack_publish_event_website", map[string]any{"slug": slug, "files": map[string]any{"index.html": "<h1>MCP event page</h1>"}})
	if published["name"] != slug {
		t.Fatalf("website response: %v", published)
	}
	wantOK(org.key, "hack_publish_event_website", map[string]any{"slug": slug, "files": map[string]any{"index.html": "<h1>Version two</h1>"}})
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	page := []byte("<h1>Archived event page</h1>")
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0o644, Size: int64(len(page))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(page); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	wantDenied(org.key, "hack_publish_event_website", map[string]any{"slug": slug, "archive_base64": base64.StdEncoding.EncodeToString(archive.Bytes()), "files": map[string]any{"index.html": "mixed"}}, "cannot be combined")
	wantOK(org.key, "hack_publish_event_website", map[string]any{"slug": slug, "archive_base64": base64.StdEncoding.EncodeToString(archive.Bytes())})
	if state := wantOK(org.key, "hack_get_event_website", map[string]any{"slug": slug}); state["mode"] != "custom" || state["has_custom_page"] != true {
		t.Fatalf("custom website: %v", state)
	}
	wantDenied(member.key, "hack_set_event_website_mode", map[string]any{"slug": slug, "mode": "builtin"}, "event_not_found")
	if state := wantOK(org.key, "hack_set_event_website_mode", map[string]any{"slug": slug, "mode": "builtin"}); state["mode"] != "builtin" {
		t.Fatalf("switch: %v", state)
	}
	if state := wantOK(org.key, "hack_set_event_website_mode", map[string]any{"slug": slug, "mode": "custom"}); state["mode"] != "custom" {
		t.Fatalf("restore: %v", state)
	}
	public := wantOK(outsider.key, "hack_get_public_event", map[string]any{"slug": slug})
	if public["slug"] != slug || public["join_code"] != nil || public["contact_email"] != nil {
		t.Fatalf("public feed: %v", public)
	}
	wantDenied(member.key, "hack_set_event_icon", map[string]any{"slug": slug, "content_type": "image/png", "image_base64": base64.StdEncoding.EncodeToString(shotShown)}, "event_not_found")
	wantDenied(org.key, "hack_set_event_icon", map[string]any{"slug": slug, "content_type": "image/png", "image_base64": base64.StdEncoding.EncodeToString([]byte("<svg/>"))}, "invalid_icon")
	icon := wantOK(org.key, "hack_set_event_icon", map[string]any{"slug": slug, "content_type": "image/png", "image_base64": base64.StdEncoding.EncodeToString(shotShown)})
	if icon["content_type"] != "image/png" {
		t.Fatalf("icon: %v", icon)
	}
	wantDenied(outsider.key, "hack_clear_event_icon", map[string]any{"slug": slug}, "event_not_found")
	wantOK(org.key, "hack_clear_event_icon", map[string]any{"slug": slug})
	if pref := wantOK(org.key, "hack_get_preferences", map[string]any{}); pref["theme"] != "system" {
		t.Fatalf("default pref: %v", pref)
	}
	wantDenied(org.key, "hack_set_preferences", map[string]any{"theme": "sepia"}, "invalid_theme")
	wantOK(org.key, "hack_set_preferences", map[string]any{"theme": "dark", "judge_walkthrough_done": true})
	if pref := wantOK(org.key, "hack_get_preferences", map[string]any{}); pref["theme"] != "dark" || pref["judge_walkthrough_done"] != true {
		t.Fatalf("saved pref: %v", pref)
	}
	if pref := wantOK(member.key, "hack_get_preferences", map[string]any{}); pref["theme"] != "system" {
		t.Fatalf("member pref isolation: %v", pref)
	}
	a.startTeam(t, slug, "MCP member team", member)
	if names := mcpToolNames(t, a.mcpWithKey(t, a.teamKey(t, slug, member))); hasTool(names, "hack_publish_event_website") || hasTool(names, "hack_set_event_icon") {
		t.Fatalf("team key exposes event tools: %v", names)
	}
}
