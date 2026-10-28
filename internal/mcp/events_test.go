package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fakeEventsBackend struct {
	caller       Caller
	subscribed   []EventSubscribeRequest
	unsubscribed []EventUnsubscribeRequest
	result       EventSubscribeResult
	err          error
}

func (b *fakeEventsBackend) Subscribe(_ context.Context, caller Caller, req EventSubscribeRequest) (EventSubscribeResult, error) {
	b.caller = caller
	b.subscribed = append(b.subscribed, req)
	return b.result, b.err
}

func (b *fakeEventsBackend) Unsubscribe(_ context.Context, caller Caller, req EventUnsubscribeRequest) error {
	b.caller = caller
	b.unsubscribed = append(b.unsubscribed, req)
	return b.err
}

func eventTestParams() map[string]any {
	return map[string]any{
		"name": "record.added", "arguments": map[string]any{"site": "bakery", "storage": "orders"},
		"delivery": map[string]any{"mode": "webhook", "url": "https://receiver.example/events", "secret": "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 32))},
	}
}

func eventTestCaller() Caller {
	return Caller{APIKey: "test-key", Owner: "test-owner", OAuthGrantID: "test-grant", AppName: "ChatGPT"}
}

func sendEvent(t *testing.T, s *Server, method string, params map[string]any, caller Caller, modern bool) *httptest.ResponseRecorder {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if modern {
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    protocolVersion,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	r = r.WithContext(WithCaller(r.Context(), caller))
	if modern {
		r.Header.Set("MCP-Protocol-Version", protocolVersion)
		r.Header.Set("Mcp-Method", method)
		if method == "tools/call" {
			r.Header.Set("Mcp-Name", params["name"].(string))
		}
	} else {
		r.Header.Set("MCP-Protocol-Version", "2025-11-25")
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, r)
	return rec
}

func eventResult(t *testing.T, rec *httptest.ResponseRecorder, modern bool) map[string]any {
	t.Helper()
	out, ok := decode(t, rec)["result"].(map[string]any)
	if !ok || rec.Code != http.StatusOK {
		t.Fatalf("no successful result (HTTP %d)", rec.Code)
	}
	if modern && out["resultType"] != "complete" {
		t.Error("modern result has no resultType:complete")
	}
	if !modern {
		if _, present := out["resultType"]; present {
			t.Error("initialize-era result has resultType")
		}
	}
	return out
}

func eventErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want int) map[string]any {
	t.Helper()
	err, ok := decode(t, rec)["error"].(map[string]any)
	if !ok || err["code"] != float64(want) {
		t.Fatalf("expected JSON-RPC error %d", want)
	}
	return err
}

func TestEventsCapabilitiesAndTools(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, mode := range []string{"", CallerModeEvents, CallerModeHackSite} {
			for _, modern := range []bool{false, true} {
				t.Run(fmt.Sprintf("enabled=%v/mode=%s/modern=%v", enabled, mode, modern), func(t *testing.T) {
					backend := &fakeEventsBackend{}
					cfg := Config{}
					if enabled {
						cfg.Events = backend
					}
					s := NewServer(cfg)
					caller := eventTestCaller()
					caller.Mode, caller.GrantID = mode, "selected-team-grant"
					want := enabled && mode != CallerModeHackSite
					method := "initialize"
					if modern {
						method = "server/discover"
					}
					out := eventResult(t, sendEvent(t, s, method, nil, caller, modern), modern)
					caps := out["capabilities"].(map[string]any)
					_, hasEvents := caps["events"]
					_, hasExtensions := caps["extensions"]
					if hasEvents != want || hasExtensions != want {
						t.Errorf("event capabilities present=%v/%v, want %v", hasEvents, hasExtensions, want)
					}
					if want {
						if len(caps["events"].(map[string]any)) != 0 {
							t.Error("events capability is not empty")
						}
						ext := caps["extensions"].(map[string]any)
						if _, ok := ext["io.modelcontextprotocol/events"].(map[string]any); !ok {
							t.Error("missing extension")
						}
					}
					listed := eventResult(t, sendEvent(t, s, "tools/list", nil, caller, modern), modern)
					names := map[string]bool{}
					for _, raw := range listed["tools"].([]any) {
						names[raw.(map[string]any)["name"].(string)] = true
					}
					for _, name := range []string{"list_notifications", "remove_notification"} {
						if names[name] != (enabled && (mode == CallerModeEvents || mode == "" && flatListApp(caller.AppName))) {
							t.Errorf("%s listing disagrees with caller role", name)
						}
						if !want {
							eventErrorCode(t, sendEvent(t, s, "tools/call", map[string]any{"name": name}, caller, modern), codeInvalidParams)
						}
					}
					if !want {
						for _, method := range []string{"events/list", "events/subscribe", "events/unsubscribe"} {
							rec := sendEvent(t, s, method, nil, caller, modern)
							err := eventErrorCode(t, rec, codeMethodNotFound)
							status := http.StatusOK
							if modern {
								status = http.StatusNotFound
							}
							if rec.Code != status || err["message"] != "unknown method: "+method {
								t.Error("disabled method differs from unknown method")
							}
						}
					}
					if len(backend.subscribed)+len(backend.unsubscribed) != 0 {
						t.Error("disabled backend was called")
					}
				})
			}
		}
	}
}

