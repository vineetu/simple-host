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
	for _, tc := range []struct {
		enabled bool
		mode    string
	}{{false, ""}, {true, ""}, {true, CallerModeEvents}} {
		for _, modern := range []bool{false, true} {
			t.Run(fmt.Sprintf("enabled=%v/mode=%s/modern=%v", tc.enabled, tc.mode, modern), func(t *testing.T) {
				backend := &fakeEventsBackend{}
				cfg := Config{}
				if tc.enabled {
					cfg.Events = backend
				}
				s := NewServer(testConfig(cfg))
				caller := eventTestCaller()
				caller.Mode, caller.GrantID = tc.mode, "selected-team-grant"
				method := "initialize"
				if modern {
					method = "server/discover"
				}
				caps := eventResult(t, sendEvent(t, s, method, nil, caller, modern), modern)["capabilities"].(map[string]any)
				_, hasEvents := caps["events"]
				_, hasExtensions := caps["extensions"]
				if hasEvents != tc.enabled || hasExtensions != tc.enabled {
					t.Errorf("event capabilities present=%v/%v, want %v", hasEvents, hasExtensions, tc.enabled)
				}
				listed := eventResult(t, sendEvent(t, s, "tools/list", nil, caller, modern), modern)
				names := map[string]bool{}
				for _, raw := range listed["tools"].([]any) {
					names[raw.(map[string]any)["name"].(string)] = true
				}
				for _, name := range []string{"list_notifications", "remove_notification"} {
					if names[name] != tc.enabled {
						t.Errorf("%s listing disagrees with caller role", name)
					}
					if !tc.enabled {
						eventErrorCode(t, sendEvent(t, s, "tools/call", map[string]any{"name": name}, caller, modern), codeInvalidParams)
					}
				}
				if !tc.enabled {
					for _, method := range []string{"events/list", "events/subscribe", "events/unsubscribe"} {
						eventErrorCode(t, sendEvent(t, s, method, nil, caller, modern), codeMethodNotFound)
					}
				}
				if len(backend.subscribed)+len(backend.unsubscribed) != 0 {
					t.Error("disabled backend was called")
				}
			})
		}
	}
}

func TestEventsListSchemas(t *testing.T) {
	s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}}))
	events := eventResult(t, sendEvent(t, s, "events/list", nil, eventTestCaller(), false), false)["events"].([]any)
	if len(events) != 7 {
		t.Fatal("expected seven event types on Simple Host")
	}
	for _, raw := range events {
		event := raw.(map[string]any)
		for _, key := range []string{"inputSchema", "payloadSchema"} {
			if err := CheckOutputSchema(event[key]); err != nil {
				t.Errorf("%s %s: %v", event["name"], key, err)
			}
		}
	}
	record := events[0].(map[string]any)
	data := map[string]any{"v": 1, "site": "bakery", "site_url": "https://bakery.example/", "storage": "orders", "visitor": nil, "untrusted": []string{},
		"kind": "sqlite", "table": "orders", "row_id": 42, "fields": map[string]any{"item": "cake"}}
	if err := ValidateOutput(record["payloadSchema"], data, nil); err != nil {
		t.Error(err)
	}
	product := eventTestCaller()
	product.Mode = CallerModeEvents
	if listed := eventResult(t, sendEvent(t, s, "events/list", nil, product, true), true)["events"].([]any); len(listed) != 3 {
		t.Fatal("expected three event types on Simple Hack")
	}
}

func TestEventsSiteEventArguments(t *testing.T) {
	name := SiteEventNames[0]
	backend := &fakeEventsBackend{}
	s := NewServer(testConfig(Config{Events: backend}))
	params := eventTestParams()
	params["name"] = name
	// A storage filter means nothing on a site event: InvalidParams, not silently ignored.
	eventErrorCode(t, sendEvent(t, s, "events/subscribe", params, eventTestCaller(), false), codeInvalidParams)
	if len(backend.subscribed) != 0 {
		t.Fatal("site event with storage reached backend")
	}
	params = eventTestParams()
	params["name"] = name
	params["arguments"] = map[string]any{"site": "bakery"}
	eventResult(t, sendEvent(t, s, "events/subscribe", params, eventTestCaller(), false), false)
	if len(backend.subscribed) != 1 || backend.subscribed[0].Name != name || backend.subscribed[0].CanonicalArgs != `{"site":"bakery"}` {
		t.Fatalf("site event subscribe: %+v", backend.subscribed)
	}
	// Simple Hack does not offer site events.
	product := eventTestCaller()
	product.Mode = CallerModeEvents
	eventErrorCode(t, sendEvent(t, s, "events/subscribe", params, product, false), EventCodeNotFound)
	if len(backend.subscribed) != 1 {
		t.Fatal("Hack site event reached backend")
	}
}

