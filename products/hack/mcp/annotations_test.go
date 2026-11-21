package hackmcp

// The annotation rule, the same one Simple Host's tools follow
// (host/mcp/annotations_test.go). Sources: the MCP ToolAnnotations schema
// (https://modelcontextprotocol.io/specification/2025-11-25/schema#toolannotations)
// and OpenAI's app submission guidelines, section 6.3
// (https://developers.openai.com/apps-sdk/app-submission-guidelines).
//
//   - title: every tool has a short human title.
//   - readOnlyHint: true only if the tool changes nothing anywhere. A read
//     that records something as a side effect (deadline pins) is a write.
//   - destructiveHint: true if the tool deletes or overwrites data, takes
//     something offline, or removes access, in any mode or through its
//     defaults. Being able to undo it does not make it false. False only for
//     purely additive writes, and always false for reads.
//   - openWorldHint: true if the tool reaches outside the service (sends
//     email, fetches a URL, DNS or certificates), or changes what the public
//     internet sees (an event's public page, results, a team site, a join
//     link). Reading data inside the service is false, even public data.
//   - idempotentHint: true only when calling again with the same arguments
//     has no further effect. Always true for reads.
//
// Every hint is an explicit boolean. The table below names each Hack tool's
// class from what its REST call does: GET is a read; DELETE, PUT over an
// existing value, or a change of access is destructive; a POST that only adds
// is a write. The website and storage tools a Hack connection also offers are
// Simple Host's, and keep Host's hints: Host's test owns their rows.

import (
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/mcp"
)

