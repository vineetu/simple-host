package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
)

// This file is SIM-384's regression test: hack_create_event once returned
// "Structured content does not match the tool's output schema: data must
// NOT have additional properties" to a client whose cached copy of the
// schema predated judging_ends_at, even though the event was created. The
// actual fix is openOutputSchemaForWire (outputs.go), applied in every
// Tool's MarshalJSON: it opens additionalProperties: false to true on the
// WIRE copy only, so an added field can never again turn a successful call
// into a reported failure for a client holding an older schema. Every test
// in this package still validates against the AUTHORED (closed) schema, so
// a tool that starts returning an undeclared field is still caught here --
// strictness didn't go away, it just moved off the wire.
//
// Most of HackTools() already gets that strict check somewhere: the base
// event/entry tools in TestHackToolDispatch, every hackRouteTool-backed
// organiser tool in TestHackOrganiserToolRoutes, hack_set_entry_excluded in
// TestHackSetEntryExcludedTool, who_am_i in hack_whoami_test.go, a good
// number of organiser/member tools via hack_results_test.go's
// assertHackResult, the member tools in hack_member_tools_test.go, and the
// app-only twins and show tools in hack_app_only_tools_test.go/
// hack_apps_judge_test.go/hack_apps_organiser_test.go (all extended by
// SIM-384 to add the missing ValidateOutput call). What's left -- the
// connection tool, the two website-mode tool families, and the storage/
// recipe family -- is covered below. TestHackToolInventoryIsReviewed is the
// part that keeps this honest for tools nobody has written a test for yet:
// it fails the moment HackTools() gains or loses a name this file doesn't
// know about, so a new hack_* tool can't go untested by accident.

func TestHackSelectTeamOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){}}
	s := NewServer(Config{
		Upstream: up, APIHost: "simple-host.app", ContentOrigin: "https://sites.simple-host.app",
		SiteBases: []string{"simple-host.app"}, SkillVersion: "9.9.9", ServerName: "simple-host", Version: "9.9.9",
		SelectHackTeam: func(_ context.Context, _ Caller, _ string) error { return nil },
	})
	tool := s.hackByName["hack_select_team"]
	call := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil),
		caller: Caller{APIKey: "personal-key", Mode: CallerModeEvents, GrantID: "grant-1", UserID: "user-1"}}
	out, err := tool.run(call, map[string]any{"team_id": "11111111-1111-4111-8111-111111111111"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Structured["team_id"] != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("team_id: %v", out.Structured)
	}
	if err := ValidateOutput(tool.OutputSchema, out.Structured, nil); err != nil {
		t.Fatalf("structuredContent does not match outputSchema: %v", err)
	}
}

// TestHackWebEventToolsOutputMatchesSchema covers hackWebEventTools' five
// tools (hack_event_tools.go): hack_set_event_icon, hack_clear_event_icon,
// hack_get_public_event, hack_get_preferences, hack_set_preferences. Every
// one of these declares the deliberately open generic schema
// ({"type":"object","additionalProperties":true}), so ValidateOutput can
// never fail here on an added field -- the point of exercising them for
// real anyway is that a future change swapping one to a closed, structured
// schema (the thing that makes this bug class possible at all) trips this
// test the moment its real output stops being a bare JSON object.
func TestHackWebEventToolsOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"PUT /v1/hack/events/spring/icon":    jsonReply(200, `{"icon_url":"https://x/icon.png"}`),
		"DELETE /v1/hack/events/spring/icon": jsonReply(200, `{"icon_url":null}`),
		"GET /v1/hack/events/spring/public":  jsonReply(200, `{"slug":"spring","title":"Spring"}`),
		"GET /v1/hack/preferences":           jsonReply(200, `{"theme":"system"}`),
		"PATCH /v1/hack/preferences":         jsonReply(200, `{"theme":"dark"}`),
	}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range hackWebEventTools() {
		byName[tool.Name] = tool
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"hack_set_event_icon", map[string]any{"slug": "spring", "content_type": "image/png", "image_base64": "AAAA"}},
		{"hack_clear_event_icon", map[string]any{"slug": "spring"}},
		{"hack_get_public_event", map[string]any{"slug": "spring"}},
		{"hack_get_preferences", map[string]any{}},
		{"hack_set_preferences", map[string]any{"theme": "dark"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatal("tool missing from hackWebEventTools()")
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
		})
	}
}

