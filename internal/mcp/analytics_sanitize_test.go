package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestAnalyticsPostedConversation(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	s := NewServer(Config{AnalyticsProjectID: "conversation-test"})
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
		{"create_site", map[string]any{"site": "ordinary-site", "files": map[string]any{"index.html": planted}, "files_base64": map[string]any{"a.png": encoded}}, map[string]any{"site": "ordinary-site"}, "ordinary text"},
		{"update_site", map[string]any{"site": "ordinary-site", "edits": `[{"path":"index.html","find":"PLANTED_PRIVATE_VALUE","replace":"PLANTED_PRIVATE_VALUE"}]`}, map[string]any{"preview_url": planted}, planted},
		{"storage_put_file", map[string]any{"content_base64": encoded, "content_type": "image/png", "data": planted}, map[string]any{}, "ordinary text"},
		{"read_site_file", map[string]any{"site": "ordinary-site", "path": "index.html"}, map[string]any{"content": planted, "path": "index.html", "size": len(planted)}, planted},
		{"read_site_file", map[string]any{"paths": []any{"index.html"}}, map[string]any{"files": []any{map[string]any{"content": planted, "path": "index.html", "size": len(planted)}}}, planted},
		{"storage_get_kv", map[string]any{"image": "data:image/png;base64," + encoded, "blob": strings.Repeat("A", 10240)}, map[string]any{"image": "data:image/png;base64," + encoded, "blob": strings.Repeat("A", 10240)}, "ordinary text"},
		{"get_site_passcode", map[string]any{"site": "ordinary-site"}, map[string]any{"passcode": planted}, planted},
		{"create_upload_link", map[string]any{"site": "ordinary-site"}, map[string]any{"upload_url": planted, "photos_url": planted, "how": "curl " + planted}, planted},
		{"create_import_link", map[string]any{"site": "ordinary-site"}, map[string]any{"import_url": planted}, planted},
		{"storage_file_download_link", map[string]any{"site": "ordinary-site"}, map[string]any{"url": planted}, planted},
		{"hack_create_team_key", map[string]any{"slug": "ordinary-site"}, map[string]any{"key": planted}, planted},
		{"grant_site_viewer", map[string]any{"emails": []any{planted}}, map[string]any{"added": []any{planted}, "already_listed": []any{planted}}, planted},
		{"revoke_site_viewer", map[string]any{"email": planted}, map[string]any{"removed": planted}, planted},
		{"list_site_viewers", nil, map[string]any{"viewers": []any{map[string]any{"email": planted}}}, planted},
		{"storage_visitor_emails", nil, map[string]any{"visitors": []any{map[string]any{"visitor_id": ref, "email": planted}}}, planted},
		{"who_am_i", map[string]any{"include_email": true}, map[string]any{"email": planted}, planted},
		{"storage_sql_query", nil, map[string]any{"columns": []any{"visitor_id", "visitor_email", "email", "token"}, "rows": []any{[]any{ref, planted, planted, planted}}}, `{"columns":["visitor_id","email"],"rows":[["v_opaque-visitor","PLANTED_PRIVATE_VALUE"]]}`},
		{"hack_create_event", map[string]any{"contact_email": planted}, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_update_event", map[string]any{"contact_email": planted}, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"hack_get_event", nil, map[string]any{"organiser": map[string]any{"contact_email": planted}}, planted},
		{"set_site_access", map[string]any{"emails": []any{planted}}, map[string]any{"added": []any{planted}}, planted},
		{"hack_export_results", nil, map[string]any{"csv": planted, "filename": "results.csv"}, planted},
		{"hack_export_scores", nil, map[string]any{"csv": planted, "filename": "scores.csv"}, planted},
		{"hack_regenerate_code", nil, map[string]any{"url": planted, "code": planted}, planted},
		{"hack_export_participants", nil, map[string]any{"csv": planted, "filename": "participants.csv"}, planted},
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

func TestAnalyticsSDKSanitization(t *testing.T) {
	for _, tc := range []struct {
		block any
		want  string
	}{
		{map[string]any{"type": "image", "data": "bytes"}, "[image content redacted - not supported by AgentCat]"},
		{map[string]any{"type": "audio", "data": "bytes"}, "[audio content redacted - not supported by AgentCat]"},
		{map[string]any{"type": "resource", "resource": map[string]any{"blob": "bytes"}}, "[binary resource content redacted - not supported by AgentCat]"},
		{map[string]any{"type": "future"}, `[unsupported content type "future" redacted - not supported by AgentCat]`},
		{map[string]any{}, `[unsupported content type "undefined" redacted - not supported by AgentCat]`},
	} {
		result := sdkSanitizeResponse(map[string]any{"content": []any{tc.block}}).(map[string]any)
		if !reflect.DeepEqual(result["content"].([]any)[0], map[string]any{"type": "text", "text": tc.want}) {
			t.Fatalf("wrong placeholder: %v", result)
		}
	}
	for _, block := range []any{nil, map[string]any{"type": "text", "text": "hello"}, map[string]any{"type": "resource_link", "uri": "https://example.com"}, map[string]any{"type": "resource", "resource": map[string]any{"text": "hello"}}} {
		got := sdkSanitizeResponse(map[string]any{"content": []any{block}}).(map[string]any)["content"].([]any)[0]
		if !reflect.DeepEqual(got, block) {
			t.Fatal("SDK passthrough changed")
		}
	}
	for _, s := range []string{strings.Repeat("A", 10239), strings.Repeat("A", 10240), strings.Repeat("A", 10238) + "\n\r==", strings.Repeat("A", 10240) + "-", strings.Repeat("A", 10240) + "=A"} {
		want := s
		if len(s) >= 10240 && !strings.ContainsAny(s, "-") && !strings.HasSuffix(s, "=A") {
			want = "[binary data redacted - not supported by AgentCat]"
		}
		got := sdkSanitizeParameters(s)
		if got != want {
			t.Fatal("base64 gate/pattern differs from SDK")
		}
	}
}

func TestAnalyticsCaps(t *testing.T) {
	deep := map[string]any{"text": "end"}
	for range 30 {
		deep = map[string]any{"nested": deep}
	}
	wide := map[string]any{}
	for i := range 1000 {
		wide[fmt.Sprintf("field%04d", i)] = strings.Repeat("你好😀\"<", 1000)
	}
	for _, value := range []any{deep, wide, strings.Repeat("你好😀\"<", 10000), map[string]any{strings.Repeat("界", 10000): "value"}} {
		for _, got := range []any{analyticsParameters("test", map[string]any{"value": value}), analyticsResponse("test", map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("你好😀", 10000)}}, "structuredContent": value, "isError": false})} {
			b, err := json.Marshal(got)
			if err != nil || len(b) > 8192 || !utf8.Valid(b) || bytes.Contains(b, []byte("�")) {
				t.Fatalf("bad capped JSON: bytes=%d err=%v", len(b), err)
			}
		}
	}
	got := capAnalyticsIntent(strings.Repeat("😀", 3000))
	if utf8.RuneCountInString(got) != 2048 || !strings.HasSuffix(got, "...") {
		t.Fatal("intent cap")
	}
	normalized := normalizeAnalytics(deep, 10, 100, 8192)
	b, _ := json.Marshal(normalized)
	if !bytes.Contains(b, []byte("[Object]")) {
		t.Fatal("missing depth marker")
	}
	b, _ = json.Marshal(normalizeAnalytics(wide, 10, 100, 8))
	if !bytes.Contains(b, []byte("[MaxProperties ~]")) || !bytes.Contains(b, []byte("...")) {
		t.Fatal("missing SDK truncation markers")
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
	for _, name := range []string{"Pass-Code", "PASSWORD", "client_secret", "token", "api-key", "authorization", "cookie", "code", "sign_in_code", "verification_code", "team_key", "signing_key", "private_key", "auth_header", "upload_url", "photos_url", "import_url", "download_url", "signed_url", "preview_url", "pinned_url", "join_code", "judge_code", "join_url", "judge_url", "invite_url"} {
		if !analyticsSecretField("storage_put_kv", name) {
			t.Errorf("not removed: %s", name)
		}
	}
	if analyticsSecretField("storage_put_kv", "key") {
		t.Fatal("plain KV key removed")
	}
}

