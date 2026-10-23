package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppFlag(t *testing.T) {
	for _, tc := range []struct {
		value, handle string
		want          bool
	}{
		{"", "alice", false}, {"off", "alice", false}, {"true", "alice", false},
		{"on", "", true}, {"on", "alice", true}, {"accounts:alice,bob", "alice", true},
		{"accounts: alice, bob ", "bob", true}, {"accounts:alice", "ali", false},
		{"accounts:", "", false}, {"accounts:alice", "", false},
	} {
		if got := appFlag(tc.value, tc.handle); got != tc.want {
			t.Errorf("%q %q: %v", tc.value, tc.handle, got)
		}
	}
}

func TestAppsPerCallerAndCache(t *testing.T) {
	s := newTestServer(nil)
	t.Setenv("MCP_APP_PHOTO_PICKER", "off")
	full, bare, _ := s.forCaller(Caller{})
	original, _ := json.Marshal(full)
	originalBare, _ := json.Marshal(bare)
	for _, flag := range []string{"", "off", "unknown", "accounts:alice", "on"} {
		t.Setenv("MCP_APP_PHOTO_PICKER", flag)
		for _, handle := range []string{"alice", "bob", ""} {
			c := Caller{Handle: handle}
			want := appFlag(flag, handle)
			full, bare, _ := s.forCaller(c)
			if len(full) != 20 {
				t.Fatalf("tool count %d", len(full))
			}
			for _, list := range [][]Tool{full, bare} {
				for _, tool := range list {
					if got := tool.Meta["ui"] != nil; got != (want && tool.Name == "create_upload_link") {
						t.Errorf("%s %s %s metadata %v", flag, handle, tool.Name, got)
					}
				}
			}
			if !want {
				got, _ := json.Marshal(full)
				gotBare, _ := json.Marshal(bare)
				if string(got) != string(original) || string(gotBare) != string(originalBare) {
					t.Fatal("off listing changed")
				}
			}
			if (s.capabilities(c)["resources"] != nil) != want {
				t.Fatal("resource capability gate")
			}
			if (len(s.appResources(c)) == 1) != want {
				t.Fatal("resource listing gate")
			}
		}
	}
	t.Setenv("MCP_APP_PHOTO_PICKER", "on")
	for _, mode := range []string{CallerModeEvents, CallerModeHackSite} {
		if len(s.enabledScreens(Caller{Mode: mode})) != 0 {
			t.Fatal("Hack offered Apps")
		}
	}
	// Re-reading after an enabled caller cannot contaminate any cached inventory.
	t.Setenv("MCP_APP_PHOTO_PICKER", "off")
	got, _, _ := s.forCaller(Caller{})
	b, _ := json.Marshal(got)
	if string(b) != string(original) {
		t.Fatal("cache mutated")
	}
}

func TestAppsWire(t *testing.T) {
	s := newTestServer(nil)
	for _, enabled := range []bool{false, true} {
		flag := "off"
		if enabled {
			flag = "on"
		}
		t.Setenv("MCP_APP_PHOTO_PICKER", flag)
		for _, version := range []string{"2025-03-26", "2025-11-25", "2026-07-28"} {
			headers := map[string]string{"MCP-Protocol-Version": version, "Mcp-Method": "resources/list"}
			// Modern requests require per-request client capabilities.
			meta := `,"_meta":{"io.modelcontextprotocol/clientCapabilities":{}}`
			listing := decode(t, send(t, s, `{"jsonrpc":"2.0","id":1,"method":"resources/list","params":{`+strings.TrimPrefix(meta, ",")+`}}`, headers, true))
			if listing["error"] != nil {
				t.Fatalf("%v", listing)
			}
			items := listing["result"].(map[string]any)["resources"].([]any)
			if (len(items) == 1) != enabled {
				t.Fatalf("%s resources %v", version, items)
			}
			headers["Mcp-Method"] = "resources/read"
			headers["Mcp-Name"] = "ui://simple-host/photo-picker"
			read := decode(t, send(t, s, `{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"ui://simple-host/photo-picker"`+meta+`}}`, headers, true))
			if !enabled {
				if read["error"] == nil {
					t.Fatal("read when off")
				}
				continue
			}
			if read["error"] != nil {
				t.Fatalf("%v", read)
			}
			item := read["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
			if item["mimeType"] != appMIME || item["uri"] != "ui://simple-host/photo-picker" {
				t.Fatal(item)
			}
			ui := item["_meta"].(map[string]any)["ui"].(map[string]any)
			csp := ui["csp"].(map[string]any)
			if ui["prefersBorder"] != true || len(csp["connectDomains"].([]any)) != 0 {
				t.Fatal(ui)
			}
			domains := csp["resourceDomains"].([]any)
			if len(domains) != 1 || domains[0] != "https://simple-host.app" {
				t.Fatal(domains)
			}
			page := item["text"].(string)
			for _, needle := range []string{"<!DOCTYPE html>", "ui/initialize", "ResizeObserver", "window.parent", "/photo-process.js", "keep_other_files: true"} {
				if !strings.Contains(page, needle) {
					t.Errorf("missing %s", needle)
				}
			}
			if strings.Contains(page, "__BRIDGE__") || strings.Contains(page, "openai/") {
				t.Fatal("unexpanded or vendor metadata")
			}
		}
	}
	t.Setenv("MCP_APP_PHOTO_PICKER", "accounts:alice")
	for _, handle := range []string{"alice", "bob"} {
		req := request{ID: json.RawMessage(`1`), Method: "resources/read", Params: json.RawMessage(`{"uri":"ui://simple-host/photo-picker"}`)}
		response := s.dispatch(httptest.NewRequest("POST", "/mcp", nil), req, Caller{Handle: handle})
		b, _ := json.Marshal(response)
		if strings.Contains(string(b), `"contents"`) != (handle == "alice") {
			t.Fatalf("account resource gate: %s", b)
		}
	}
}
