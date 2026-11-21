package mcp

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestAnalyticsContextListing(t *testing.T) {
	s := NewServer(testConfig(Config{}))
	base, _, _ := s.forCaller(Caller{AppName: "Claude"})
	before, _ := json.Marshal(base)
	for _, on := range []bool{false, true} {
		if on {
			s.analytics = &analyticsSender{}
		}
		for _, app := range []string{"Claude", "ChatGPT"} {
			caller := Caller{AppName: app}
			want := on
			full, bare, byName := s.forCaller(caller)
			for _, listed := range [][]Tool{full, bare} {
				for _, tool := range listed {
					assertAnalyticsContextSchema(t, tool.InputSchema, want)
					assertAnalyticsContextSchema(t, byName[tool.Name].InputSchema, false)
				}
			}
			found := s.searchFor(caller).find("passcode", 3)
			for _, item := range found.Structured["tools"].([]any) {
				schema := item.(map[string]any)["arguments"].(map[string]any)
				assertAnalyticsContextSchema(t, schema, want)
			}
		}
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("shared schemas mutated")
	}
}

func assertAnalyticsContextSchema(t *testing.T, schema map[string]any, want bool) {
	t.Helper()
	props, _ := schema["properties"].(map[string]any)
	_, got := props["context"]
	if got != want {
		t.Fatalf("context present = %v, want %v", got, want)
	}
	raw, _ := json.Marshal(schema["required"])
	if strings.Contains(string(raw), `"context"`) {
		t.Fatal("context is required")
	}
	if want && !reflect.DeepEqual(props["context"], map[string]any{"type": "string", "description": analyticsContextDescription}) {
		t.Fatal("wrong context description")
	}
}

func TestAnalyticsContextCalls(t *testing.T) {
	for _, on := range []bool{true} {
		for _, app := range []string{"Claude"} {
			for _, wrapped := range []bool{false, true} {
				for _, own := range []bool{false, true} {
					s := NewServer(testConfig(Config{}))
					if on {
						s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
					}
					props := map[string]any{"value": str("text")}
					if own {
						props["context"] = str("tool-owned context")
					}
					ran := false
					s.byName["context_test"] = Tool{Name: "context_test", InputSchema: object(props, "value"), run: func(_ *call, args map[string]any) (output, error) {
						ran = true
						_, present := args["context"]
						if present != own {
							t.Errorf("handler context = %v, own=%v", present, own)
						}
						return output{Text: "ok"}, nil
					}}
					args := map[string]any{"value": "hello", "context": "inner intent"}
					name := "context_test"
					if wrapped {
						name = "run_tool"
						args = map[string]any{"name": "context_test", "arguments": args, "context": "outer intent"}
					}
					var req request
					if err := json.Unmarshal([]byte(toolCall(name, args)), &req); err != nil {
						t.Fatal(err)
					}
					got := s.callTool(httptest.NewRequest("POST", "/mcp", nil), req, Caller{AppName: app}, false)
					if got.Error != nil || !ran || got.Result.(map[string]any)["isError"] != false {
						t.Fatalf("call failed: %+v", got)
					}
					if on {
						e := <-s.analytics.queue
						want := "inner intent"
						if own {
							want = ""
							if wrapped {
								want = "outer intent"
							}
						}
						if e.UserIntent != want {
							t.Errorf("intent=%q want=%q", e.UserIntent, want)
						}
						params := e.Parameters.(map[string]any)["arguments"].(map[string]any)
						if params["value"] != "hello" || e.ResourceName != "context_test" {
							t.Fatal("wrapped parameters or name")
						}
					}
				}
			}
		}
	}
}

func TestAnalyticsSuppliedSecretsNeverLeave(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		s := newTestServer(&recordingUpstream{})
		s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
		s.byName["visitor_read_site"] = Tool{Name: "visitor_read_site", InputSchema: object(map[string]any{"passcode": str("secret"), "text": str("text")}), run: func(_ *call, args map[string]any) (output, error) {
			return output{Text: "opened using 4477", Structured: map[string]any{"text": "4477"}}, nil
		}}
		args := map[string]any{"passcode": " 4477 ", "context": "unlocking with 4477", "text": "repeated 4477"}
		name := "visitor_read_site"
		if wrapped {
			name = "run_tool"
			args = map[string]any{"name": "visitor_read_site", "arguments": args, "context": "outer 4477"}
		}
		var req request
		if err := json.Unmarshal([]byte(toolCall(name, args)), &req); err != nil {
			t.Fatal(err)
		}
		got := s.callTool(httptest.NewRequest("POST", "/mcp", nil), req, Caller{AppName: "Claude"}, false)
		if got.Error != nil {
			t.Fatal(got.Error)
		}
		select {
		case e := <-s.analytics.queue:
			raw, _ := json.Marshal(e)
			if bytes.Contains(raw, []byte("4477")) {
				t.Fatal("secret in analytics")
			}
		default:
			t.Fatal("no analytics event")
		}
	}
}

// TestAnalyticsForNeverLeaks: review round 2, item B. for (an email, a raw
// visitor_id, or an opaque v_ reference) is dropped unconditionally, the
// same as a key or code — never only when it looks like an email — and its
// value is also scrubbed if repeated in the free-text context field, the
// same protection a secret field already gets. Covers direct and
// run_tool-wrapped dispatch, using storage_sql_execute's real, registered
// policy (unconditional now, so emailIdentity does not need to be the
// reason it is dropped).
func TestAnalyticsForNeverLeaks(t *testing.T) {
	for _, forValue := range []string{
		"person@example.com",
		"1b4e28ba-2fa1-11d2-883f-0016d3cca427",
		"v_opaque-for-reference",
	} {
		for _, wrapped := range []bool{false, true} {
			s := newTestServer(&recordingUpstream{})
			s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
			s.byName["storage_sql_execute"] = Tool{
				Name:        "storage_sql_execute",
				InputSchema: object(map[string]any{"site": str("site"), "name": str("name"), "for": str("for"), "table": str("table"), "values": map[string]any{"type": "object"}}),
				run: func(_ *call, args map[string]any) (output, error) {
					return output{Text: "added", Structured: map[string]any{"response": map[string]any{"changes": 1}, "status": float64(200)}}, nil
				},
			}
			args := map[string]any{"site": "ordinary-site", "name": "orders", "for": forValue, "table": "entries", "values": map[string]any{"note": "ordinary"}, "context": "adding for " + forValue}
			name := "storage_sql_execute"
			if wrapped {
				name = "run_tool"
				args = map[string]any{"name": "storage_sql_execute", "arguments": args, "context": "outer " + forValue}
			}
			var req request
			if err := json.Unmarshal([]byte(toolCall(name, args)), &req); err != nil {
				t.Fatal(err)
			}
			got := s.callTool(httptest.NewRequest("POST", "/mcp", nil), req, Caller{AppName: "Claude"}, false)
			if got.Error != nil {
				t.Fatal(got.Error)
			}
			select {
			case e := <-s.analytics.queue:
				raw, _ := json.Marshal(e)
				if bytes.Contains(raw, []byte(forValue)) {
					t.Fatalf("for %q leaked (wrapped=%v): %s", forValue, wrapped, raw)
				}
			default:
				t.Fatal("no analytics event")
			}
		}
	}
}