// TestHackWebsiteModeToolsOutputMatchesSchema covers famSites' three tools
// (hack_web_tools.go): hack_get_event_website, hack_publish_event_website,
// hack_set_event_website_mode. Same reasoning as the test above: the schema
// is the open generic one, so this is mainly a tripwire for a future
// structured schema on one of these.
func TestHackWebsiteModeToolsOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"GET /v1/hack/events/spring/website":       jsonReply(200, `{"has_custom_page":true,"mode":"custom"}`),
		"PATCH /v1/hack/events/spring/website":     jsonReply(200, `{"mode":"builtin"}`),
		"PUT /v1/hack/events/spring/website/files": jsonReply(200, `{"active_version":2}`),
	}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range hackWebTools() {
		byName[tool.Name] = tool
	}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"hack_get_event_website", map[string]any{"slug": "spring"}},
		{"hack_publish_event_website", map[string]any{"slug": "spring", "files": map[string]any{"index.html": "<h1>x</h1>"}}},
		{"hack_set_event_website_mode", map[string]any{"slug": "spring", "mode": "builtin"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tool, ok := byName[tc.name]
			if !ok {
				t.Fatal("tool missing from hackWebTools()")
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(tc.name, tc.args), CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", tc.name, isErr, text)
			}
			if err := ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", tc.name, err)
			}
		})
	}
}