func TestEventsListSchemas(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	for _, modern := range []bool{false, true} {
		out := eventResult(t, sendEvent(t, s, "events/list", nil, eventTestCaller(), modern), modern)
		events := out["events"].([]any)
		if len(events) != 3 {
			t.Fatal("expected three event types")
		}
		for i, name := range []string{"record.added", "record.changed", "file.uploaded"} {
			event := events[i].(map[string]any)
			if event["name"] != name || event["description"] == "" || !reflect.DeepEqual(event["delivery"], []any{"webhook"}) {
				t.Errorf("bad descriptor for %s", name)
			}
			for _, key := range []string{"inputSchema", "payloadSchema"} {
				if err := CheckOutputSchema(event[key]); err != nil {
					t.Errorf("%s %s: %v", name, key, err)
				}
			}
			for _, args := range []any{map[string]any{"site": "bakery"}, map[string]any{"site": "bakery", "storage": "orders"}} {
				if err := ValidateOutput(event["inputSchema"], args, nil); err != nil {
					t.Error(err)
				}
			}
			for _, args := range []any{map[string]any{}, map[string]any{"site": 1}, map[string]any{"site": "bakery", "extra": true}} {
				if ValidateOutput(event["inputSchema"], args, nil) == nil {
					t.Error("invalid input matches schema")
				}
			}
			data := map[string]any{"v": 1, "site": "bakery", "site_url": "https://bakery.example/", "storage": "orders", "preset": "records", "visitor": nil, "untrusted": []string{}}
			if name == "file.uploaded" {
				data["kind"], data["path"], data["size"], data["content_type"] = "files", "cake.jpg", 42, "image/jpeg"
				if err := ValidateOutput(event["payloadSchema"], data, nil); err != nil {
					t.Error(err)
				}
				data["fields"] = map[string]any{}
				if ValidateOutput(event["payloadSchema"], data, nil) == nil {
					t.Error("file payload accepts fields")
				}
			} else {
				data["kind"], data["table"], data["row_id"] = "sqlite", "orders", 42
				data["fields"] = map[string]any{"item": "cake", "nested": map[string]any{"any": true}, "count": 2}
				if name == "record.changed" {
					data["change"] = "edited"
				}
				if err := ValidateOutput(event["payloadSchema"], data, nil); err != nil {
					t.Error(err)
				}
				delete(data, "table")
				delete(data, "row_id")
				data["kind"], data["key"], data["visitor"] = "kv", "order", "visitor-id"
				data["fields"] = map[string]any{"value": []any{"cake", nil, 2}}
				if err := ValidateOutput(event["payloadSchema"], data, nil); err != nil {
					t.Error(err)
				}
				delete(data, "fields")
				data["truncated"] = []string{"fields"}
				if name == "record.changed" {
					data["change"] = "deleted"
				}
				if err := ValidateOutput(event["payloadSchema"], data, nil); err != nil {
					t.Error(err)
				}
				data["visitor"] = 7
				if ValidateOutput(event["payloadSchema"], data, nil) == nil {
					t.Error("visitor accepts a number")
				}
			}
		}
	}
}

