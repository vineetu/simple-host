package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func analyticsTestEndpoint(t *testing.T, status int) <-chan []byte {
	t.Helper()
	bodies := make(chan []byte, 16)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request: %s, %s", r.Method, r.Header.Get("Content-Type"))
		}
		for name := range r.Header {
			switch name {
			case "Content-Type", "Content-Length", "User-Agent", "Accept-Encoding":
			default:
				t.Errorf("analytics sent header %s", name)
			}
		}
		if r.URL.RawQuery != "" {
			t.Errorf("analytics sent a query string")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		bodies <- body
		w.WriteHeader(status)
	}))
	old := analyticsEndpoint
	analyticsEndpoint = endpoint.URL
	t.Cleanup(func() {
		analyticsEndpoint = old
		endpoint.Close()
	})
	return bodies
}

func analyticsTestServer(t *testing.T, projectID string, runErr error) *Server {
	t.Helper()
	s := NewServer(testConfig(Config{AnalyticsProjectID: projectID, AnalyticsEnv: "prod", ServerName: "simple-host", Version: "1.2.3"}))
	if s.analytics != nil {
		t.Cleanup(func() { close(s.analytics.queue) })
		if cap(s.analytics.queue) != 512 || s.analytics.client.Timeout != 5*time.Second {
			t.Fatal("unexpected queue capacity or HTTP timeout")
		}
	}
	s.byName["analytics_test"] = Tool{
		Name:        "analytics_test",
		InputSchema: map[string]any{"properties": map[string]any{"value": map[string]any{"type": "string"}}},
		run: func(_ *call, args map[string]any) (output, error) {
			if args["value"] != "SECRET-ARG-VALUE" {
				t.Error("test argument did not reach tool")
			}
			time.Sleep(3 * time.Millisecond)
			return output{Text: "SECRET-RESULT-VALUE", Structured: map[string]any{"result": "SECRET-RESULT-VALUE"}}, runErr
		},
	}
	return s
}

func analyticsTestCall(t *testing.T, s *Server, caller Caller) bool {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(toolCall("analytics_test", map[string]any{"value": "SECRET-ARG-VALUE"})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "SECRET-HEADER-VALUE")
	req.RemoteAddr = "203.0.113.7:5555"
	req = req.WithContext(WithCaller(req.Context(), caller))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	_, _, isError := resultOf(t, rec)
	return isError
}