func TestEventsSubscribeAndRefresh(t *testing.T) {
	deadline := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cursor := "c_42"
	for _, modern := range []bool{false, true} {
		backend := &fakeEventsBackend{result: EventSubscribeResult{ID: "sub_test", RefreshBefore: &deadline, Cursor: &cursor, Truncated: true}}
		s := NewServer(testConfig(Config{Events: backend}))
		params := eventTestParams()
		params["cursor"], params["ttlMs"] = cursor, 3600000
		rec := sendEvent(t, s, "events/subscribe", params, eventTestCaller(), modern)
		out := eventResult(t, rec, modern)
		if out["id"] != "sub_test" || out["refreshBefore"] != deadline.Format(time.RFC3339) || out["cursor"] != cursor || out["truncated"] != true {
			t.Error("subscription result lost backend values")
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
	}
}

func TestEventsTTLAndCanonicalArguments(t *testing.T) {
	params := eventTestParams()
	params["ttlMs"] = json.Number("1e3")
	params["arguments"] = json.RawMessage(` { "storage":"orders", "site":"bake\u0072y" } `)
	raw, _ := json.Marshal(params)
	req, err := parseEventParams(raw, true, false)
	if err != nil || !req.TTLSet || req.TTL == nil || *req.TTL != time.Second {
		t.Error("TTL was not preserved")
	}
	// Unsubscribe matches on these canonical arguments, whatever order or escaping the client used.
	if err != nil || req.CanonicalArgs != `{"site":"bakery","storage":"orders"}` {
		t.Error("arguments were not canonicalized")
	}
}

func TestEventsValidation(t *testing.T) {
	cases := []struct {
		name, path string
		value      any
		missing    bool
		code       int
	}{
		{"missing name", "name", nil, true, codeInvalidParams},
		{"unknown name", "name", "notification.test", false, EventCodeNotFound},
		{"array arguments", "arguments", []any{}, false, codeInvalidParams},
		{"blank site", "arguments.site", " ", false, codeInvalidParams},
		{"unknown argument", "arguments.other", "value", false, codeInvalidParams},
		{"missing delivery", "delivery", nil, true, codeInvalidParams},
		{"unsupported mode", "delivery.mode", "push", false, EventCodeUnsupported},
		{"empty url", "delivery.url", "", false, codeInvalidParams},
		{"wrong prefix", "delivery.secret", "other_" + base64.StdEncoding.EncodeToString(make([]byte, 32)), false, codeInvalidParams},
		{"short secret", "delivery.secret", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 23)), false, codeInvalidParams},
		{"long secret", "delivery.secret", "whsec_" + base64.StdEncoding.EncodeToString(make([]byte, 65)), false, codeInvalidParams},
		{"numeric cursor", "cursor", 1, false, codeInvalidParams},
		{"negative ttl", "ttlMs", -1, false, codeInvalidParams},
		{"overflow ttl", "ttlMs", math.MaxInt64/int64(time.Millisecond) + 1, false, codeInvalidParams},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &fakeEventsBackend{}
			s := NewServer(testConfig(Config{Events: backend}))
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
			eventErrorCode(t, sendEvent(t, s, "events/subscribe", params, eventTestCaller(), false), tc.code)
			if len(backend.subscribed) != 0 {
				t.Error("invalid request reached backend")
			}
		})
	}
}

func TestEventsForbidden(t *testing.T) {
	backend := &fakeEventsBackend{}
	s := NewServer(testConfig(Config{Events: backend}))
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
	data := map[string]any{"reason": "challenge_failed"}
	backend := &fakeEventsBackend{err: fmt.Errorf("wrapped: %w", &EventError{Code: EventCodeCallbackEndpoint, Message: "backend category", Data: data})}
	err := eventErrorCode(t, sendEvent(t, NewServer(testConfig(Config{Events: backend})), "events/subscribe", eventTestParams(), eventTestCaller(), false), EventCodeCallbackEndpoint)
	if !reflect.DeepEqual(err["data"], data) {
		t.Error("EventError changed")
	}
	backend = &fakeEventsBackend{err: errors.New("private backend detail")}
	rec := sendEvent(t, NewServer(testConfig(Config{Events: backend})), "events/subscribe", eventTestParams(), eventTestCaller(), false)
	eventErrorCode(t, rec, codeInternalError)
	if strings.Contains(rec.Body.String(), "private backend detail") {
		t.Error("internal error exposes backend detail")
	}
}