// hackAnnotationTable: tool name -> "read", "write" or "destructive", with
// "+open" for openWorldHint and "+idem" for an idempotent write.
var hackAnnotationTable = map[string]string{
	"hack_get_navigation":              "read",
	"hack_list_events":                 "read",
	"hack_check_event_name":            "read",
	"hack_create_event":                "write+open",
	"hack_get_event":                   "write+idem",
	"hack_update_event":                "destructive+open+idem",
	"hack_set_event_stage":             "destructive+open+idem",
	"hack_list_open_teams":             "read",
	"hack_ask_to_join":                 "destructive",
	"hack_invite_to_team":              "destructive",
	"hack_accept_team_request":         "destructive",
	"hack_decline_team_request":        "destructive",
	"hack_withdraw_team_request":       "destructive",
	"hack_remove_teammate":             "destructive",
	"hack_set_looking":                 "destructive+idem",
	"hack_preview_team_link":           "read",
	"hack_update_my_team":              "destructive+idem",
	"hack_lock_my_team":                "destructive+idem",
	"hack_set_teammate_admin":          "destructive+idem",
	"hack_new_my_team_link":            "destructive",
	"hack_preview_join":                "read",
	"hack_join_event":                  "write",
	"hack_preview_application":         "read",
	"hack_apply_to_event":              "write",
	"hack_application_status":          "write+idem",
	"hack_create_team":                 "write",
	"hack_join_team":                   "write",
	"hack_leave_team":                  "destructive",
	"hack_choose_track":                "destructive+idem",
	"hack_get_team_status":             "write+idem",
	"hack_get_my_teams":                "read",
	"hack_get_my_team_messages":        "read",
	"hack_message_organisers":          "write",
	"hack_get_my_todos":                "read",
	"hack_dismiss_notices":             "destructive+idem",
	"hack_set_email_updates":           "destructive+idem",
	"hack_preview_judge":               "read",
	"hack_join_judge":                  "write",
	"hack_preview_leave_event":         "read",
	"hack_leave_event":                 "destructive+open",
	"hack_get_directory":               "read",
	"hack_get_content":                 "read",
	"hack_set_content":                 "destructive+open+idem",
	"hack_get_announcements":           "read",
	"hack_edit_announcement":           "destructive+open+idem",
	"hack_delete_announcement":         "destructive+open+idem",
	"hack_post_announcement":           "destructive+open",
	"hack_get_registration":            "read",
	"hack_set_registration":            "destructive+idem",
	"hack_get_applications":            "read",
	"hack_decide_application":          "destructive",
	"hack_get_tracks":                  "read",
	"hack_set_tracks":                  "destructive+open+idem",
	"hack_set_directory_listing":       "destructive+open+idem",
	"hack_regenerate_code":             "destructive+open",
	"hack_delete_event":                "destructive+open",
	"hack_get_people":                  "read",
	"hack_remove_person":               "destructive",
	"hack_fill_teams":                  "destructive",
	"hack_get_teams":                   "write+idem",
	"hack_move_member":                 "destructive",
	"hack_remove_team_member":          "destructive",
	"hack_rename_team":                 "destructive+open",
	"hack_lock_team":                   "destructive+idem",
	"hack_set_team_admin":              "destructive+idem",
	"hack_new_team_link":               "destructive",
	"hack_delete_team":                 "destructive+open",
	"hack_set_team_deadline":           "destructive+idem",
	"hack_take_down_team_site":         "destructive+open+idem",
	"hack_restore_team_site":           "destructive+open+idem",
	"hack_create_organiser_invite":     "destructive",
	"hack_revoke_organiser_invite":     "destructive+idem",
	"hack_preview_organiser_invite":    "read",
	"hack_accept_organiser_invite":     "destructive",
	"hack_remove_organiser":            "destructive",
	"hack_export_participants":         "read",
	"hack_export_teams":                "read",
	"hack_export_projects_archive":     "read",
	"hack_export_own_team_archive":     "read",
	"hack_get_usage":                   "read",
	"hack_message_team":                "destructive+open",
	"hack_get_team_messages":           "read",
	"hack_get_reminder_settings":       "read",
	"hack_set_reminder_settings":       "destructive+idem",
	"hack_set_event_icon":              "destructive+open+idem",
	"hack_clear_event_icon":            "destructive+open+idem",
	"hack_get_public_event":            "read",
	"hack_get_preferences":             "read",
	"hack_set_preferences":             "destructive+idem",
	"hack_get_checklist":               "write+idem",
	"hack_set_checklist_not_needed":    "destructive+idem",
	"hack_acknowledge_checklist_item":  "write+idem",
	"hack_get_rubric":                  "read",
	"hack_set_rubric":                  "destructive+idem",
	"hack_export_scores":               "read",
	"hack_export_results":              "read",
	"hack_get_submissions":             "read",
	"hack_get_entry":                   "read",
	"hack_update_entry":                "destructive+open+idem",
	"hack_submit_entry":                "write+idem",
	"hack_unsubmit_entry":              "destructive+idem",
	"hack_delete_entry_screenshot":     "destructive+open+idem",
	"hack_get_voting":                  "read",
	"hack_get_my_vote":                 "read",
	"hack_vote":                        "destructive+open+idem",
	"hack_get_my_results":              "read",
	"hack_get_judge_queue":             "write+idem",
	"hack_get_judge_scores":            "read",
	"hack_score_team":                  "destructive+idem",
	"hack_get_my_conflicts":            "read",
	"hack_declare_my_conflict":         "destructive+idem",
	"hack_remove_my_conflict":          "destructive+idem",
	"hack_get_entry_screenshot":        "read",
	"hack_set_entry_screenshot":        "destructive+open+idem",
	"hack_get_voting_settings":         "read",
	"hack_set_voting_settings":         "destructive+open+idem",
	"hack_get_entries":                 "write+idem",
	"hack_get_entry_rules":             "read",
	"hack_set_entry_rules":             "destructive+idem",
	"hack_get_team_screenshot":         "read",
	"hack_get_judging_settings":        "read",
	"hack_set_judging_settings":        "destructive+open",
	"hack_generate_assignments":        "destructive",
	"hack_get_assignments":             "read",
	"hack_set_assignments":             "destructive+idem",
	"hack_get_panels":                  "read",
	"hack_set_panels":                  "destructive+idem",
	"hack_preview_judging_assignments": "read",
	"hack_get_conflicts":               "read",
	"hack_set_judge_conflict":          "destructive",
	"hack_remove_judge_conflict":       "destructive+idem",
	"hack_get_judging_dashboard":       "read",
	"hack_delete_former_judge_scores":  "destructive",
	"hack_publish_results":             "destructive+open",
	"hack_set_results_view":            "destructive+open+idem",
	"hack_get_public_results":          "read",
	"hack_export_entries":              "read",
	"hack_set_entry_excluded":          "destructive+idem",
	"hack_get_page_recipe":             "read",
	"hack_set_event_website_mode":      "destructive+open+idem",
	"hack_app_preview_results":         "read",
	"hack_app_get_results":             "read",
	"hack_app_publish_results":         "destructive+open",
	"hack_app_get_people":              "read",
	"hack_app_get_applications":        "read",
	"hack_app_get_registration":        "read",
	"hack_app_get_judging_settings":    "read",
	"hack_app_get_conflicts":           "read",
	"hack_app_get_teams":               "write+idem",
	"hack_app_get_assignments":         "read",
	"hack_app_get_judging_dashboard":   "write+idem",
	"hack_app_set_event_stage":         "destructive+open+idem",
	"hack_app_regenerate_code":         "destructive+open",
	"hack_app_create_organiser_invite": "destructive",
	"hack_app_set_judging_ends_at":     "destructive+open+idem",
	"hack_app_get_rubric":              "read",
	"hack_app_get_judge_queue":         "write+idem",
	"hack_app_get_judge_scores":        "read",
	"hack_app_get_my_conflicts":        "read",
	"hack_show_judge":                  "write+idem",
	"hack_show_organiser":              "write+idem",
}