// TestHackStorageAndRecipeToolsOutputMatchesSchema covers hackStorageTools()
// (hack_event_storage_* and hack_get_page_recipe). hack_event_storage_*'s
// outputSchema is the same low-risk {"response": <any>, "status": <int>}
// wrapper the organiser route tools use (storage_tools.go's
// storageToolsFor), and hack_get_page_recipe shares its OutputSchema object
// directly with Host's own get_page_recipe (already exhaustively checked by
// TestEveryToolResultMatchesItsOutputSchema and TestOutputSchemasMatchRealResults
// in internal/handler), so one representative call of each REST shape (KV,
// SQL, recipe) is enough to prove the hack_-prefixed wrapper forwards
// correctly rather than re-proving the schema itself.
func TestHackStorageAndRecipeToolsOutputMatchesSchema(t *testing.T) {
	up := &scriptUpstream{ans: map[string]func(http.ResponseWriter, *http.Request){
		"PUT /v1/hack/events/spring/website/storage/kv/store/keys/count": jsonReply(200, `{"key":"count","value":3}`),
		"GET /v1/hack/events/spring/website/storage/kv/store/keys/count": jsonReply(200, `{"key":"count","value":3}`),
		"POST /v1/hack/events/spring/website/storage/sqlite/store/query": jsonReply(200, `{"columns":["n"],"rows":[[1]]}`),
	}}
	s := newTestServer(up)
	byName := map[string]Tool{}
	for _, tool := range hackStorageTools() {
		byName[tool.Name] = tool
	}
	if _, ok := byName["hack_get_page_recipe"]; !ok {
		t.Fatal("hack_get_page_recipe missing from hackStorageTools()")
	}
	cases := []string{"hack_event_storage_put_kv", "hack_event_storage_get_kv", "hack_event_storage_sql_query"}
	argsFor := map[string]map[string]any{
		"hack_event_storage_put_kv":    {"event": "spring", "name": "store", "key": "count", "value": 3},
		"hack_event_storage_get_kv":    {"event": "spring", "name": "store", "key": "count"},
		"hack_event_storage_sql_query": {"event": "spring", "name": "store", "sql": "SELECT n FROM t"},
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			tool, ok := byName[name]
			if !ok {
				t.Fatalf("%s missing from hackStorageTools()", name)
			}
			text, structured, isErr := resultOf(t, sendMode(t, s, toolCall(name, argsFor[name]), CallerModeEvents))
			if isErr || structured == nil {
				t.Fatalf("%s: isErr=%v text=%s", name, isErr, text)
			}
			if err := ValidateOutput(tool.OutputSchema, structured, nil); err != nil {
				t.Fatalf("%s: structuredContent does not match outputSchema: %v", name, err)
			}
		})
	}
	recipeTool := byName["hack_get_page_recipe"]
	recipeCall := &call{server: s, orig: httptest.NewRequest("POST", "/mcp", nil), caller: Caller{APIKey: "k", Mode: CallerModeEvents}}
	out, err := recipeTool.run(recipeCall, map[string]any{"topic": "wall"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateOutput(recipeTool.OutputSchema, out.Structured, nil); err != nil {
		t.Fatalf("hack_get_page_recipe: structuredContent does not match outputSchema: %v", err)
	}
}

// TestHackToolInventoryIsReviewed is the backstop for this class of bug
// (SIM-384): hackToolInventorySnapshot below is a frozen list of every name
// HackTools() returned when this test was last updated. A name added to or
// removed from HackTools() without a matching, reviewed update here fails
// loudly -- naming exactly which tool -- so a brand new hack_* tool can
// never go unnoticed by every test in this file (and, since every family
// above and every test this file's header comment lists already drives its
// own tools through ValidateOutput in strict mode, "noticed" means someone
// has to put the new tool in one of those paths, or add a new one, before
// this list can be updated to include it). This is deliberately a plain
// slice literal, not something computed from the same registry functions
// HackTools() itself calls -- that would make the comparison trivially
// true and catch nothing.
func TestHackToolInventoryIsReviewed(t *testing.T) {
	want := append([]string{}, hackToolInventorySnapshot...)
	sort.Strings(want)
	got := make([]string, 0, len(want))
	for _, tool := range HackTools() {
		got = append(got, tool.Name)
	}
	sort.Strings(got)

	wantSet := map[string]bool{}
	for _, name := range want {
		wantSet[name] = true
	}
	gotSet := map[string]bool{}
	for _, name := range got {
		gotSet[name] = true
	}
	for _, name := range got {
		if !wantSet[name] {
			t.Errorf("%s is new in HackTools() and not in hackToolInventorySnapshot: add real output-schema coverage for it (see this file's header), then add its name to the snapshot", name)
		}
	}
	for _, name := range want {
		if !gotSet[name] {
			t.Errorf("%s is in hackToolInventorySnapshot but no longer in HackTools(): remove it from the snapshot", name)
		}
	}
}

var hackToolInventorySnapshot = []string{
	"hack_accept_organiser_invite",
	"hack_accept_team_request",
	"hack_app_create_organiser_invite",
	"hack_app_get_applications",
	"hack_app_get_assignments",
	"hack_app_get_conflicts",
	"hack_app_get_judge_queue",
	"hack_app_get_judge_scores",
	"hack_app_get_judging_dashboard",
	"hack_app_get_judging_settings",
	"hack_app_get_my_conflicts",
	"hack_app_get_people",
	"hack_app_get_registration",
	"hack_app_get_rubric",
	"hack_app_get_teams",
	"hack_app_regenerate_code",
	"hack_app_set_event_stage",
	"hack_app_set_judging_ends_at",
	"hack_application_status",
	"hack_apply_to_event",
	"hack_ask_to_join",
	"hack_check_event_name",
	"hack_choose_track",
	"hack_clear_event_icon",
	"hack_create_event",
	"hack_create_organiser_invite",
	"hack_create_team",
	"hack_create_team_key",
	"hack_decide_application",
	"hack_declare_my_conflict",
	"hack_decline_team_request",
	"hack_delete_announcement",
	"hack_delete_entry_screenshot",
	"hack_delete_event",
	"hack_delete_former_judge_scores",
	"hack_delete_team",
	"hack_dismiss_notices",
	"hack_edit_announcement",
	"hack_event_storage_delete_file",
	"hack_event_storage_delete_kv",
	"hack_event_storage_delete_resource",
	"hack_event_storage_file_download_link",
	"hack_event_storage_get_kv",
	"hack_event_storage_get_usage",
	"hack_event_storage_list_file_objects",
	"hack_event_storage_list_kv_keys",
	"hack_event_storage_list_resources",
	"hack_event_storage_put_file",
	"hack_event_storage_put_kv",
	"hack_event_storage_set_resource",
	"hack_event_storage_sql_execute",
	"hack_event_storage_sql_query",
	"hack_event_storage_sql_schema",
	"hack_export_entries",
	"hack_export_own_team_archive",
	"hack_export_participants",
	"hack_export_projects_archive",
	"hack_export_results",
	"hack_export_scores",
	"hack_export_teams",
	"hack_fill_teams",
	"hack_generate_assignments",
	"hack_get_announcements",
	"hack_get_applications",
	"hack_get_assignments",
	"hack_get_conflicts",
	"hack_get_content",
	"hack_get_directory",
	"hack_get_entries",
	"hack_get_entry",
	"hack_get_entry_rules",
	"hack_get_entry_screenshot",
	"hack_get_event",
	"hack_get_event_website",
	"hack_get_judge_queue",
	"hack_get_judge_scores",
	"hack_get_judging_dashboard",
	"hack_get_judging_settings",
	"hack_get_my_conflicts",
	"hack_get_my_results",
	"hack_get_my_team_messages",
	"hack_get_my_teams",
	"hack_get_my_todos",
	"hack_get_my_vote",
	"hack_get_page_recipe",
	"hack_get_panels",
	"hack_get_people",
	"hack_get_preferences",
	"hack_get_public_event",
	"hack_get_public_results",
	"hack_get_registration",
	"hack_get_reminder_settings",
	"hack_get_rubric",
	"hack_get_team_key",
	"hack_get_team_messages",
	"hack_get_team_screenshot",
	"hack_get_team_status",
	"hack_get_teams",
	"hack_get_tracks",
	"hack_get_usage",
	"hack_get_voting",
	"hack_get_voting_settings",
	"hack_invite_to_team",
	"hack_join_event",
	"hack_join_judge",
	"hack_join_team",
	"hack_leave_event",
	"hack_leave_team",
	"hack_list_events",
	"hack_list_open_teams",
	"hack_lock_judging",
	"hack_lock_my_team",
	"hack_lock_team",
	"hack_message_team",
	"hack_move_member",
	"hack_new_my_team_link",
	"hack_new_team_link",
	"hack_post_announcement",
	"hack_preview_application",
	"hack_preview_join",
	"hack_preview_judge",
	"hack_preview_judging_assignments",
	"hack_preview_leave_event",
	"hack_preview_organiser_invite",
	"hack_preview_team_link",
	"hack_publish_event_website",
	"hack_publish_results",
	"hack_regenerate_code",
	"hack_remove_judge_conflict",
	"hack_remove_my_conflict",
	"hack_remove_organiser",
	"hack_remove_person",
	"hack_remove_team_member",
	"hack_remove_teammate",
	"hack_rename_team",
	"hack_restore_team_site",
	"hack_revoke_organiser_invite",
	"hack_revoke_person_key",
	"hack_revoke_team_key",
	"hack_score_team",
	"hack_select_team",
	"hack_set_assignments",
	"hack_set_content",
	"hack_set_directory_listing",
	"hack_set_email_updates",
	"hack_set_entry_excluded",
	"hack_set_entry_rules",
	"hack_set_entry_screenshot",
	"hack_set_event_icon",
	"hack_set_event_stage",
	"hack_set_event_website_mode",
	"hack_set_judge_conflict",
	"hack_set_judging_settings",
	"hack_set_looking",
	"hack_set_panels",
	"hack_set_preferences",
	"hack_set_registration",
	"hack_set_reminder_settings",
	"hack_set_results_view",
	"hack_set_rubric",
	"hack_set_team_admin",
	"hack_set_team_deadline",
	"hack_set_teammate_admin",
	"hack_set_tracks",
	"hack_set_voting_settings",
	"hack_show_judge",
	"hack_show_organiser",
	"hack_submit_entry",
	"hack_take_down_team_site",
	"hack_unlock_judging",
	"hack_unsubmit_entry",
	"hack_update_entry",
	"hack_update_event",
	"hack_update_my_team",
	"hack_vote",
	"hack_withdraw_team_request",
}
