package hackmcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// The generic organiser adapter calls its REST method and path with the
// caller's key, passes the body through unchanged, and returns a REST
// refusal as a tool error. One route of each shape proves the wiring.
func TestHackOrganiserToolRoutes(t *testing.T) {
	routes := map[string]hackRouteTool{}
	for _, route := range hackOrganiserRoutes() {
		routes[route.name] = route
	}
	for _, name := range []string{"hack_get_people", "hack_set_registration", "hack_export_participants", "hack_export_projects_archive"} {
		route, ok := routes[name]
		if !ok {
			t.Fatalf("no route %s", name)
		}
		t.Run(name, func(t *testing.T) {
			args := map[string]any{}
			path := route.path
			for _, p := range route.params() {
				args[p] = "test-value"
				path = strings.ReplaceAll(path, "{"+p+"}", url.PathEscape("test-value"))
			}
			if route.body {
				args["body"] = map[string]any{"sample": "unchanged"}
			}
			up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
			up.ans[route.method+" "+path] = func(w http.ResponseWriter, _ *http.Request) {
				switch {
				case route.csv:
					w.Header().Set("Content-Disposition", `attachment; filename="data.csv"`)
					_, _ = w.Write([]byte("a,b\n1,2\n"))
				case route.archive:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"url":"https://example.test/archive","expires_at":"2026-10-02T12:00:00Z","expires_in":600}`))
				default:
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true}`))
				}
			}
			s := newTestServer(up)
			text, result, bad := resultOf(t, sendMode(t, s, toolCall(name, args), mcp.CallerModeEvents))
			if bad || result == nil {
				t.Fatalf("success: %s", text)
			}
			last := up.last()
			if last.method != route.method || last.path != path || last.key != "user-key" {
				t.Fatalf("forwarded %s %s key %s", last.method, last.path, last.key)
			}
			expectedBody := ""
			if route.body {
				b, _ := json.Marshal(args["body"])
				expectedBody = string(b)
			}
			if last.body != expectedBody {
				t.Fatalf("body %q, want %q", last.body, expectedBody)
			}
			if route.archive && result["url"] != "https://example.test/archive" {
				t.Fatalf("archive link: %v", result)
			}
			up.ans[route.method+" "+path] = jsonReply(403, `{"error":"REST denied","code":"forbidden"}`)
			text, _, bad = resultOf(t, sendMode(t, s, toolCall(name, args), mcp.CallerModeEvents))
			if !bad || !strings.Contains(text, "forbidden") {
				t.Fatalf("REST refusal lost: %s", text)
			}
		})
	}
}

// hack_set_entry_excluded maps one boolean onto two REST actions and refuses
// a call that does not say which.
func TestHackSetEntryExcludedTool(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	up.ans["POST /v1/hack/events/spring/teams/alpha/entry/exclude"] = jsonReply(200, `{"team":{"slug":"alpha"},"excluded":true}`)
	up.ans["POST /v1/hack/events/spring/teams/alpha/entry/include"] = jsonReply(200, `{"team":{"slug":"alpha"},"excluded":false}`)
	s := newTestServer(up)
	for _, excluded := range []bool{true, false} {
		text, result, bad := resultOf(t, sendMode(t, s, toolCall("hack_set_entry_excluded", map[string]any{"slug": "spring", "team": "alpha", "excluded": excluded}), mcp.CallerModeEvents))
		resp, _ := result["response"].(map[string]any)
		action := "include"
		if excluded {
			action = "exclude"
		}
		if bad || resp["excluded"] != excluded || up.last().path != "/v1/hack/events/spring/teams/alpha/entry/"+action {
			t.Fatalf("excluded=%v: %s via %s", excluded, text, up.last().path)
		}
	}
	before := up.len()
	if _, _, bad := resultOf(t, sendMode(t, s, toolCall("hack_set_entry_excluded", map[string]any{"slug": "spring", "team": "alpha"}), mcp.CallerModeEvents)); !bad || up.len() != before {
		t.Fatal("missing excluded accepted or forwarded")
	}
}

// An organiser removes another judge's conflict by naming that judge.
func TestHackRemoveOtherJudgeConflictQuery(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	up.ans["DELETE /v1/hack/events/spring/conflicts/team-one"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("judge_user_id") != "judge-one" {
			t.Errorf("judge query %q", r.URL.RawQuery)
		}
		w.WriteHeader(204)
	}
	s := newTestServer(up)
	text, result, bad := resultOf(t, sendMode(t, s, toolCall("hack_remove_judge_conflict", map[string]any{"slug": "spring", "team_id": "team-one", "judge_user_id": "judge-one"}), mcp.CallerModeEvents))
	if bad || result["status"] != float64(204) {
		t.Fatalf("%s %v", text, result)
	}
}

// Tools that delete, replace or publish are advertised as destructive writes,
// so a client asks before running them.
func TestHackOrganiserConsequentialAnnotations(t *testing.T) {
	byName := map[string]mcp.Tool{}
	for _, tool := range Tools() {
		byName[tool.Name] = tool
	}
	for _, name := range []string{
		"hack_delete_event", "hack_delete_team", "hack_delete_announcement", "hack_delete_former_judge_scores",
		"hack_remove_person", "hack_regenerate_code", "hack_take_down_team_site", "hack_set_entry_excluded",
		"hack_set_content", "hack_set_assignments", "hack_publish_results",
	} {
		tool, ok := byName[name]
		if !ok || tool.Annotations["readOnlyHint"] != false || tool.Annotations["destructiveHint"] != true {
			t.Errorf("%s must be advertised as a destructive write", name)
		}
	}
	for _, name := range []string{"hack_get_people", "hack_get_my_conflicts", "hack_list_events"} {
		if byName[name].Annotations["readOnlyHint"] != true || byName[name].Annotations["destructiveHint"] != false {
			t.Errorf("%s must be a read", name)
		}
	}
}

// hack_delete_event forwards the confirmation the person gave.
func TestHackDeleteToolDestructiveConfirmation(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	up.ans["DELETE /v1/hack/events/spring"] = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.Unmarshal([]byte(up.last().body), &body); err != nil || body["confirm"] != "spring" {
			t.Errorf("confirmation not forwarded: %v %v", body, err)
		}
		w.WriteHeader(204)
	}
	text, result, bad := resultOf(t, sendMode(t, newTestServer(up), toolCall("hack_delete_event", map[string]any{"slug": "spring", "body": map[string]any{"confirm": "spring"}}), mcp.CallerModeEvents))
	if bad || result["status"] != float64(204) {
		t.Fatalf("delete result: %s", text)
	}
}
