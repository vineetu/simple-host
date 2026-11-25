package mcp

import (
	"net/http"
	"net/url"
	"strings"

	"simplehost/core/web"
)

// Old tool names. AI apps that connected before the switch keep their old
// tool list and do not list again, so each old name still works: it is
// reshaped into calls of the current tools (never listed in tools/list),
// and the answer says to reconnect for the current list.

const oldListNote = "This connection has an old tool list. Reconnect Simple Host or start a new chat to get the current tools."

// step is one call of a current tool.
type step struct {
	tool string
	args map[string]any
}

// oldTools maps an old name to the current calls it stands for, in order;
// the answer is the last one's. An error is the answer instead.
var oldTools = map[string]func(a args) ([]step, error){
	"create_site": func(a args) ([]step, error) {
		steps := []step{{"publish_site", a.keep("site", "owner", "files", "files_base64")}}
		if a.s("access") == "specific" {
			steps = append(steps, step{"set_site_access", a.keep("site", "owner").with("viewers", a.list("viewers"))})
		}
		return steps, nil
	},
	"update_site": func(a args) ([]step, error) {
		if a["remove_files"] != nil || a["edits"] != nil {
			return nil, web.Refuse(http.StatusBadRequest, "send_whole_files", "remove_files and edits are gone: send each changed file whole with keep_other_files: true, or send every file to drop the others.")
		}
		return one("publish_site", a.keep("site", "owner", "files", "files_base64", "keep_other_files"))
	},
	"site_analytics":         func(a args) ([]step, error) { return one("site_stats", a.keep("site", "owner", "days")) },
	"get_page_recipe":        func(a args) ([]step, error) { return one("get_recipe", a.keep("topic", "site")) },
	"list_versions":          siteOnly("get_site"),
	"list_site_viewers":      siteOnly("get_site"),
	"domain_status":          siteOnly("get_site"),
	"storage_list_resources": siteOnly("get_site"),
	"storage_get_usage":      siteOnly("get_site"),
	"list_deleted_sites":     func(a args) ([]step, error) { return one("list_sites", args{"bin": true}) },
	"rollback_site":          change("live_version", func(a args) any { return a["version"] }),
	"restore_site":           change("restore", func(args) any { return true }),
	"set_site_offline":       change("offline", func(a args) any { return a["offline"] }),
	"set_visibility":         change("listed", func(a args) any { return a.s("visibility") == "public" }),
	"grant_site_viewer":      access("viewers_add", func(a args) any { return a.list("emails") }),
	"revoke_site_viewer":     access("viewers_remove", func(a args) any { return append(a.list("emails"), a.list("email")...) }),
	"sign_out_site_visitors": access("sign_everyone_out", func(args) any { return true }),
	// The passcode screen keeps the code out of the chat: without a code
	// given, these open it.
	"get_site_passcode": access("access", func(args) any { return "passcode" }),
	"set_site_passcode": func(a args) ([]step, error) {
		return one("set_site_access", a.keep("site", "owner", "passcode").with("access", "passcode"))
	},
	"remove_site_passcode": func(a args) ([]step, error) {
		if a["confirm"] != true {
			return nil, web.Refuse(http.StatusBadRequest, "ask_first", "Tell the person anyone with the link will be able to open the site, then call again with confirm: true once they agree.")
		}
		return one("set_site_access", a.keep("site", "owner").with("access", "open"))
	},
	"connect_domain": func(a args) ([]step, error) {
		d := strings.ToLower(a.s("domain"))
		if strings.HasPrefix(d, "*.") {
			return one("update_account", args{"domain": strings.TrimPrefix(d, "*.")})
		}
		if name, ok := strings.CutSuffix(d, ".simple-host.app"); ok && !strings.Contains(name, ".") {
			return one("set_site_address", a.keep("site", "owner").with("short_name", name))
		}
		return one("set_site_address", a.keep("site", "owner").with("domain", d))
	},
	"remove_domain": func(a args) ([]step, error) {
		if strings.HasSuffix(strings.ToLower(a.s("confirm_domain")), ".simple-host.app") {
			return one("set_site_address", a.keep("site", "owner").with("short_name", ""))
		}
		return one("set_site_address", a.keep("site", "owner").with("domain", ""))
	},
	"export_site":        func(a args) ([]step, error) { return one("download_my_data", args{}) },
	"set_home_page":      func(a args) ([]step, error) { return one("update_account", args{"home_site": a.s("site")}) },
	"list_teams":         func(a args) ([]step, error) { return one("who_am_i", args{}) },
	"list_team_members":  func(a args) ([]step, error) { return one("who_am_i", args{}) },
	"add_team_member":    func(a args) ([]step, error) { return one("invite_to_team", a.keep("team", "email")) },
	"remove_team_member": func(a args) ([]step, error) { return one("remove_from_team", a.keep("team", "person")) },
	"answer_team_invitation": func(a args) ([]step, error) {
		if a.s("answer") != "accept" {
			return nil, removed("Declining an invitation")
		}
		return one("accept_invite", args{"id": a.s("invitation")})
	},
	"storage_set_resource": func(a args) ([]step, error) {
		body, _ := a["body"].(map[string]any)
		b := args(body)
		rules := map[string]any{}
		switch b.s("kind") {
		case "kv":
			rules["_values"] = b["rules"]
		case "files":
			rules["_files"] = b["rules"]
		default:
			tables, _ := b["tables"].(map[string]any)
			for t, v := range tables {
				if m, ok := v.(map[string]any); ok {
					rules[t] = m["rules"]
				}
			}
			if len(rules) == 0 && b["rules"] != nil {
				return nil, web.Refuse(http.StatusBadRequest, "rules_per_table", `SQLite rules are now set per table: send body.tables, like {"orders": {"rules": {...}}}.`)
			}
		}
		return one("set_data_rules", a.keep("site", "owner").with("namespace", a.s("name")).with("rules", rules))
	},
	"storage_put_kv":            kv("PUT", func(a args) any { return map[string]any{"value": a["value"]} }),
	"storage_get_kv":            kv("GET", nil),
	"storage_delete_kv":         kv("DELETE", nil),
	"storage_list_kv_keys":      store("GET", "kv", "/keys"),
	"storage_list_file_objects": store("GET", "files", "/objects"),
	"storage_sql_query":         sql,
	"storage_sql_execute":       sql,
	"storage_sql_schema":        sql,
	"storage_put_file": func(a args) ([]step, error) {
		return one("data", a.keep("site", "owner").with("method", "PUT").with("path", filePath(a)).
			with("body_base64", a.s("content_base64")).with("type", a.s("content_type")))
	},
	"storage_delete_file": func(a args) ([]step, error) {
		return one("data", a.keep("site", "owner").with("method", "DELETE").with("path", filePath(a)))
	},
	"preview_version":            gone("Drafts and previews"),
	"discard_version":            gone("Drafts and previews"),
	"keep_site":                  gone("Keeping an idle site"),
	"set_keep_versions":          gone("Choosing how many versions to keep"),
	"add_site_editor":            gone("Site editors"),
	"list_site_editors":          gone("Site editors"),
	"remove_site_editor":         gone("Site editors"),
	"rename_site":                gone("Renaming a site"),
	"visitor_read_site":          gone("Separate visitor tools"),
	"storage_visitor_emails":     gone("Separate visitor tools"),
	"list_notifications":         gone("The notification list"),
	"remove_notification":        gone("The notification list"),
	"cancel_team_invitation":     gone("Cancelling an invitation"),
	"set_bio":                    gone("The profile bio"),
	"set_showcase_site":          gone("The showcase site"),
	"duplicate":                  gone("Duplicating a site"),
	"storage_file_download_link": gone("Download links for stored files"),
	"storage_delete_resource":    gone("Deleting a whole storage resource"),
	"passcode_screen":            gone("The old passcode screen"),
}

