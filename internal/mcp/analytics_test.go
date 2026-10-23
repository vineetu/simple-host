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
	"regexp"
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
	s := NewServer(Config{AnalyticsProjectID: projectID, AnalyticsEnv: "prod", ServerName: "simple-host", Version: "1.2.3"})
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
		{name: "code-like text from the caller", errorText: "SECRET-ERROR-TEXT [site_exists]", code: "tool_error", status: http.StatusNoContent},
		{name: "code this server does not define", errorText: "SECRET-ERROR-TEXT", refusal: "someones_site", code: "tool_error", status: http.StatusAccepted},
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
			before := time.Now().UTC().Truncate(time.Millisecond)
			if got := analyticsTestCall(t, s, caller); got != (runErr != nil) {
				t.Fatalf("tool error = %v", got)
			}
			after := time.Now().UTC()
			var body []byte
			select {
			case body = <-bodies:
			case <-time.After(2 * time.Second):
				t.Fatal("analytics event not sent")
			}
			for _, secret := range []string{"SECRET-ARG-VALUE", "SECRET-RESULT-VALUE", "SECRET-ERROR-TEXT", "SECRET-HEADER-VALUE", caller.Owner, caller.UserID, caller.APIKey, "203.0.113.7"} {
				if bytes.Contains(body, []byte(secret)) {
					t.Errorf("event contains private value %q", secret)
				}
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatal(err)
			}
			keys := []string{"id", "project_id", "session_id", "event_type", "resource_name", "duration", "timestamp", "is_error", "identify_actor_given_id", "client_name", "server_name", "server_version", "sdk_language", "tags"}
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
			if !regexp.MustCompile(`^evt_[a-zA-Z0-9]{27}$`).MatchString(got["id"].(string)) ||
				!regexp.MustCompile(`^ses_[a-zA-Z0-9]{27}$`).MatchString(got["session_id"].(string)) {
				t.Error("invalid event or session ID")
			}
			var typed analyticsEvent
			if err := json.Unmarshal(body, &typed); err != nil { // Duration must be an integer.
				t.Fatal(err)
			}
			if typed.Duration < 3 || typed.Duration > after.Sub(before).Milliseconds() {
				t.Errorf("duration = %d", typed.Duration)
			}
			stamp, err := time.Parse("2006-01-02T15:04:05.000Z", typed.Timestamp)
			if err != nil || stamp.Before(before) || stamp.After(after) {
				t.Errorf("timestamp = %q: %v", typed.Timestamp, err)
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
	brain := NewBrainServer(BrainConfig{Query: func(string, map[string]any) (string, map[string]any, error) {
		return "answer", map[string]any{}, nil
	}})
	if brain.analytics != nil {
		t.Fatal("brain has an analytics sender")
	}
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

func TestAnalyticsIdentityAndSession(t *testing.T) {
	a := &analyticsSender{queue: make(chan analyticsEvent, 1)}
	at := time.Date(2026, 10, 23, 23, 0, 0, 123000000, time.UTC)
	event := func(caller Caller, stamp time.Time) analyticsEvent {
		a.enqueue(caller, "test", 7*time.Millisecond, stamp, nil)
		return <-a.queue
	}
	first := event(Caller{Owner: "owner", UserID: "ignored", AppName: "Claude"}, at)
	if first.IdentifyActorGivenID != "81998e5f3c88325f" || first.SessionID != "ses_JvroxBc3pXTf8S8hCikBrOVQYCW" {
		t.Fatal("actor or session hash differs from the SHA-256 fixture")
	}
	fallback := event(Caller{UserID: "owner", AppName: "Claude"}, at.Add(-time.Hour))
	if first.SessionID != fallback.SessionID || first.IdentifyActorGivenID != fallback.IdentifyActorGivenID {
		t.Fatal("owner fallback or same-day session differs")
	}
	if first.SessionID != event(Caller{Owner: "owner", AppName: "Claude"}, at.In(time.FixedZone("east", 7200))).SessionID {
		t.Error("session date is not UTC")
	}
	for _, changed := range []analyticsEvent{
		event(Caller{Owner: "owner", AppName: "Claude"}, at.Add(24*time.Hour)),
		event(Caller{Owner: "other", AppName: "Claude"}, at),
		event(Caller{Owner: "owner", AppName: "ChatGPT"}, at),
	} {
		if first.SessionID == changed.SessionID {
			t.Error("session did not change with date, actor or app")
		}
	}
	anonymous := event(Caller{}, at)
	body, err := json.Marshal(anonymous)
	if err != nil {
		t.Fatal(err)
	}
	if anonymous.ClientName != "api-key" || bytes.Contains(body, []byte("identify_actor_given_id")) {
		t.Errorf("anonymous identity = %s", body)
	}
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
		a.enqueue(caller, "test", time.Millisecond, time.Now(), nil)
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

func TestAnalyticsDiscoveryAndRunTool(t *testing.T) {
	s := newTestServer(&recordingUpstream{answers: map[string]func() (int, string){
		"GET /v1/sites/blog/versions": func() (int, string) { return 200, `[{"version":1}]` },
	}})
	s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 10)}
	for _, tc := range []struct {
		name  string
		args  map[string]any
		event string
		isErr bool
	}{
		{"run_tool", map[string]any{"name": "list_versions", "arguments": map[string]any{"site": "blog"}}, "list_versions", false},
		{"run_tool", map[string]any{"name": "list_versions"}, "list_versions", true},
		{"run_tool", map[string]any{"name": "list_versions", "unexpected": true}, "list_versions", true},
		{"run_tool", map[string]any{"name": "list_versions", "arguments": false}, "list_versions", true},
		{"run_tool", map[string]any{"name": "not_a_tool"}, "run_tool", true},
		{"run_tool", map[string]any{"name": "run_tool"}, "run_tool", true},
		{"run_tool", map[string]any{"name": "get_state"}, "run_tool", true},
		{"find_tools", map[string]any{"query": "domain"}, "find_tools", false},
	} {
		_, _, isErr := resultOf(t, send(t, s, toolCall(tc.name, tc.args), nil, true))
		if isErr != tc.isErr {
			t.Errorf("%s: isError=%v", tc.event, isErr)
		}
		if len(s.analytics.queue) != 1 {
			t.Fatalf("got %d events, want one", len(s.analytics.queue))
		}
		event := <-s.analytics.queue
		if event.ResourceName != tc.event || event.IsError != tc.isErr {
			t.Errorf("got event %+v", event)
		}
	}
}