func assertNoAnalyticsEvent(t *testing.T, bodies <-chan []byte) {
	t.Helper()
	select {
	case body := <-bodies:
		t.Fatalf("unexpected analytics event: %s", body)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAnalyticsToolCalls(t *testing.T) {
	for _, tc := range []struct {
		name, errorText, refusal, code string
		status                         int
	}{
		{name: "success", status: http.StatusNoContent},
		{name: "refusal", errorText: "SECRET-ERROR-TEXT SECRET-ARG-VALUE [site_exists]", refusal: "site_exists", code: "site_exists", status: http.StatusServiceUnavailable},
		{name: "product refusal", errorText: "hack_lock_team failed (HTTP 403): Organisers only. [not_organiser]", refusal: "not_organiser", code: "not_organiser", status: http.StatusNoContent},
		{name: "malformed refusal code", errorText: "failed [Not A Code]", refusal: "Not A Code", code: "tool_error", status: http.StatusNoContent},
		{name: "code-like text from the caller", errorText: "SECRET-ERROR-TEXT [site_exists]", code: "tool_error", status: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bodies := analyticsTestEndpoint(t, tc.status)
			var runErr error
			if tc.refusal != "" {
				runErr = &refusalError{text: tc.errorText, code: tc.refusal}
			} else if tc.errorText != "" {
				runErr = errors.New(tc.errorText)
			}
			s := analyticsTestServer(t, "project-test", runErr)
			caller := Caller{Owner: "SECRET-OWNER", UserID: "SECRET-USER-ID", APIKey: "SECRET-API-KEY", AppName: "Test App"}
			if got := analyticsTestCall(t, s, caller); got != (runErr != nil) {
				t.Fatalf("tool error = %v", got)
			}
			var body []byte
			select {
			case body = <-bodies:
			case <-time.After(2 * time.Second):
				t.Fatal("analytics event not sent")
			}
			for _, secret := range []string{"SECRET-HEADER-VALUE", caller.Owner, caller.UserID, caller.APIKey, "203.0.113.7"} {
				if bytes.Contains(body, []byte(secret)) {
					t.Errorf("event contains private value %q", secret)
				}
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			keys := []string{"parameters", "response", "user_intent", "id", "project_id", "session_id", "event_type", "resource_name", "duration", "timestamp", "is_error", "identify_actor_given_id", "client_name", "server_name", "server_version", "sdk_language", "tags"}
			if runErr != nil {
				keys = append(keys, "error")
				if !reflect.DeepEqual(got["error"], map[string]any{"message": tc.code}) {
					t.Errorf("error = %v", got["error"])
				}
			}
			var gotKeys []string
			for key := range got {
				gotKeys = append(gotKeys, key)
			}
			slices.Sort(keys)
			slices.Sort(gotKeys)
			if !slices.Equal(gotKeys, keys) {
				t.Fatalf("keys = %v, want %v", gotKeys, keys)
			}
			hash := sha256.Sum256([]byte("agentcat-actor::" + caller.Owner))
			for key, want := range map[string]any{
				"project_id": "project-test", "event_type": "mcp:tools/call", "resource_name": "analytics_test",
				"is_error": runErr != nil, "identify_actor_given_id": hex.EncodeToString(hash[:8]),
				"client_name": "other", "server_name": "simple-host", "server_version": "1.2.3", "sdk_language": "go",
				"tags": map[string]any{"env": "prod"},
			} {
				if !reflect.DeepEqual(got[key], want) {
					t.Errorf("%s = %v, want %v", key, got[key], want)
				}
			}
			assertNoAnalyticsEvent(t, bodies) // Including after a failed POST.
		})
	}
}

func TestAnalyticsDisabledAndSkippedCalls(t *testing.T) {
	bodies := analyticsTestEndpoint(t, http.StatusNoContent)
	s := analyticsTestServer(t, "", nil)
	if s.analytics != nil {
		t.Fatal("empty project ID created a sender")
	}
	analyticsTestCall(t, s, Caller{APIKey: "test-key"})
	enabled := analyticsTestServer(t, "project-test", nil)
	for _, body := range []string{
		toolCall("unknown_tool", nil),
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"analytics_test","arguments":[]}}`,
		`{"jsonrpc":`,
	} {
		send(t, enabled, body, nil, true)
	}
	assertNoAnalyticsEvent(t, bodies)
}

func TestAnalyticsFullQueueDropsWithoutBlocking(t *testing.T) {
	s := analyticsTestServer(t, "", nil)
	a := &analyticsSender{queue: make(chan analyticsEvent, 512)}
	s.analytics = a
	for i := 0; i < cap(a.queue); i++ {
		a.queue <- analyticsEvent{ResourceName: "already-queued"}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		analyticsTestCall(t, s, Caller{APIKey: "test-key"})
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("full analytics queue blocked a tool call")
	}
	if len(a.queue) != cap(a.queue) {
		t.Fatal("queue length changed")
	}
	for range cap(a.queue) {
		if got := <-a.queue; got.ResourceName != "already-queued" {
			t.Fatal("full queue accepted a new event")
		}
	}
}

func TestAnalyticsClientNameAndPepper(t *testing.T) {
	a := &analyticsSender{queue: make(chan analyticsEvent, 1), pepper: "server-secret"}
	event := func(caller Caller) analyticsEvent {
		a.enqueue(caller, "test", time.Millisecond, time.Now(), nil, nil, nil, "", analyticsCall{})
		return <-a.queue
	}
	for app, want := range map[string]string{"Claude": "Claude", "chatgpt": "chatgpt", "ada@example.com": "other", "": "api-key"} {
		if got := event(Caller{Owner: "owner", AppName: app}).ClientName; got != want {
			t.Errorf("app %q sent as %q, want %q", app, got, want)
		}
	}
	unpeppered := sha256.Sum256([]byte("agentcat-actor::owner"))
	if event(Caller{Owner: "owner"}).IdentifyActorGivenID == hex.EncodeToString(unpeppered[:8]) {
		t.Error("the pepper is not mixed into the account hash")
	}
}

func TestAnalyticsRejectedWrapperArguments(t *testing.T) {
	s := newTestServer(nil)
	s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
	for _, tc := range []struct {
		name, event string
		args        any
	}{
		{"updat_site", "run_tool", map[string]any{"files": map[string]any{"index.html": "PRIVATE"}}},
		{"run_tool", "run_tool", map[string]any{"name": "update_site", "arguments": map[string]any{"files": map[string]any{"index.html": "PRIVATE"}}}},
		{"update_site", "update_site", "PRIVATE"},
	} {
		_, _, isErr := resultOf(t, send(t, s, toolCall("run_tool", map[string]any{"name": tc.name, "arguments": tc.args}), nil, true))
		if !isErr || len(s.analytics.queue) != 1 {
			t.Fatalf("%s: expected a failed call and one event", tc.name)
		}
		event := <-s.analytics.queue
		parameters, _ := json.Marshal(event.Parameters)
		if string(parameters) != `{"arguments":"[removed]"}` || event.ResourceName != tc.event || !event.IsError || event.Error == nil {
			t.Fatalf("%s: bad rejected event: %+v", tc.name, event)
		}
	}
}
