package servertest

import (
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
	// The event website is the site named the event's id, published by an
	// organiser with owner set to the event.
	var site string
	if err := a.database.QueryRow(`SELECT id FROM events WHERE slug=$1`, slug).Scan(&site); err != nil {
		t.Fatal(err)
	}
	page := map[string]any{"site": site, "owner": slug, "files": map[string]any{"index.html": "<h1>MCP event page</h1>"}}
	wantDenied(member.key, "create_site", page, "owner_not_found")
	wantDenied(outsider.key, "create_site", page, "owner_not_found")
	wantDenied(org.key, "hack_set_event_website_mode", map[string]any{"slug": slug, "mode": "custom"}, "website_not_published")
	wantOK(org.key, "create_site", page)
	if state := wantOK(org.key, "hack_set_event_website_mode", map[string]any{"slug": slug, "mode": "custom"}); state["mode"] != "custom" {
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
}
