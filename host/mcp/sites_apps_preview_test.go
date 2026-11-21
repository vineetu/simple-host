package mcp

import (
	"encoding/json"
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
		{"no dashboard frame", "box.example.test", "https://box.example.test", nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_SITE_PREVIEW", "on")
			s := NewServer(testConfig(Config{APIHost: tc.api, ContentOrigin: tc.content, SiteBases: tc.bases}))
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

func TestSitePreviewStructuredOnlyAndAccess(t *testing.T) {
	for _, tc := range []struct {
		name   string
		open   bool
		active int
		want   bool
	}{
		{name: "open", open: true, active: 1, want: true},
		{name: "not open", active: 1},
		{name: "never deployed", open: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal([]map[string]any{{"name": "blog", "site_url": "https://blog.alice.simple-host.app/", "custom_domain": "blog.example.org", "domain_status": "active", "live_url": "https://blog.example.org/", "active_version": tc.active, "open_to_anyone": tc.open}})
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
