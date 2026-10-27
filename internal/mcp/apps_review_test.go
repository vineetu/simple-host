package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPickerPhotoLinkCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, flag, handle, mode            string
		modern, present, ui, archive, token bool
	}{
		{name: "legacy picker", flag: "on"},
		{name: "modern UI", flag: "on", modern: true, present: true, ui: true},
		{name: "modern non UI", flag: "on", modern: true, present: true, token: true},
		{name: "legacy ignores request capabilities", flag: "on", present: true},
		{name: "off", flag: "off", token: true},
		{name: "account enabled", flag: "accounts:alice", handle: "alice"},
		{name: "other account", flag: "accounts:alice", handle: "bob", token: true},
		{name: "archive unchanged", flag: "on", archive: true, token: true},
		{name: "Hack unchanged", flag: "on", mode: CallerModeHackSite, token: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MCP_APP_PHOTO_PICKER", tc.flag)
			s := newTestServer(nil)
			for _, wrapped := range []bool{false, true} {
				args := map[string]any{"site": "blog"}
				if tc.archive {
					args["archive"] = true
				}
				params := map[string]any{"name": "create_upload_link", "arguments": args}
				if wrapped {
					params["name"] = "run_tool"
					params["arguments"] = map[string]any{"name": "create_upload_link", "arguments": args}
					if tc.mode != "" {
						continue
					}
				}
				if tc.present {
					caps := map[string]any{}
					if tc.ui {
						caps["extensions"] = map[string]any{"io.modelcontextprotocol/ui": map[string]any{"mimeTypes": []string{appMIME}}}
					}
					params["_meta"] = map[string]any{"io.modelcontextprotocol/clientCapabilities": caps}
				}
				raw, _ := json.Marshal(params)
				before := len(s.uploads)
				reply := s.callTool(httptest.NewRequest("POST", "/mcp", nil), request{Params: raw}, Caller{Handle: tc.handle, Mode: tc.mode}, tc.modern)
				b, _ := json.Marshal(reply.Result)
				var result map[string]any
				json.Unmarshal(b, &result)
				if result["isError"] == true {
					t.Fatalf("tool failed: %s", b)
				}
				out := result["structuredContent"].(map[string]any)
				// Hosts draw the picker only for the listed tool, so run_tool gets the link.
				token := tc.token || wrapped
				if tc.archive || tc.mode != "" {
					if out["upload_url"] == nil || out["photos_url"] != nil {
						t.Fatal("archive result changed")
					}
				} else {
					address := out["photos_url"].(string)
					if strings.Contains(address, "#t=") != token {
						t.Fatal("wrong token presence")
					}
					if !token && address != "https://"+s.cfg.APIHost+"/photos?site=blog&account="+url.QueryEscape(tc.handle) {
						t.Fatal("wrong sign-in address")
					}
					if result["content"].([]any)[0].(map[string]any)["text"] != jsonText(out) {
						t.Fatal("text and structured result differ")
					}
					if (len(out) == 5) != !token || (out["picker"] == "shown") != !token {
						t.Fatal("photo output fields changed")
					}
				}
				want := before
				if token {
					want++
				}
				if len(s.uploads) != want {
					t.Fatal("unexpected grant allocation")
				}
			}
		})
	}
}