func TestEventsSubscribeAndRefresh(t *testing.T) {
	deadline := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cursor := "c_42"
	for _, modern := range []bool{false, true} {
		backend := &fakeEventsBackend{result: EventSubscribeResult{ID: "sub_test", RefreshBefore: &deadline, Cursor: &cursor, Truncated: true}}
		s := NewServer(Config{Events: backend})
		params := eventTestParams()
		params["cursor"], params["ttlMs"] = cursor, 3600000
		rec := sendEvent(t, s, "events/subscribe", params, eventTestCaller(), modern)
		out := eventResult(t, rec, modern)
		if out["id"] != "sub_test" || out["refreshBefore"] != deadline.Format(time.RFC3339) || out["cursor"] != cursor || out["truncated"] != true {
			t.Error("subscription result lost backend values")
		}
		if _, present := out["deliveryStatus"]; present {
			t.Error("new subscription carries deliveryStatus")
		}
		if len(backend.subscribed) != 1 {
			t.Fatal("backend not called once")
		}
		req := backend.subscribed[0]
		if req.Name != "record.added" || req.Site != "bakery" || req.Storage != "orders" || req.URL != "https://receiver.example/events" || req.CanonicalArgs != `{"site":"bakery","storage":"orders"}` || req.Cursor == nil || *req.Cursor != cursor || !req.TTLSet || req.TTL == nil || *req.TTL != time.Hour {
			t.Error("backend received wrong subscription arguments")
		}
		if req.Secret != params["delivery"].(map[string]any)["secret"] {
			t.Error("secret not passed to backend")
		}
		if backend.caller.Owner != "test-owner" || backend.caller.OAuthGrantID != "test-grant" || backend.caller.AppName != "ChatGPT" {
			t.Error("caller provenance lost")
		}
		if strings.Contains(rec.Body.String(), "whsec_") || strings.Contains(rec.Body.String(), "test-key") {
			t.Error("response contains credential")
		}
		for _, lastError := range []string{"", "timeout"} {
			backend.result = EventSubscribeResult{ID: "sub_test", Refreshed: true, Active: lastError == "", LastError: lastError}
			params = eventTestParams()
			params["ttlMs"] = nil
			out = eventResult(t, sendEvent(t, s, "events/subscribe", params, eventTestCaller(), modern), modern)
			if out["refreshBefore"] != nil || out["cursor"] != nil || out["truncated"] != false {
				t.Error("nullable result is wrong")
			}
			status := out["deliveryStatus"].(map[string]any)
			if status["active"] != (lastError == "") {
				t.Error("wrong active flag")
			}
			if lastError == "" {
				if _, present := status["lastError"]; present {
					t.Error("empty lastError should be omitted")
				}
			} else if status["lastError"] != lastError {
				t.Error("lastError lost")
			}
		}
	}
}