// callOld answers a tool name that is not in the table: an old name,
// find_tools or run_tool. ok is false for a name nobody ever had.
func (h *server) callOld(r *http.Request, name string, a args) (map[string]any, bool) {
	var res map[string]any
	switch name {
	case "find_tools":
		lines := []string{}
		for _, t := range tools {
			if !t.appOnly {
				lines = append(lines, t.name+": "+t.about)
			}
		}
		res = map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Join(lines, "\n")}}}
	case "run_tool":
		inner, _ := a["arguments"].(map[string]any)
		if inner == nil {
			inner = map[string]any{}
		}
		if t, ok := findTool(a.s("name")); ok {
			res = h.call(r, t, inner)
		} else if res, ok = h.callOld(r, a.s("name"), inner); !ok {
			res = refusal(web.Refuse(http.StatusNotFound, "no_tool", "No tool named "+a.s("name")+". Reconnect Simple Host to get the current tools."))
		}
	default:
		reshape, ok := oldTools[name]
		if !ok {
			return nil, false
		}
		steps, err := reshape(a)
		if err != nil {
			res = refusal(err)
		}
		for _, s := range steps {
			t, _ := findTool(s.tool)
			if res = h.call(r, t, s.args); res["isError"] == true {
				break
			}
		}
	}
	content, _ := res["content"].([]any)
	res["content"] = append(content, map[string]any{"type": "text", "text": oldListNote})
	return res, true
}

