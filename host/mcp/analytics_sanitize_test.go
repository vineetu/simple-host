package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnalyticsPostedConversation(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	s := NewServer(testConfig(Config{AnalyticsProjectID: "conversation-test"}))
	t.Cleanup(func() { close(s.analytics.queue) })
	const planted = "PLANTED_PRIVATE_VALUE"
	encoded := base64.StdEncoding.EncodeToString([]byte(planted))
	ref := "v_opaque-visitor"
	for i, tc := range []struct {
		name             string
		args, structured map[string]any
		text             string
	}{
		{"storage_put_kv", map[string]any{"site": "ordinary-site", "key": "menu", "value": map[string]any{"normal": "ordinary text", "nested": map[string]any{"passcode": planted, "access_token": planted, "team_key": planted, "signing_secret": planted, "API-Key": planted, "Authorization": planted, "cookie": planted, "auth_header": map[string]any{"value": planted}}}}, map[string]any{"visitor_id": ref, "email": planted}, `{"visitor_id":"v_opaque-visitor","email":"PLANTED_PRIVATE_VALUE","normal":"ordinary text"}`},
		{"create_site", map[string]any{"site": "ordinary-site", "viewers": []any{planted}, "files": map[string]any{"index.html": planted}, "files_base64": map[string]any{"a.png": encoded}}, map[string]any{"site": "ordinary-site"}, "not an email address: " + planted},
		{"read_site_file", map[string]any{"site": "ordinary-site", "path": "index.html"}, map[string]any{"content": planted, "path": "index.html", "size": len(planted)}, planted},
		{"storage_get_kv", map[string]any{"image": "data:image/png;base64," + encoded, "blob": strings.Repeat("A", 10240)}, map[string]any{"image": "data:image/png;base64," + encoded, "blob": strings.Repeat("A", 10240)}, "ordinary text"},
		{"get_site_passcode", map[string]any{"site": "ordinary-site"}, map[string]any{"passcode": planted}, planted},
		{"storage_file_download_link", map[string]any{"site": "ordinary-site"}, map[string]any{"url": planted}, planted},
		{"grant_site_viewer", map[string]any{"emails": []any{planted}}, map[string]any{"added": []any{planted}, "already_listed": []any{planted}}, planted},
		{"storage_visitor_emails", nil, map[string]any{"visitors": []any{map[string]any{"visitor_id": ref, "email": planted}}}, planted},
		{"who_am_i", map[string]any{"include_email": true}, map[string]any{"email": planted}, planted},
		{"storage_sql_query", nil, map[string]any{"columns": []any{"visitor_id", "visitor_email", "email", "token"}, "rows": []any{[]any{ref, planted, planted, planted}}}, `{"columns":["visitor_id","email"],"rows":[["v_opaque-visitor","PLANTED_PRIVATE_VALUE"]]}`},
		{"visitor_add_entry", map[string]any{"address": "ordinary-site", "passcode": planted, "value": "ordinary text"}, map[string]any{"visitor_data": map[string]any{"mine": true}, "note": "ordinary text"}, `{"visitor_data":{"mine":true},"note":"ordinary text"}`},
	} {
		t.Run(fmt.Sprintf("%d_%s", i, tc.name), func(t *testing.T) {
			props := map[string]any{}
			for k := range tc.args {
				props[k] = map[string]any{}
			}
			originalArgs, _ := json.Marshal(tc.args)
			originalStructured, _ := json.Marshal(tc.structured)
			s.byName[tc.name] = Tool{Name: tc.name, InputSchema: object(props), run: func(_ *call, args map[string]any) (output, error) {
				if _, ok := args["context"]; ok {
					t.Error("context reached handler")
				}
				return output{Text: tc.text, Structured: tc.structured, Meta: map[string]any{"ui_token": planted}}, nil
			}}
			args := map[string]any{}
			for k, v := range tc.args {
				args[k] = v
			}
			args["context"] = "The agent checks ordinary values for the site owner."
			var req request
			_ = json.Unmarshal([]byte(toolCall(tc.name, args)), &req)
			reply := s.callTool(httptest.NewRequest("POST", "/mcp", nil), req, Caller{AppName: "Claude"}, false)
			gotClient, _ := json.Marshal(reply)
			expected := toolResult(req.ID, tc.text, tc.structured, false, false)
			expected.Result.(map[string]any)["_meta"] = map[string]any{"ui_token": planted}
			wantClient, _ := json.Marshal(expected)
			if !bytes.Equal(gotClient, wantClient) {
				t.Fatal("client result changed")
			}
			after, _ := json.Marshal(tc.structured)
			afterArgs, _ := json.Marshal(tc.args)
			if !bytes.Equal(after, originalStructured) || !bytes.Equal(afterArgs, originalArgs) {
				t.Fatal("source mutated")
			}
			var body []byte
			select {
			case body = <-bodies:
			case <-time.After(2 * time.Second):
				t.Fatal("no event")
			}
			for _, hidden := range []string{planted, encoded, strings.Repeat("A", 100), "data:image/png;base64,"} {
				if bytes.Contains(body, []byte(hidden)) {
					t.Errorf("raw event contains planted content %q", hidden[:min(25, len(hidden))])
				}
			}
			var event map[string]json.RawMessage
			if err := json.Unmarshal(body, &event); err != nil {
				t.Fatal(err)
			}
			for _, field := range []string{"parameters", "response", "user_intent"} {
				if event[field] == nil {
					t.Fatalf("missing %s", field)
				}
			}
			for _, field := range []string{"parameters", "response"} {
				if len(event[field]) > 8192 {
					t.Fatalf("oversized %s", field)
				}
			}
			if i == 0 {
				for _, ordinary := range []string{"ordinary-site", "menu", "ordinary text", ref} {
					if !bytes.Contains(body, []byte(ordinary)) {
						t.Errorf("lost %s", ordinary)
					}
				}
			}
		})
	}
}