func TestEventsTTLAndCanonicalArguments(t *testing.T) {
	for _, tc := range []struct {
		name    string
		present bool
		ttl     any
		want    *time.Duration
	}{
		{"omitted", false, nil, nil}, {"null", true, nil, nil},
		{"zero", true, 0, eventDuration(0)}, {"integer", true, 1234, eventDuration(1234 * time.Millisecond)},
		{"whole number", true, json.Number("1.0"), eventDuration(time.Millisecond)},
		{"exponent", true, json.Number("1e3"), eventDuration(time.Second)},
		{"maximum", true, math.MaxInt64 / int64(time.Millisecond), eventDuration(time.Duration(math.MaxInt64/int64(time.Millisecond)) * time.Millisecond)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := eventTestParams()
			if tc.present {
				params["ttlMs"] = tc.ttl
			}
			raw, _ := json.Marshal(params)
			req, err := parseEventParams(raw, true)
			if err != nil || req.TTLSet != tc.present || !reflect.DeepEqual(req.TTL, tc.want) {
				t.Error("TTL presence or value was not preserved")
			}
		})
	}
	for _, size := range []int{24, 64} {
		params := eventTestParams()
		params["delivery"].(map[string]any)["secret"] = "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, size))
		raw, _ := json.Marshal(params)
		if _, err := parseEventParams(raw, true); err != nil {
			t.Errorf("valid %d-byte secret rejected", size)
		}
	}
	for _, arguments := range []string{`{"storage":"orders", "site":"bakery"}`, ` { "site":"bakery", "storage":"orders" } `, `{"site":"bake\u0072y","storage":"orders"}`} {
		params := eventTestParams()
		params["arguments"] = json.RawMessage(arguments)
		raw, _ := json.Marshal(params)
		req, err := parseEventParams(raw, true)
		if err != nil || req.CanonicalArgs != `{"site":"bakery","storage":"orders"}` {
			t.Error("arguments were not canonicalized")
		}
	}
	for _, cursor := range []any{nil, "", "c_1"} {
		params := eventTestParams()
		params["cursor"] = cursor
		raw, _ := json.Marshal(params)
		if _, err := parseEventParams(raw, true); err != nil {
			t.Error("valid cursor rejected")
		}
	}
}

func eventDuration(d time.Duration) *time.Duration { return &d }

