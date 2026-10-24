package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestPreviewFrameDomainsFollowInstance(t *testing.T) {
	for _, tc := range []struct {
		name, api, content string
		bases, want        []string
	}{
		{"hosted", "simple-host.app", "https://sites.simple-host.app", []string{"simple-host.app"}, []string{"https://*.simple-host.app"}},
		{"box", "box.example.test", "https://content.example.test", []string{"box.example.test"}, []string{"https://*.box.example.test", "https://content.example.test"}},
		{"alternate base", "box.example.test", "https://sites.box.example.test", []string{"pages.example.test", "box.example.test"}, []string{"https://*.pages.example.test", "https://*.box.example.test"}},
		{"no dashboard frame", "box.example.test", "https://box.example.test", nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_SITE_PREVIEW", "on")
			s := NewServer(Config{APIHost: tc.api, ContentOrigin: tc.content, SiteBases: tc.bases})
			if got := s.previewFrameDomains(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("frame domains = %v, want %v", got, tc.want)
			}
			// The listed CSP and the config in resources/read must match.
			resources := s.appResources(Caller{})
			meta := resources[0].(map[string]any)["_meta"].(map[string]any)
			csp := meta["ui"].(map[string]any)["csp"].(map[string][]string)
			if !reflect.DeepEqual(csp["frameDomains"], tc.want) {
				t.Fatal(csp)
			}
			reply := s.readApp(request{ID: json.RawMessage(`1`), Params: json.RawMessage(`{"uri":"ui://simple-host/site-preview"}`)}, Caller{})
			b, _ := json.Marshal(reply)
			var wire struct {
				Result struct{ Contents []struct{ Text string } }
			}
			if err := json.Unmarshal(b, &wire); err != nil {
				t.Fatal(err)
			}
			want, _ := json.Marshal(tc.want)
			if !strings.Contains(wire.Result.Contents[0].Text, `"frameDomains":`+string(want)) {
				t.Fatal("resource HTML does not use the listed frame origins")
			}
		})
	}
}