func TestAnalyticsSQLRowsOnce(t *testing.T) {
	var nested any = "end"
	for range 30 {
		nested = map[string]any{
			"columns": []any{"nested", "blob"},
			"rows":    []any{[]any{nested, map[string]any{"base64": "YWJjZA=="}}},
		}
	}
	started := time.Now()
	got := dropAnalyticsValues("storage_sql_query", nested)
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("depth-30 SQL rows took %v", elapsed)
	}
	for level := 0; level < 2; level++ {
		cells := got.(map[string]any)["rows"].([]any)[0].([]any)
		blob := cells[1].(map[string]any)["base64"].(map[string]any)
		if blob["bytes"] != 4 {
			t.Fatalf("level %d: BLOB size = %v, want 4", level, blob["bytes"])
		}
		got = cells[0]
	}
	body, _ := json.Marshal(nested)
	if !bytes.Contains(body, []byte("[Object]")) || bytes.Contains(body, []byte(`"end"`)) {
		t.Fatalf("missing recursion bound: %s", body)
	}

	// Duplicate column names, extra cells, and malformed rows still get one pass.
	got = dropAnalyticsValues("storage_sql_query", map[string]any{
		"columns": []any{"visitor_id", "email", "content_base64", "content_base64"},
		"rows": []any{
			[]any{"v_ref", "PRIVATE", "YQ==", "YWJjZA==", map[string]any{"token": "PRIVATE"}},
			map[string]any{"token": "PRIVATE"},
		},
	})
	rows := got.(map[string]any)["rows"].([]any)
	cells := rows[0].([]any)
	if cells[1] != "v_ref" || cells[2].(map[string]any)["bytes"] != 1 || cells[3].(map[string]any)["bytes"] != 4 ||
		cells[4].(map[string]any)["token"] != "[removed]" || rows[1].(map[string]any)["token"] != "[removed]" {
		t.Fatalf("bad SQL field handling: %v", rows)
	}
}

func TestAnalyticsMalformedEdits(t *testing.T) {
	for _, edits := range []any{
		[]any{"PRIVATE"},
		`["PRIVATE"]`,
		"PRIVATE",
		map[string]any{"unexpected": "PRIVATE"},
		[]any{map[string]any{"unexpected": "PRIVATE"}},
		[]any{map[string]any{"path": "index.html", "find": "PRIVATE", "replace": "PRIVATE", "unexpected": "PRIVATE", "content_type": "PRIVATE"}},
		[]any{map[string]any{"path": map[string]any{"unexpected": "PRIVATE"}}},
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
		{"Ask ada@example.com about https://example.com from 192.0.2.1", "Ask ada@example.com about https://example.com from 192.0.2.1"},
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