func TestEventsValidation(t *testing.T) {
	cases := []struct {
		name, path string
		value      any
		missing    bool
		code       int
	}{
		{"missing name", "name", nil, true, codeInvalidParams},
		{"null name", "name", nil, false, codeInvalidParams},
		{"numeric name", "name", 1, false, codeInvalidParams},
		{"empty name", "name", "", false, codeInvalidParams},
		{"unknown name", "name", "notification.test", false, EventCodeNotFound},
		{"missing arguments", "arguments", nil, true, codeInvalidParams},
		{"null arguments", "arguments", nil, false, codeInvalidParams},
		{"array arguments", "arguments", []any{}, false, codeInvalidParams},
		{"missing site", "arguments.site", nil, true, codeInvalidParams},
		{"null site", "arguments.site", nil, false, codeInvalidParams},
		{"numeric site", "arguments.site", 1, false, codeInvalidParams},
		{"empty site", "arguments.site", "", false, codeInvalidParams},
		{"blank site", "arguments.site", " ", false, codeInvalidParams},
		{"null storage", "arguments.storage", nil, false, codeInvalidParams},
		{"numeric storage", "arguments.storage", 1, false, codeInvalidParams},
		{"unknown argument", "arguments.other", "value", false, codeInvalidParams},
		{"missing delivery", "delivery", nil, true, codeInvalidParams},
		{"null delivery", "delivery", nil, false, codeInvalidParams},
		{"array delivery", "delivery", []any{}, false, codeInvalidParams},
		{"missing mode", "delivery.mode", nil, true, codeInvalidParams},
		{"null mode", "delivery.mode", nil, false, codeInvalidParams},
		{"numeric mode", "delivery.mode", 1, false, codeInvalidParams},
		{"empty mode", "delivery.mode", "", false, EventCodeUnsupported},
		{"unsupported mode", "delivery.mode", "push", false, EventCodeUnsupported},
		{"missing url", "delivery.url", nil, true, codeInvalidParams},
		{"null url", "delivery.url", nil, false, codeInvalidParams},
		{"numeric url", "delivery.url", 1, false, codeInvalidParams},
		{"empty url", "delivery.url", "", false, codeInvalidParams},
		{"blank url", "delivery.url", " ", false, codeInvalidParams},
		{"missing secret", "delivery.secret", nil, true, codeInvalidParams},
		{"null secret", "delivery.secret", nil, false, codeInvalidParams},
		{"numeric secret", "delivery.secret", 1, false, codeInvalidParams},
		{"empty secret", "delivery.secret", "", false, codeInvalidParams},
		{"wrong prefix", "delivery.secret", "other_" + base64.StdEncoding.EncodeToString(make([]byte, 32)), false, codeInvalidParams},
		{"invalid base64", "delivery.secret", "whsec_!!!!", false, codeInvalidParams},
		{"URL base64", "delivery.secret", "whsec_" + strings.Repeat("_", 32), false, codeInvalidParams},
		{"unpadded base64", "delivery.secret", "whsec_" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)), false, codeInvalidParams},
		{"short secret", "delivery.secret", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 23)), false, codeInvalidParams},
		{"long secret", "delivery.secret", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 65)), false, codeInvalidParams},
		{"numeric cursor", "cursor", 1, false, codeInvalidParams},
		{"boolean cursor", "cursor", false, false, codeInvalidParams},
		{"object cursor", "cursor", map[string]any{}, false, codeInvalidParams},
		{"string ttl", "ttlMs", "1000", false, codeInvalidParams},
		{"boolean ttl", "ttlMs", true, false, codeInvalidParams},
		{"object ttl", "ttlMs", map[string]any{}, false, codeInvalidParams},
		{"negative ttl", "ttlMs", -1, false, codeInvalidParams},
		{"fractional ttl", "ttlMs", 1.5, false, codeInvalidParams},
		{"overflow ttl", "ttlMs", math.MaxInt64/int64(time.Millisecond) + 1, false, codeInvalidParams},
		{"huge ttl", "ttlMs", json.Number("1e400"), false, codeInvalidParams},
	}
	for _, modern := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("%s/modern=%v", tc.name, modern), func(t *testing.T) {
				backend := &fakeEventsBackend{}
				s := NewServer(Config{Events: backend})
				params := eventTestParams()
				parts := strings.Split(tc.path, ".")
				obj := params
				if len(parts) == 2 {
					obj = params[parts[0]].(map[string]any)
				}
				key := parts[len(parts)-1]
				if tc.missing {
					delete(obj, key)
				} else {
					obj[key] = tc.value
				}
				err := eventErrorCode(t, sendEvent(t, s, "events/subscribe", params, eventTestCaller(), modern), tc.code)
				if tc.code == EventCodeNotFound && err["data"].(map[string]any)["kind"] != "event" {
					t.Error("missing event discriminator")
				}
				if tc.code == EventCodeUnsupported && !reflect.DeepEqual(err["data"], map[string]any{"feature": "deliveryMode", "value": tc.value}) {
					t.Error("missing unsupported delivery discriminator")
				}
				if len(backend.subscribed) != 0 {
					t.Error("invalid request reached backend")
				}
				if tc.path != "cursor" && tc.path != "ttlMs" && tc.path != "delivery.secret" && tc.name != "missing mode" {
					eventErrorCode(t, sendEvent(t, s, "events/unsubscribe", params, eventTestCaller(), modern), tc.code)
					if len(backend.unsubscribed) != 0 {
						t.Error("invalid unsubscribe reached backend")
					}
				}
			})
		}
	}
	for _, raw := range []string{"", "null", "[]", `"params"`, "{"} {
		if _, err := parseEventParams(json.RawMessage(raw), true); err == nil {
			t.Error("malformed params accepted")
		}
	}
}