func hackAnnotationClass(a map[string]any) (string, []string) {
	var problems []string
	hint := func(key string) bool {
		v, ok := a[key].(bool)
		if !ok {
			problems = append(problems, key+" is not an explicit boolean")
		}
		return v
	}
	read, destructive, idem, open := hint("readOnlyHint"), hint("destructiveHint"), hint("idempotentHint"), hint("openWorldHint")
	class := "write"
	switch {
	case read:
		class = "read"
		if destructive {
			problems = append(problems, "a read cannot be destructive")
		}
		if !idem {
			problems = append(problems, "a read is idempotent")
		}
	case destructive:
		class = "destructive"
	}
	if open {
		class += "+open"
	}
	if !read && idem {
		class += "+idem"
	}
	return class, problems
}

// TestHackToolAnnotationsFollowTheRule walks every tool Simple Hack offers:
// every declaration (with events and teams on, the app-only family and the
// website variants included) and what each kind of connection lists.
func TestHackToolAnnotationsFollowTheRule(t *testing.T) {
	host := map[string]string{}
	for _, entry := range mcp.Inventory(mcp.Contribution{}) {
		host[entry.Name], _ = hackAnnotationClass(entry.Annotations)
	}
	seen := map[string]bool{}
	check := func(where, name, title string, annotations map[string]any) {
		t.Helper()
		if strings.TrimSpace(title) == "" {
			t.Errorf("%s %s: no human title", where, name)
		}
		got, problems := hackAnnotationClass(annotations)
		for _, p := range problems {
			t.Errorf("%s %s: %s", where, name, p)
		}
		want, ok := hackAnnotationTable[name]
		if !ok {
			want, ok = host[name]
		} else {
			seen[name] = true
		}
		if !ok {
			t.Errorf("%s %s: no row in the annotation table (%s now)", where, name, got)
		} else if got != want {
			t.Errorf("%s %s: annotations say %s, the rule's table says %s", where, name, got, want)
		}
	}
	for _, entry := range mcp.Inventory(Contribution()) {
		if entry.Edition == "host" || entry.Edition == "screen" {
			if _, own := hackAnnotationTable[entry.Name]; own {
				t.Errorf("%s is a Simple Host tool: its row belongs in host/mcp/annotations_test.go", entry.Name)
			}
		}
		check("declared", entry.Name, entry.Title, entry.Annotations)
	}
	limits := mcp.DefaultLimits()
	for _, tool := range mcp.Tools() {
		if variant := Contribution().SiteVariants[tool.Name]; variant != nil {
			v := variant(tool, limits)
			check("website variant", v.Name, v.Title, v.Annotations)
		}
	}
	s := newTestServer(nil)
	for _, caller := range []mcp.Caller{
		{APIKey: "user-key"}, {APIKey: "user-key", AppName: "ChatGPT"},
		{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant"},
		{APIKey: "user-key", Mode: mcp.CallerModeEvents, GrantID: "grant", AppName: "ChatGPT"},
	} {
		for _, tool := range listedTools(t, rpcAs(t, s, caller, "2025-06-18", "tools/list", nil, false)) {
			annotations, _ := tool["annotations"].(map[string]any)
			title, _ := tool["title"].(string)
			check("listed", tool["name"].(string), title, annotations)
		}
	}
	for name := range hackAnnotationTable {
		if !seen[name] {
			t.Errorf("table row %s names no Hack tool", name)
		}
	}
}
