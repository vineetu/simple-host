package mcp

import (
	"encoding/json"
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

func TestHackAuditAnnotationTuples(t *testing.T) {
	// Tuple order: readOnlyHint, destructiveHint, openWorldHint.
	want := map[string][3]bool{}
	for _, name := range []string{"hack_get_directory", "hack_get_public_event", "hack_get_public_results", "hack_get_voting", "hack_check_event_name"} {
		want[name] = [3]bool{true, false, true}
	}
	for _, name := range []string{"hack_update_event", "hack_edit_announcement", "hack_delete_announcement", "hack_post_announcement", "hack_set_tracks", "hack_rename_team", "hack_update_entry", "hack_set_entry_screenshot", "hack_delete_entry_screenshot", "hack_vote", "hack_set_results_view", "hack_set_event_website_mode", "hack_restore_team_site"} {
		want[name] = [3]bool{false, true, true}
	}
	for _, name := range []string{"hack_create_organiser_invite", "hack_accept_organiser_invite", "hack_lock_judging", "hack_unlock_judging", "hack_set_preferences", "hack_select_team"} {
		want[name] = [3]bool{false, true, false}
	}
	// Deadline pins fill previously unset state, without replacing existing pins.
	for _, name := range []string{"hack_get_event", "hack_application_status", "hack_get_team_status", "hack_get_teams", "hack_get_entries", "hack_get_judge_queue"} {
		want[name] = [3]bool{false, false, false}
	}
	for _, name := range []string{"hack_get_people", "hack_get_my_conflicts", "hack_get_judging_dashboard", "hack_get_preferences", "hack_preview_organiser_invite", "hack_export_own_team_archive", "hack_export_projects_archive"} {
		want[name] = [3]bool{true, false, false}
	}
	for _, tool := range HackTools() {
		expected, ok := want[tool.Name]
		if !ok {
			continue
		}
		for i, hint := range []string{"readOnlyHint", "destructiveHint", "openWorldHint"} {
			if tool.Annotations[hint] != expected[i] {
				t.Errorf("%s %s = %v, want %v", tool.Name, hint, tool.Annotations[hint], expected[i])
			}
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("tools missing from annotation check: %v", want)
	}
}

func TestHackDeleteToolDestructiveConfirmation(t *testing.T) {
	for _, tool := range HackOrganiserTools() {
		if tool.Name != "hack_delete_event" {
			continue
		}
		if tool.Annotations["destructiveHint"] != true || tool.Annotations["readOnlyHint"] != false {
			t.Fatalf("delete annotations: %v", tool.Annotations)
		}
		if !strings.Contains(tool.Description, "confirm with the person first") || !strings.Contains(tool.Description, "body.confirm") {
			t.Fatal("delete must explain human confirmation")
		}
		up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
		up.ans["DELETE /v1/hack/events/spring"] = func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.Unmarshal([]byte(up.last().body), &body); err != nil || body["confirm"] != "spring" {
				t.Fatalf("confirmation not forwarded: %v %v", body, err)
			}
			w.WriteHeader(204)
		}
		s := newTestServer(up)
		text, result, bad := resultOf(t, sendMode(t, s, toolCall(tool.Name, map[string]any{"slug": "spring", "body": map[string]any{"confirm": "spring"}}), CallerModeEvents))
		if bad || result["status"] != float64(204) {
			t.Fatalf("delete result: %s", text)
		}
		return
	}
	t.Fatal("delete tool missing")
}