func TestEventsForbidden(t *testing.T) {
	backend := &fakeEventsBackend{}
	s := NewServer(Config{Events: backend})
	for _, modern := range []bool{false, true} {
		for _, caller := range []Caller{{}, {APIKey: "test-key"}, {Owner: "test-owner"}, {APIKey: "test-key", Owner: "test-owner", DeployOnly: true}} {
			for _, method := range []string{"events/list", "events/subscribe", "events/unsubscribe"} {
				eventErrorCode(t, sendEvent(t, s, method, eventTestParams(), caller, modern), EventCodeForbidden)
			}
		}
	}
	// Also exercise a request with no Caller context at all.
	eventErrorCode(t, send(t, s, `{"jsonrpc":"2.0","id":1,"method":"events/list"}`, nil, false), EventCodeForbidden)
	if len(backend.subscribed)+len(backend.unsubscribed) != 0 {
		t.Error("unauthenticated caller reached backend")
	}
}

func TestEventsBackendErrors(t *testing.T) {
	for _, modern := range []bool{false, true} {
		for _, method := range []string{"events/subscribe", "events/unsubscribe"} {
			for _, code := range []int{EventCodeNotFound, EventCodeForbidden, EventCodeResourceExhausted, EventCodeUnsupported, EventCodeCallbackEndpoint, codeInvalidParams} {
				data := map[string]any{"reason": "challenge_failed"}
				backend := &fakeEventsBackend{err: fmt.Errorf("wrapped: %w", &EventError{Code: code, Message: "backend category", Data: data})}
				s := NewServer(Config{Events: backend})
				err := eventErrorCode(t, sendEvent(t, s, method, eventTestParams(), eventTestCaller(), modern), code)
				if err["message"] != "backend category" || !reflect.DeepEqual(err["data"], data) {
					t.Error("EventError changed")
				}
			}
			backend := &fakeEventsBackend{err: errors.New("private backend detail")}
			rec := sendEvent(t, NewServer(Config{Events: backend}), method, eventTestParams(), eventTestCaller(), modern)
			err := eventErrorCode(t, rec, codeInternalError)
			if err["message"] != "internal error" || err["data"] != nil || strings.Contains(rec.Body.String(), "private backend detail") {
				t.Error("internal error exposes backend detail")
			}
		}
	}
}

func TestEventsUnsubscribe(t *testing.T) {
	for _, modern := range []bool{false, true} {
		backend := &fakeEventsBackend{}
		s := NewServer(Config{Events: backend})
		params := eventTestParams()
		delete(params["delivery"].(map[string]any), "secret")
		delete(params["delivery"].(map[string]any), "mode")
		out := eventResult(t, sendEvent(t, s, "events/unsubscribe", params, eventTestCaller(), modern), modern)
		delete(out, "resultType")
		if len(out) != 0 {
			t.Error("unsubscribe result is not empty")
		}
		want := EventUnsubscribeRequest{Name: "record.added", Site: "bakery", Storage: "orders", CanonicalArgs: `{"site":"bakery","storage":"orders"}`, URL: "https://receiver.example/events"}
		if !reflect.DeepEqual(backend.unsubscribed, []EventUnsubscribeRequest{want}) || backend.caller.Owner != "test-owner" {
			t.Error("unsubscribe key or principal changed")
		}
	}
}

