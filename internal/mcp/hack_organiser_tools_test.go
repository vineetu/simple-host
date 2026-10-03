package mcp

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Every organiser adapter must call the exact REST method/path, preserve its
// JSON body and refusal, and return a result matching its advertised schema.
func TestHackOrganiserToolRoutes(t *testing.T) {
	routes := hackOrganiserRoutes()
	tools := HackOrganiserTools()
	if len(tools) != len(routes) {
		t.Fatalf("%d tools for %d routes", len(tools), len(routes))
	}
	for i, route := range routes {
		tool := tools[i]
		t.Run(route.name, func(t *testing.T) {
			if tool.Name != route.name {
				t.Fatalf("name %q", tool.Name)
			}
			if route.body && hackRouteBodyDescriptions[route.name] == "" {
				t.Fatal("body shape is not described")
			}
			if err := CheckOutputSchema(tool.InputSchema); err != nil {
				t.Fatal(err)
			}
			if err := CheckOutputSchema(tool.OutputSchema); err != nil {
				t.Fatal(err)
			}
			if route.name == "hack_create_team_key" {
				// REST/UI still create keys; the connector never returns one into chat.
				return
			}
			args := map[string]any{}
			path := route.path
			for _, p := range route.params {
				value := "test-value"
				if p == "slug" {
					value = "Spring"
				}
				args[p] = value
				if p == "slug" {
					value = "spring"
				}
				path = strings.ReplaceAll(path, "{"+p+"}", url.PathEscape(value))
			}
			if route.body {
				args["body"] = map[string]any{"sample": "unchanged"}
			}
			up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
			up.ans[route.method+" "+path] = func(w http.ResponseWriter, _ *http.Request) {
				if route.csv {
					w.Header().Set("Content-Disposition", `attachment; filename="data.csv"`)
					_, _ = w.Write([]byte("a,b\n1,2\n"))
					return
				}
				if route.archive {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"url":"https://example.test/archive","expires_at":"2026-10-02T12:00:00Z","expires_in":600}`))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":true}`))
			}
			s := newTestServer(up)
			text, result, bad := resultOf(t, sendMode(t, s, toolCall(route.name, args), CallerModeEvents))
			if bad || result == nil {
				t.Fatalf("success: %s", text)
			}
			if err := ValidateOutput(tool.OutputSchema, result, nil); err != nil {
				t.Fatalf("output: %v", err)
			}
			last := up.last()
			if last.method != route.method || last.path != path || last.key != "user-key" {
				t.Fatalf("forwarded %s %s key %s", last.method, last.path, last.key)
			}
			if route.body && last.body != `{"sample":"unchanged"}` {
				t.Fatalf("body %s", last.body)
			}
			if !route.body && last.body != "" {
				t.Fatalf("unexpected body %s", last.body)
			}
			if route.archive {
				if result["url"] != "https://example.test/archive" || result["expires_in"] != float64(600) {
					t.Fatalf("archive link: %v", result)
				}
			}
			up.ans[route.method+" "+path] = jsonReply(403, `{"error":"REST denied","code":"forbidden"}`)
			text, _, bad = resultOf(t, sendMode(t, s, toolCall(route.name, args), CallerModeEvents))
			if !bad || !strings.Contains(text, "REST denied") || !strings.Contains(text, "forbidden") {
				t.Fatalf("REST refusal lost: %s", text)
			}
		})
	}
}

func TestHackRemoveOtherJudgeConflictQuery(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	path := "/v1/hack/events/spring/conflicts/team-one"
	up.ans["DELETE "+path] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("judge_user_id") != "judge-one" {
			t.Errorf("judge query %q", r.URL.RawQuery)
		}
		w.WriteHeader(204)
	}
	s := newTestServer(up)
	text, result, bad := resultOf(t, sendMode(t, s, toolCall("hack_remove_judge_conflict", map[string]any{"slug": "spring", "team_id": "team-one", "judge_user_id": "judge-one"}), CallerModeEvents))
	if bad || result["status"] != float64(204) {
		t.Fatalf("%s %v", text, result)
	}
}

func TestHackOrganiserConsequentialAnnotations(t *testing.T) {
	byName := map[string]Tool{}
	for _, tool := range HackOrganiserTools() {
		byName[tool.Name] = tool
	}
	for _, name := range []string{
		"hack_delete_event", "hack_regenerate_code", "hack_remove_person",
		"hack_delete_team", "hack_take_down_team_site", "hack_revoke_person_key",
		"hack_set_content", "hack_set_assignments", "hack_set_panels",
		"hack_set_judge_conflict", "hack_publish_results", "hack_create_team_key",
	} {
		tool, ok := byName[name]
		if !ok || tool.Annotations["readOnlyHint"] != false || tool.Annotations["destructiveHint"] != true {
			t.Errorf("%s must be advertised as a destructive write", name)
		}
	}
	for _, name := range []string{"hack_post_announcement", "hack_publish_results", "hack_take_down_team_site", "hack_restore_team_site"} {
		tool := byName[name]
		if tool.Annotations["openWorldHint"] != true || !strings.Contains(strings.ToLower(tool.Description), "ask") {
			t.Errorf("%s must say to ask before an outward action", name)
		}
	}
}
