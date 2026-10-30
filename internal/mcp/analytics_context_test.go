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
	s := NewServer(Config{})
	base, _, _ := s.forCaller(Caller{AppName: "Claude"})
	before, _ := json.Marshal(base)
	for _, on := range []bool{false, true} {
		if on {
			s.analytics = &analyticsSender{}
		}
		for _, app := range []string{"Claude", "ChatGPT", "OpenAI", "Codex", "claude code"} {
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
			if app == "Claude" && on {
				after, _ := json.Marshal(full)
				growth := len(after) - len(before)
				t.Logf("Claude tools/list byte growth: %d (%d -> %d)", growth, len(before), len(after))
				if growth <= 0 || growth >= 3000 {
					t.Fatalf("growth = %d", growth)
				}
			}
		}
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) {
		t.Fatal("shared schemas mutated")
	}
	for _, schema := range []map[string]any{
		{"properties": map[string]any{"context": str("owned")}},
		{"oneOf": []any{}}, {"anyOf": []any{}}, {"allOf": []any{}},
	} {
		before, _ := json.Marshal(schema)
		got, _ := json.Marshal(withAnalyticsContext(Tool{InputSchema: schema}).InputSchema)
		if !bytes.Equal(before, got) {
			t.Fatal("changed an excluded schema")
		}
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
	for _, on := range []bool{false, true} {
		for _, app := range []string{"Claude", "ChatGPT", "OpenAI", "Codex"} {
			for _, wrapped := range []bool{false, true} {
				for _, own := range []bool{false, true} {
					s := NewServer(Config{})
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

func TestAnalyticsNonStringContext(t *testing.T) {
	for _, on := range []bool{false, true} {
		for _, wrapped := range []bool{false, true} {
			for _, context := range []any{nil, 42, map[string]any{"reason": "test"}} {
				s := newTestServer(&recordingUpstream{answers: map[string]func() (int, string){
					"GET /v1/sites": func() (int, string) { return 200, `[]` },
				}})
				if on {
					s.analytics = &analyticsSender{queue: make(chan analyticsEvent, 1)}
				}
				name := "list_sites"
				args := map[string]any{"context": context}
				if wrapped {
					name = "run_tool"
					args = map[string]any{"name": "list_sites", "arguments": args, "context": context}
				}
				text, _, isErr := resultOf(t, send(t, s, toolCall(name, args), nil, true))
				if isErr {
					t.Fatalf("on=%v wrapped=%v context=%v: %s", on, wrapped, context, text)
				}
				if on {
					e := <-s.analytics.queue
					params := e.Parameters.(map[string]any)["arguments"].(map[string]any)
					if _, exists := params["context"]; exists || e.UserIntent != "" {
						t.Fatalf("non-string context retained: %+v", e)
					}
				}
				// A tool's own context stays intact, including non-string values.
				own := map[string]any{"context": context}
				intent, present := takeAnalyticsContext(Tool{InputSchema: object(map[string]any{"context": map[string]any{}})}, own)
				if _, exists := own["context"]; !exists || intent != "" || present {
					t.Fatal("tool-owned context changed")
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