func TestNotificationToolsOutputs(t *testing.T) {
	body := `{"notifications":[{"id":"sub_test","site":"bakery","url":"https://receiver.example/events","events":["record.added"],"storage":"orders","label":"Orders","source":"mcp","app_name":"ChatGPT","state":"active","created_at":"2026-10-22T12:00:00Z","expires_at":null,"last_attempt_at":null,"last_success_at":"2026-10-22T13:00:00Z","last_result":"ok","failing_since":null,"auth_header_name":"Authorization","secret":"do-not-return","prev_secret":"do-not-return","auth_header_value":"do-not-return"}],"limits":{"per_site":10,"per_minute":60,"per_day":5000,"history_days":7,"private":"do-not-return"}}`
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/notifications":                          func() (int, string) { return 200, body },
		"GET /v1/sites/bakery/notifications":             func() (int, string) { return 200, body },
		"DELETE /v1/sites/bakery/notifications/sub_test": func() (int, string) { return 200, `{"deleted":true}` },
	}}
	s := NewServer(Config{Events: &fakeEventsBackend{}, Upstream: up})
	for _, modern := range []bool{false, true} {
		seen := map[string]map[string]bool{}
		for _, tc := range []struct {
			name         string
			args         map[string]any
			method, path string
		}{
			{"list_notifications", map[string]any{}, "GET", "/v1/notifications"},
			{"list_notifications", map[string]any{"site": "bakery"}, "GET", "/v1/sites/bakery/notifications"},
			{"remove_notification", map[string]any{"site": "bakery", "id": "sub_test"}, "DELETE", "/v1/sites/bakery/notifications/sub_test"},
		} {
			rec := sendEvent(t, s, "tools/call", map[string]any{"name": tc.name, "arguments": tc.args}, eventTestCaller(), modern)
			text, structured, isErr := resultOf(t, rec)
			if isErr || text == "" {
				t.Fatal("notification tool failed")
			}
			schema := s.byName[tc.name].OutputSchema
			if err := CheckOutputSchema(schema); err != nil {
				t.Fatal(err)
			}
			if seen[tc.name] == nil {
				seen[tc.name] = map[string]bool{}
			}
			if err := ValidateOutput(schema, structured, seen[tc.name]); err != nil {
				t.Error(err)
			}
			if strings.Contains(rec.Body.String(), "do-not-return") || strings.Contains(rec.Body.String(), "test-key") {
				t.Error("tool output contains credentials")
			}
			r := up.requests[len(up.requests)-1]
			if r.Method != tc.method || r.URL.Path != tc.path || r.Header.Get("X-API-Key") != "test-key" {
				t.Error("REST route or credential not forwarded")
			}
		}
		for name, paths := range seen {
			for _, path := range SchemaPropertyPaths(s.byName[name].OutputSchema) {
				if !paths[path] {
					t.Errorf("%s schema field %s not exercised", name, path)
				}
			}
		}
	}
	up.answers["GET /v1/notifications"] = func() (int, string) {
		return 200, `{"notifications":[],"limits":{"per_site":10,"per_minute":60,"per_day":5000,"history_days":7}}`
	}
	_, out, isErr := resultOf(t, send(t, s, toolCall("list_notifications", nil), nil, true))
	if isErr || len(out["notifications"].([]any)) != 0 {
		t.Error("empty notifications list is not an array")
	}
	for _, tool := range notificationTools() {
		if tool.Annotations["openWorldHint"] != false || tool.Annotations["readOnlyHint"] != (tool.Name == "list_notifications") || tool.Annotations["destructiveHint"] != (tool.Name == "remove_notification") {
			t.Error("incorrect notification annotations")
		}
	}
	listed := send(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, map[string]string{"MCP-Protocol-Version": "2025-03-26"}, true)
	for _, raw := range decode(t, listed)["result"].(map[string]any)["tools"].([]any) {
		tool := raw.(map[string]any)
		if tool["outputSchema"] != nil {
			t.Error("old client received outputSchema")
		}
	}
}

func TestNotificationToolsErrors(t *testing.T) {
	up := &recordingUpstream{}
	s := NewServer(Config{Events: &fakeEventsBackend{}, Upstream: up})
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_notifications", map[string]any{"site": 1}},
		{"list_notifications", map[string]any{"site": nil}},
		{"list_notifications", map[string]any{"extra": true}},
		{"remove_notification", map[string]any{"id": "sub_test"}},
		{"remove_notification", map[string]any{"site": "bakery"}},
		{"remove_notification", map[string]any{"site": "bakery", "id": 1}},
	} {
		_, _, isErr := resultOf(t, send(t, s, toolCall(tc.name, tc.args), nil, true))
		if !isErr || len(up.requests) != 0 {
			t.Error("invalid tool arguments reached REST")
		}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"list_notifications", map[string]any{}},
		{"remove_notification", map[string]any{"site": "bakery", "id": "sub_test"}},
	} {
		text, _, isErr := resultOf(t, send(t, s, toolCall(tc.name, tc.args), nil, true))
		if !isErr || !strings.Contains(text, "HTTP 404") {
			t.Error("REST refusal was not a tool error")
		}
	}
	up.answers = map[string]func() (int, string){"GET /v1/notifications": func() (int, string) { return 200, "not JSON" }}
	_, _, isErr := resultOf(t, send(t, s, toolCall("list_notifications", nil), nil, true))
	if !isErr {
		t.Error("invalid REST JSON accepted")
	}
}