func TestEventsUnsubscribe(t *testing.T) {
	backend := &fakeEventsBackend{}
	s := NewServer(testConfig(Config{Events: backend}))
	params := eventTestParams()
	delete(params["delivery"].(map[string]any), "secret")
	delete(params["delivery"].(map[string]any), "mode")
	eventResult(t, sendEvent(t, s, "events/unsubscribe", params, eventTestCaller(), true), true)
	want := EventUnsubscribeRequest{Name: "record.added", Site: "bakery", Storage: "orders", CanonicalArgs: `{"site":"bakery","storage":"orders"}`, URL: "https://receiver.example/events"}
	if !reflect.DeepEqual(backend.unsubscribed, []EventUnsubscribeRequest{want}) || backend.caller.Owner != "test-owner" {
		t.Error("unsubscribe key or principal changed")
	}
}

func TestNotificationToolsOutputs(t *testing.T) {
	body := `{"notifications":[{"id":"sub_test","site":"bakery","url":"https://receiver.example/events","events":["record.added"],"storage":"orders","label":"Orders","source":"mcp","app_name":"ChatGPT","state":"active","created_at":"2026-10-22T12:00:00Z","expires_at":null,"last_attempt_at":null,"last_success_at":"2026-10-22T13:00:00Z","last_result":"ok","failing_since":null,"auth_header_name":"Authorization","secret":"do-not-return","prev_secret":"do-not-return","auth_header_value":"do-not-return"}],"limits":{"per_site":10,"per_minute":60,"per_day":5000,"history_days":7,"private":"do-not-return"}}`
	up := &recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/bakery/notifications":             func() (int, string) { return 200, body },
		"DELETE /v1/sites/bakery/notifications/sub_test": func() (int, string) { return 200, `{"deleted":true}` },
	}}
	s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}, Upstream: up}))
	for _, tc := range []struct {
		name         string
		args         map[string]any
		method, path string
	}{
		{"list_notifications", map[string]any{"site": "bakery"}, "GET", "/v1/sites/bakery/notifications"},
		{"remove_notification", map[string]any{"site": "bakery", "id": "sub_test"}, "DELETE", "/v1/sites/bakery/notifications/sub_test"},
	} {
		rec := sendEvent(t, s, "tools/call", map[string]any{"name": tc.name, "arguments": tc.args}, eventTestCaller(), true)
		_, structured, isErr := resultOf(t, rec)
		if isErr {
			t.Fatal("notification tool failed")
		}
		if err := ValidateOutput(s.byName[tc.name].OutputSchema, structured, nil); err != nil {
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
	// Bad arguments never reach REST.
	before := len(up.requests)
	if _, _, isErr := resultOf(t, send(t, s, toolCall("remove_notification", map[string]any{"site": "bakery"}), nil, true)); !isErr || len(up.requests) != before {
		t.Error("invalid tool arguments reached REST")
	}
}

func TestEventsDeployTools(t *testing.T) {
	s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}}))
	caller := eventTestCaller()
	caller.DeployOnly = true
	if s.offersEvents(caller) {
		t.Fatal("deploy key offered events")
	}
	if _, _, byName := s.forCaller(caller); func() bool { _, ok := byName["list_notifications"]; return ok }() {
		t.Fatal("deploy key can call notification tool")
	}
}

func TestNotificationToolRedactsWebhookURL(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"https://hooks.example:8443/incoming/credential?token=secret#hidden", "https://hooks.example:8443/..."},
		{"https://user:secret@hooks.example/credential", "https://hooks.example/..."},
		{"invalid%URL", ""},
	} {
		up := &recordingUpstream{answers: map[string]func() (int, string){"GET /v1/notifications": func() (int, string) {
			body, _ := json.Marshal(map[string]any{"notifications": []any{map[string]any{"url": tc.raw}}, "limits": map[string]int{}})
			return 200, string(body)
		}}}
		s := NewServer(testConfig(Config{Events: &fakeEventsBackend{}, Upstream: up}))
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
	backend := &unavailableEventsBackend{}
	s := NewServer(testConfig(Config{Events: backend}))
	caller := eventTestCaller()
	if _, ok := s.capabilities(caller)["events"]; ok {
		t.Fatal("unavailable events advertised")
	}
	eventErrorCode(t, sendEvent(t, s, "events/subscribe", eventTestParams(), caller, true), EventCodeForbidden)
	eventResult(t, sendEvent(t, s, "events/unsubscribe", eventTestParams(), caller, true), true)
	if len(backend.subscribed) != 0 || len(backend.unsubscribed) != 1 {
		t.Fatal("unavailable events blocked unsubscribe or accepted subscribe")
	}
}