func TestAnalyticsFileMetadataAndFields(t *testing.T) {
	for _, tc := range []struct {
		value   any
		kind    string
		encoded bool
		size    int
	}{
		{"你好", ".html", false, 6}, {"YWJjZA==", "image/png", true, 4}, {"YWJjZA", "image/png", true, 4}, {"data:image/png;base64,YWJjZA==", "", false, 4},
	} {
		got := analyticsFile(tc.value, tc.kind, tc.encoded).(map[string]any)
		if got["bytes"] != tc.size || got["removed"] != "file" {
			t.Fatalf("bad file metadata %v", got)
		}
	}
	for _, name := range []string{"Pass-Code", "client_secret", "api-key", "sign_in_code", "team_key", "private_key", "upload_url", "preview_url", "join_code", "gender"} {
		if !analyticsSecretField("storage_put_kv", name) {
			t.Errorf("not removed: %s", name)
		}
	}
	if analyticsSecretField("storage_put_kv", "key") {
		t.Fatal("plain KV key removed")
	}
}

func TestAnalyticsMalformedEdits(t *testing.T) {
	for _, edits := range []any{
		"PRIVATE",
		map[string]any{"unexpected": "PRIVATE"},
		[]any{map[string]any{"path": "index.html", "find": "PRIVATE", "replace": "PRIVATE", "unexpected": "PRIVATE", "content_type": "PRIVATE"}},
	} {
		got := analyticsParameters("update_site", map[string]any{"edits": edits})
		body, _ := json.Marshal(got)
		if bytes.Contains(body, []byte("PRIVATE")) || !bytes.Contains(body, []byte(`"removed":"file"`)) {
			t.Fatalf("edits not replaced with file metadata: %s", body)
		}
	}
	got := analyticsParameters("update_site", map[string]any{"edits": []any{"PRIVATE"}})
	file := got.(map[string]any)["arguments"].(map[string]any)["edits"].([]any)[0].(map[string]any)
	if file["bytes"] != 7 || file["type"] != "unknown" {
		t.Fatalf("bad edit metadata: %v", file)
	}
}

func TestAnalyticsIntentBinary(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"data:image/png;base64,YWJjZA==", "[file: 4 bytes, image/png]"},
		{"data:image/png;base64," + strings.Repeat("A", 12000), "[file: 9000 bytes, image/png]"},
		{strings.Repeat("A", 10240), "[binary data redacted - not supported by AgentCat]"},
		{"Ask ada@example.com about https://example.com from 192.0.2.1", "Ask [email removed] about https://example.com from 192.0.2.1"},
	} {
		if got := capAnalyticsIntent(tc.input); got != tc.want {
			t.Errorf("intent = %q, want %q", got, tc.want)
		}
	}
}

func TestAnalyticsSetSiteAccessError(t *testing.T) {
	reply := toolResult(nil, "Invalid viewer email: PRIVATE@example.com", nil, true, false)
	got := analyticsResponse("set_site_access", reply.Result).(map[string]any)
	if got["isError"] != true || got["content"].([]any)[0].(map[string]any)["text"] != "null" {
		t.Fatalf("viewer error prose was retained: %v", got)
	}
}

func TestPasscodeScreenAnalyticsDispatch(t *testing.T) {
	s, _, _ := passcodeTestServer(t)
	s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
	for _, wrapped := range []bool{false, true} {
		name := "passcode_screen"
		args := map[string]any{"action": "show", "site": "blog", "session": "fixture-session", "context": "fixture-private-context"}
		if wrapped {
			name = "run_tool"
			args = map[string]any{"name": "passcode_screen", "arguments": args}
		}
		passcodeWire(t, s, screenCaller(), name, args)
		select {
		case event := <-s.analytics.queue:
			if screenJSON(event.Parameters) != `{"arguments":"[removed]"}` || event.Response != "[removed]" || event.UserIntent != "[removed]" {
				t.Fatal("passcode screen data reached analytics")
			}
		default:
			t.Fatal("missing analytics event")
		}
	}
}

// A key typed into a secret tool by mistake (a value argument, which every
// secret tool refuses) never reaches analytics.
func TestSecretToolsDropValue(t *testing.T) {
	for _, name := range []string{"request_secret", "list_secrets", "delete_secret"} {
		got := jsonText(analyticsParameters(name, map[string]any{"site": "shop", "name": "ai", "value": "sk-typed-by-mistake-0123"}))
		if strings.Contains(got, "sk-typed") || !strings.Contains(got, "shop") {
			t.Errorf("%s: %s", name, got)
		}
	}
}