func TestEventsDeployTools(t *testing.T) {
	s := NewServer(Config{Events: &fakeEventsBackend{}})
	caller := eventTestCaller()
	caller.DeployOnly = true
	if s.offersEvents(caller) {
		t.Fatal("deploy key offered events")
	}
	full, bare, byName := s.forCaller(caller)
	for _, tool := range append(full, bare...) {
		if tool.Name == "list_notifications" || tool.Name == "remove_notification" {
			t.Fatal("deploy key offered notification tool")
		}
	}
	if _, ok := byName["list_notifications"]; ok {
		t.Fatal("deploy key can call notification tool")
	}
}
func TestEventsClientErrorNumbers(t *testing.T) {
	if EventCodeNotFound != -32011 || EventCodeForbidden != -32012 || EventCodeResourceExhausted != -32013 || EventCodeUnsupported != -32014 || EventCodeCallbackEndpoint != -32015 {
		t.Fatal("event errors differ from ChatGPT contract")
	}
}

func TestNotificationToolRedactsWebhookURL(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://hooks.example:8443/incoming/credential?token=secret#hidden", "https://hooks.example:8443/..."},
		{"https://hooks.example?token=secret", "https://hooks.example"},
		{"https://hooks.example/", "https://hooks.example/..."},
		{"https://[2606:4700::1111]:8443/credential", "https://[2606:4700::1111]:8443/..."},
		{"https://user:secret@hooks.example/credential", "https://hooks.example/..."},
		{"invalid%URL", ""},
	} {
		up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/notifications": func() (int, string) {
			body, _ := json.Marshal(map[string]any{"notifications": []any{map[string]any{"url": tc.raw}}, "limits": map[string]int{}})
			return 200, string(body)
		}}}
		s := NewServer(Config{Events: &fakeEventsBackend{}, Upstream: up})
		rec := send(t, s, toolCall("list_notifications", nil), nil, true)
		_, out, isErr := resultOf(t, rec)
		if isErr || out["notifications"].([]any)[0].(map[string]any)["url"] != tc.want {
			t.Fatal("webhook destination was not redacted")
		}
		for _, secret := range []string{"credential", "token=", "secret", "hidden"} {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatal("tool result exposed a URL credential")
			}
		}
	}
}

type unavailableEventsBackend struct{ fakeEventsBackend }

func (b *unavailableEventsBackend) EventsAvailability() error {
	return &EventError{Code: EventCodeForbidden, Message: "notifications_not_enabled"}
}

func TestEventsUnavailableOnlyBlocksSubscribe(t *testing.T) {
	for _, modern := range []bool{false, true} {
		backend := &unavailableEventsBackend{}
		s := NewServer(Config{Events: backend})
		caller := eventTestCaller()
		if _, ok := s.capabilities(caller)["events"]; ok {
			t.Fatal("unavailable events advertised")
		}
		eventErrorCode(t, sendEvent(t, s, "events/subscribe", eventTestParams(), caller, modern), EventCodeForbidden)
		eventResult(t, sendEvent(t, s, "events/list", nil, caller, modern), modern)
		eventResult(t, sendEvent(t, s, "events/unsubscribe", eventTestParams(), caller, modern), modern)
		if len(backend.subscribed) != 0 || len(backend.unsubscribed) != 1 {
			t.Fatal("unavailable events blocked unsubscribe or accepted subscribe")
		}
	}
}