func findTool(name string) (tool, bool) {
	for _, t := range tools {
		if t.name == name {
			return t, true
		}
	}
	return tool{}, false
}

// args are a call's arguments.
type args map[string]any

func (a args) s(k string) string { v, _ := a[k].(string); return strings.TrimSpace(v) }

// keep is the arguments named, those present.
func (a args) keep(keys ...string) args {
	out := args{}
	for _, k := range keys {
		if v, ok := a[k]; ok {
			out[k] = v
		}
	}
	return out
}

func (a args) with(k string, v any) args { a[k] = v; return a }

// list is a list argument, or one string, as strings.
func (a args) list(k string) []any {
	switch v := a[k].(type) {
	case []any:
		return v
	case string:
		return []any{v}
	}
	return []any{}
}

func one(tool string, a args) ([]step, error) { return []step{{tool, a}}, nil }

func siteOnly(tool string) func(args) ([]step, error) {
	return func(a args) ([]step, error) { return one(tool, a.keep("site", "owner")) }
}

func change(field string, value func(args) any) func(args) ([]step, error) {
	return func(a args) ([]step, error) { return one("change_site", a.keep("site", "owner").with(field, value(a))) }
}

func access(field string, value func(args) any) func(args) ([]step, error) {
	return func(a args) ([]step, error) {
		return one("set_site_access", a.keep("site", "owner").with(field, value(a)))
	}
}

func gone(feature string) func(args) ([]step, error) {
	return func(args) ([]step, error) { return nil, removed(feature) }
}

func removed(feature string) error {
	return web.Refuse(http.StatusGone, "removed", feature+" was removed from Simple Host.")
}

// store is a data call on resource name's kind of store, with prefix kept.
func store(method, kind, rest string) func(args) ([]step, error) {
	return func(a args) ([]step, error) {
		p := "storage/" + kind + "/" + url.PathEscape(a.s("name")) + rest
		if prefix := a.s("prefix"); prefix != "" {
			p += "?" + url.Values{"prefix": {prefix}}.Encode()
		}
		return one("data", a.keep("site", "owner").with("method", method).with("path", p))
	}
}

func kv(method string, body func(args) any) func(args) ([]step, error) {
	return func(a args) ([]step, error) {
		s := a.keep("site", "owner").with("method", method).
			with("path", "storage/kv/"+url.PathEscape(a.s("name"))+"/keys/"+url.PathEscape(a.s("key")))
		if body != nil {
			s["body"] = body(a)
		}
		return one("data", s)
	}
}

func sql(a args) ([]step, error) {
	return one("run_sql", a.keep("site", "owner", "sql", "params").with("namespace", a.s("name")))
}

func filePath(a args) string {
	parts := strings.Split(strings.TrimPrefix(a.s("path"), "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "storage/files/" + url.PathEscape(a.s("name")) + "/objects/" + strings.Join(parts, "/")
}