func TestSitePreviewGatesAndResource(t *testing.T) {
	s := newTestServer(nil)
	t.Setenv("MCP_APP_PHOTO_PICKER", "off")
	t.Setenv("MCP_APP_SITE_PREVIEW", "off")
	before, bareBefore, _ := s.forCaller(Caller{})
	baseline, _ := json.Marshal(before)
	bareBaseline, _ := json.Marshal(bareBefore)
	for _, flag := range []string{"", "off", "unknown", "on", "accounts:alice"} {
		t.Setenv("MCP_APP_SITE_PREVIEW", flag)
		for _, handle := range []string{"alice", "bob", ""} {
			caller := Caller{Handle: handle}
			enabled := appFlag(flag, handle)
			full, bare, _ := s.forCaller(caller)
			if len(full) != 20 {
				t.Fatal("changed core inventory")
			}
			for _, list := range [][]Tool{full, bare} {
				for _, tool := range list {
					if (tool.Meta["ui"] != nil) != (enabled && tool.Name == "get_site") {
						t.Fatalf("%s %s %s metadata", flag, handle, tool.Name)
					}
					if tool.Name == "get_site" && tool.OutputSchema != nil {
						_, hasPreview := tool.OutputSchema["properties"].(map[string]any)["site_preview"]
						if hasPreview != enabled {
							t.Fatal("preview output schema gate")
						}
					}
				}
			}
			if !enabled {
				got, _ := json.Marshal(full)
				gotBare, _ := json.Marshal(bare)
				if string(got) != string(baseline) || string(gotBare) != string(bareBaseline) {
					t.Fatal("off tools/list changed")
				}
			}
			resources := s.appResources(caller)
			if (len(resources) == 1) != enabled {
				t.Fatal("resources/list gate")
			}
			req := request{ID: json.RawMessage(`1`), Method: "resources/read", Params: json.RawMessage(`{"uri":"ui://simple-host/site-preview"}`)}
			reply := s.dispatch(httptest.NewRequest("POST", "/mcp", nil), req, caller)
			b, _ := json.Marshal(reply)
			var wire map[string]any
			_ = json.Unmarshal(b, &wire)
			if !enabled {
				if wire["error"] == nil {
					t.Fatal("off resource read allowed")
				}
				continue
			}
			item := wire["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
			if item["mimeType"] != appMIME {
				t.Fatal(item["mimeType"])
			}
			ui := item["_meta"].(map[string]any)["ui"].(map[string]any)
			csp := ui["csp"].(map[string]any)
			if !reflect.DeepEqual(csp["frameDomains"], []any{"https://*.simple-host.app"}) || len(csp["connectDomains"].([]any)) != 0 || len(csp["resourceDomains"].([]any)) != 0 {
				t.Fatal(csp)
			}
			if ui["permissions"].(map[string]any)["clipboardWrite"] == nil {
				t.Fatal("clipboard permission missing")
			}
			page := item["text"].(string)
			for _, needle := range []string{"ui/initialize", "ui/open-link", "ui/request-display-mode", "Back to card", "navigator.clipboard.writeText", "1500", "textContent"} {
				if !strings.Contains(page, needle) {
					t.Fatal("missing " + needle)
				}
			}
			if strings.Contains(page, "__SCREEN__") || strings.Contains(page, "innerHTML") || strings.Contains(page, "'pip'") {
				t.Fatal("unexpected page content")
			}
		}
	}
	t.Setenv("MCP_APP_SITE_PREVIEW", "on")
	for _, mode := range []string{CallerModeEvents, CallerModeHackSite} {
		if len(s.enabledScreens(Caller{Mode: mode})) != 0 {
			t.Fatal("Hack offered preview")
		}
	}
	t.Setenv("MCP_APP_SITE_PREVIEW", "off")
	after, _, _ := s.forCaller(Caller{})
	b, _ := json.Marshal(after)
	if string(b) != string(baseline) {
		t.Fatal("cached schema mutated")
	}
}

func TestSitePreviewStructuredOnlyAndAccess(t *testing.T) {
	for _, tc := range []struct {
		name, visibility, access           string
		passcode, offline, suspended, want bool
	}{
		{name: "public", visibility: "public", access: "anyone", want: true},
		{name: "unlisted", visibility: "unlisted", access: "anyone", want: true},
		{name: "private", visibility: "private", access: "anyone"},
		{name: "passcode", visibility: "public", access: "anyone", passcode: true},
		{name: "viewers", visibility: "public", access: "specific"},
		{name: "unknown visibility", access: "anyone"},
		{name: "unknown access", visibility: "public"},
		{name: "offline", visibility: "public", access: "anyone", offline: true},
		{name: "suspended", visibility: "public", access: "anyone", suspended: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal([]map[string]any{{"name": "blog", "site_url": "https://blog.alice.simple-host.app/", "custom_domain": "blog.example.org", "domain_status": "active", "active_version": 1, "visibility": tc.visibility, "access": tc.access, "passcode_protected": tc.passcode, "offline": tc.offline, "suspended": tc.suspended}})
			up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/sites": func() (int, string) { return 200, string(raw) }}}
			s := newTestServer(up)
			read := func() map[string]any {
				return decode(t, send(t, s, toolCall("get_site", map[string]any{"site": "blog"}), nil, true))["result"].(map[string]any)
			}
			t.Setenv("MCP_APP_SITE_PREVIEW", "off")
			off := read()
			t.Setenv("MCP_APP_SITE_PREVIEW", "on")
			on := read()
			if !reflect.DeepEqual(off["content"], on["content"]) {
				t.Fatal("text result changed")
			}
			data := on["structuredContent"].(map[string]any)
			preview := data["site_preview"].(map[string]any)
			if preview["open_to_anyone"] != tc.want || preview["canonical_url"] != "https://blog.alice.simple-host.app/" || data["url"] != "https://blog.example.org/" {
				t.Fatal(data)
			}
			full, _, _ := s.forCaller(Caller{})
			for _, tool := range full {
				if tool.Name == "get_site" {
					if err := ValidateOutput(tool.OutputSchema, data, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			delete(data, "site_preview")
			if !reflect.DeepEqual(data, off["structuredContent"]) {
				t.Fatal("existing structured fields changed")
			}
			t.Setenv("MCP_APP_SITE_PREVIEW", "accounts:alice")
			gated := read() // send's caller has no handle.
			if !reflect.DeepEqual(gated, off) {
				t.Fatal("non-allowlisted result changed")
			}
		})
	}
}
