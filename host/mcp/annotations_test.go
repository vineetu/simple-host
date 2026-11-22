package mcp

// The annotation rule. Every Simple Host tool follows it, and this file is
// where it is written down; products/hack/mcp/annotations_test.go repeats it
// for Simple Hack's own tools. Sources: the MCP ToolAnnotations schema
// (https://modelcontextprotocol.io/specification/2025-11-25/schema#toolannotations)
// and OpenAI's app submission guidelines, section 6.3
// (https://developers.openai.com/apps-sdk/app-submission-guidelines), which
// require all three of readOnlyHint, destructiveHint and openWorldHint.
//
//   - title: every tool has a short human title.
//   - readOnlyHint: true only if the tool changes nothing anywhere. A read
//     that records something as a side effect is a write.
//   - destructiveHint: true if the tool deletes or overwrites data, takes
//     something offline, or removes access, in any mode or through its
//     defaults. Being able to undo it does not make it false. False only for
//     purely additive writes, and always false for reads.
//   - openWorldHint: true if the tool reaches outside the service (sends
//     email, fetches a URL, DNS or certificates), or changes what the public
//     internet sees (publishes, unpublishes, re-addresses or changes who can
//     open a site or its saved data; OpenAI: "true for public or open-ended
//     entities"). Reading data inside the service is false, even public data.
//   - idempotentHint: true only when calling again with the same arguments
//     has no further effect. Always true for reads.
//
// Every hint is an explicit boolean. The table below names each tool's class,
// from what its REST call does: GET is a read; DELETE, PUT over an existing
// value, or a change of access is destructive; a POST that only adds is a
// write. A new tool fails this test until it has a row.

import (
	"strings"
	"testing"
)

// hostAnnotationTable: tool name -> "read", "write" or "destructive", with
// "+open" for openWorldHint and "+idem" for an idempotent write.
var hostAnnotationTable = map[string]string{
	"who_am_i":                   "read",
	"set_bio":                    "destructive+open+idem",
	"set_showcase_site":          "destructive+open+idem",
	"list_sites":                 "read",
	"get_site":                   "read",
	"read_site_file":             "read",
	"create_site":                "write+open",
	"update_site":                "destructive+open",
	"set_keep_versions":          "destructive",
	"list_versions":              "read",
	"rollback_site":              "destructive+open+idem",
	"preview_version":            "read",
	"discard_version":            "destructive+idem",
	"delete_site":                "destructive+open+idem",
	"list_deleted_sites":         "read",
	"restore_site":               "write+open+idem",
	"rename_site":                "destructive+open",
	"set_home_page":              "destructive+open+idem",
	"set_visibility":             "destructive+open+idem",
	"set_site_offline":           "destructive+open+idem",
	"keep_site":                  "destructive+open",
	"site_analytics":             "read",
	"export_site":                "read",
	"create_upload_link":         "destructive+open",
	"connect_domain":             "destructive+open+idem",
	"domain_status":              "read",
	"remove_domain":              "destructive+open",
	"get_site_passcode":          "read",
	"set_site_passcode":          "destructive+open+idem",
	"remove_site_passcode":       "destructive+open+idem",
	"sign_out_site_visitors":     "destructive+open",
	"set_site_access":            "destructive+open+idem",
	"list_site_viewers":          "read",
	"grant_site_viewer":          "destructive+open+idem",
	"revoke_site_viewer":         "destructive+open+idem",
	"storage_list_resources":     "read",
	"storage_get_usage":          "read",
	"storage_set_resource":       "destructive+open+idem",
	"storage_delete_resource":    "destructive+open+idem",
	"storage_list_kv_keys":       "read",
	"storage_get_kv":             "read",
	"storage_put_kv":             "destructive+open+idem",
	"storage_delete_kv":          "destructive+open+idem",
	"storage_sql_query":          "read",
	"storage_sql_execute":        "destructive+open",
	"storage_sql_schema":         "destructive+open",
	"storage_list_file_objects":  "read",
	"storage_put_file":           "destructive+open+idem",
	"storage_delete_file":        "destructive+open+idem",
	"storage_file_download_link": "read",
	"visitor_read_site":          "read",
	"visitor_add_entry":          "write+open",
	"visitor_change_entry":       "destructive+open",
	"storage_visitor_emails":     "read",
	"get_page_recipe":            "read",
	"create_team":                "write",
	"list_teams":                 "read",
	"list_team_members":          "read",
	"add_team_member":            "write",
	"cancel_team_invitation":     "destructive+idem",
	"answer_team_invitation":     "destructive",
	"remove_team_member":         "destructive+idem",
	"delete_team":                "destructive+idem",
	"list_site_editors":          "read",
	"add_site_editor":            "write+idem",
	"remove_site_editor":         "destructive+idem",
	"list_notifications":         "read",
	"remove_notification":        "destructive+idem",
	"find_tools":                 "read",
	"run_tool":                   "destructive+open",
	"passcode_screen":            "destructive+open",
}

// annotationClass is the table entry a tool's hints describe.
func annotationClass(a map[string]any) (string, []string) {
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

func checkAnnotations(t *testing.T, where string, tool Tool, table map[string]string) {
	t.Helper()
	if strings.TrimSpace(tool.Title) == "" {
		t.Errorf("%s %s: no human title", where, tool.Name)
	}
	got, problems := annotationClass(tool.Annotations)
	for _, p := range problems {
		t.Errorf("%s %s: %s", where, tool.Name, p)
	}
	want, ok := table[tool.Name]
	if !ok {
		t.Errorf("%s %s: no row in the annotation table (%s now)", where, tool.Name, got)
		return
	}
	if got != want {
		t.Errorf("%s %s: annotations say %s, the table says %s", where, tool.Name, got, want)
	}
}

// TestToolAnnotationsFollowTheRule walks every Simple Host tool: every
// declaration with teams, events and the passcode screen on, and what each
// kind of connection is offered and may call.
func TestToolAnnotationsFollowTheRule(t *testing.T) {
	seen := map[string]bool{}
	for _, noSpam := range []bool{false, true} {
		cfg := Config{Contribution: &Contribution{}, Teams: true, Events: &fakeEventsBackend{}, NoSpamCheck: noSpam}
		limits := DefaultLimits()
		cfg.Limits = &limits
		for _, entry := range buildRegistry(cfg, nil).entries {
			checkAnnotations(t, "declared", entry.tool, hostAnnotationTable)
			seen[entry.tool.Name] = true
		}
		s := NewServer(cfg)
		for _, caller := range []Caller{
			{}, {DeployOnly: true}, {AppName: "ChatGPT"}, {AppName: "Codex"},
			{Mode: CallerModeEvents, GrantID: "grant"}, {APIKey: "k", Owner: "o", OAuthGrantID: "g", AppName: "ChatGPT"},
		} {
			full, _, byName := s.forCaller(caller)
			for _, tool := range full {
				checkAnnotations(t, "listed", tool, hostAnnotationTable)
			}
			for _, tool := range byName {
				checkAnnotations(t, "callable", tool, hostAnnotationTable)
			}
		}
	}
	for name := range hostAnnotationTable {
		if !seen[name] {
			t.Errorf("table row %s names no tool", name)
		}
	}
}
