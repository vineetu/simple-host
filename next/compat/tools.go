package compat

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"simplehost/app"
	"simplehost/core/web"
)

// Old tool names. AI apps that connected before the switch keep their old
// tool list and do not list again, so each old name still works: it is
// reshaped into calls of the current tools. With SH_COMPAT_NOTICE=on the
// answer also says to reconnect (notice.go).

// Tool answers a tool name the current list does not have: an old name,
// find_tools or run_tool. list is the current tools, one "name: about" a
// line; run calls a current tool (false: no such tool). ok is false for a
// name nobody ever had, and for every name after the sunset.
func Tool(a *app.App, r *http.Request, name string, in map[string]any, list string, run func(tool string, args map[string]any) (map[string]any, bool)) (map[string]any, bool) {
	if over() {
		return nil, false
	}
	var res map[string]any
	switch name {
	case "create_upload_link": // only the old shape gets here (OldArgs)
		res = photoPicker(args(in), run)
	case "find_tools":
		res = map[string]any{"content": []any{text(list)}}
	case "run_tool":
		inner, _ := in["arguments"].(map[string]any)
		if inner == nil {
			inner = map[string]any{}
		}
		innerName := args(in).s("name")
		if OldArgs(innerName, inner) {
			return Tool(a, r, innerName, inner, list, run)
		}
		var ok bool
		if res, ok = run(innerName, inner); !ok {
			if res, ok = Tool(a, r, innerName, inner, list, run); ok {
				return res, true // counted and noted as the old name
			}
			res = refusal(web.Refuse(http.StatusNotFound, "no_tool", "No tool named "+innerName+". Reconnect Simple Host to get the current tools."))
		}
	default:
		reshape, ok := oldTools[name]
		if !ok {
			return nil, false
		}
		steps, err := reshape(args(in))
		if err != nil {
			res = refusal(err)
		}
		var content []any // every step's answer, in order
		for _, s := range steps {
			var ok bool
			if res, ok = run(s.tool, s.args); !ok {
				res = refusal(web.Refuse(http.StatusInternalServerError, "server_error", "Something broke on our side. Try again in a minute."))
			}
			more, _ := res["content"].([]any)
			content = append(content, more...)
			if res["isError"] == true {
				break
			}
		}
		if len(steps) > 1 {
			res["content"] = content
		}
	}
	count(a, r, "tool "+name)
	if toolNoticeDue(r) {
		content, _ := res["content"].([]any)
		res["content"] = append(content, text(toolNotice()))
	}
	return res, true
}

// photoPicker answers the old create_upload_link {site}, a photo picker
// that saved into the site's images/. The current tool gives the picker for
// the namespace photos; this adds the old answer's photos_url and says where
// photos now land. Goes with this package.
func photoPicker(a args, run func(string, map[string]any) (map[string]any, bool)) map[string]any {
	if a["archive"] == true {
		res, _ := run("create_upload_link", a.keep("site", "owner").with("kind", "archive"))
		return res
	}
	res, _ := run("create_upload_link", a.keep("site", "owner"))
	link, _ := res["structuredContent"].(map[string]any)
	if res["isError"] == true || link == nil {
		return res
	}
	link["photos_url"] = link["url"] // the old answer's name for it
	content, _ := res["content"].([]any)
	res["content"] = append(content, text("The photo picker is in the chat; url is the same picker as a page. "+
		"Photos now go to this site's photos namespace, not images/. When they say done, list them with data GET storage/files/photos/objects, "+
		"and show one on a page with <img src=\"/v1/sites/"+url.PathEscape(a.s("site"))+"/storage/files/photos/objects/<path>\">. "+
		"Only the owner can see them until set_data_rules lets visitors read _files (ask the person first)."))
	return res
}

func text(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// refusal is a refusal as a tool result, as the current tools give it.
func refusal(err error) map[string]any {
	ref, ok := err.(*web.Refusal)
	if !ok {
		ref = web.Refuse(http.StatusInternalServerError, "server_error", "Something broke on our side. Try again in a minute.")
	}
	return map[string]any{"isError": true, "content": []any{text(ref.Code + ": " + ref.Fix)},
		"structuredContent": map[string]any{"code": ref.Code, "fix": ref.Fix}}
}

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
		if a["publish"] == false {
			return nil, web.Refuse(http.StatusGone, "removed", "Drafts and preview links were removed: publishing puts the files live. Nothing was published; call again without publish: false when the person wants it live.")
		}
		if a["remove_files"] != nil || a["edits"] != nil {
			return nil, web.Refuse(http.StatusBadRequest, "send_whole_files", "remove_files and edits are gone: send each changed file whole with keep_other_files: true, or send every file to drop the others.")
		}
		return one("publish_site", a.keep("site", "owner", "files", "files_base64", "keep_other_files"))
	},
	"site_analytics": func(a args) ([]step, error) { return one("site_stats", a.keep("site", "owner", "days")) },
	"get_page_recipe": func(a args) ([]step, error) {
		s := a.keep("topic", "site")
		if t, ok := recipeTopics[a.s("topic")]; ok {
			s["topic"] = t
		}
		return one("get_recipe", s)
	},
	// Old arguments of tools whose names stayed (see OldArgs).
	"set_site_access": func(a args) ([]step, error) {
		return one("set_site_access", a.keep("site", "owner").with("access", map[string]string{"anyone": "open", "specific": "viewers"}[a.s("access")]))
	},
	"read_site_file": func(a args) ([]step, error) {
		if a["version"] != nil {
			return nil, web.Refuse(http.StatusGone, "removed", "Reading a file of an older version was removed: read the live one (no version), or make the older version live with change_site live_version.")
		}
		steps := []step{}
		for _, p := range append(a.list("path"), a.list("paths")...) {
			steps = append(steps, step{"read_site_file", a.keep("site", "owner").with("path", p)})
		}
		return steps, nil
	},
	"list_sites":             func(a args) ([]step, error) { return one("list_sites", args{"q": a.s("query")}) },
	"list_versions":          siteOnly("get_site"),
	"list_site_viewers":      siteOnly("get_site"),
	"domain_status":          siteOnly("get_site"),
	"storage_list_resources": siteOnly("get_site"),
	"storage_get_usage":      siteOnly("get_site"),
	"list_deleted_sites":     func(a args) ([]step, error) { return one("list_sites", args{"bin": true}) },
	"rollback_site":          changeStep("live_version", func(a args) any { return a["version"] }),
	"restore_site":           changeStep("restore", func(args) any { return true }),
	"set_site_offline":       changeStep("offline", func(a args) any { return a["offline"] }),
	"set_visibility":         changeStep("listed", func(a args) any { return a.s("visibility") == "public" }),
	"grant_site_viewer":      accessStep("viewers_add", func(a args) any { return a.list("emails") }),
	"revoke_site_viewer":     accessStep("viewers_remove", func(a args) any { return append(a.list("emails"), a.list("email")...) }),
	"sign_out_site_visitors": accessStep("sign_everyone_out", func(args) any { return true }),
	// The passcode screen keeps the code out of the chat: without a code
	// given, these open it.
	"get_site_passcode": accessStep("access", func(args) any { return "passcode" }),
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
		rules, spam, err := namespaceRules(body)
		if err != nil {
			return nil, err
		}
		s := a.keep("site", "owner").with("namespace", a.s("name")).with("rules", rules)
		if spam {
			s["spam_check"] = true
		}
		return one("set_data_rules", s)
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

// recipeTopics are the old recipe topics that have a current one.
var recipeTopics = map[string]string{"contact": "inbox", "quote": "inbox", "waitlist": "inbox", "form": "inbox",
	"menu": "public", "guestbook": "comments", "booking": "personal", "reservation": "personal", "appointment": "personal"}

// OldArgs reports whether a call of a current tool name carries arguments
// only the old tool of that name took; mcp then sends it to Tool.
func OldArgs(name string, in map[string]any) bool {
	if over() {
		return false
	}
	a := args(in)
	switch name {
	case "create_upload_link": // the old {site} (photo picker) or {site, archive: true}
		return a.s("namespace") == "" && a.s("kind") == ""
	case "set_site_access":
		return a.s("access") == "anyone" || a.s("access") == "specific"
	case "read_site_file":
		return a["paths"] != nil || a["version"] != nil
	case "list_sites":
		return a["query"] != nil
	}
	return false
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

func changeStep(field string, value func(args) any) func(args) ([]step, error) {
	return func(a args) ([]step, error) { return one("change_site", a.keep("site", "owner").with(field, value(a))) }
}

func accessStep(field string, value func(args) any) func(args) ([]step, error) {
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
	s := a.keep("site", "owner", "sql", "params").with("namespace", a.s("name"))
	if p, ok := a["params"].(string); ok { // the old tools also took the list as JSON text
		var list []any
		if json.Unmarshal([]byte(p), &list) != nil {
			return nil, web.Refuse(http.StatusBadRequest, "bad_params", "Send params as a list, like [\"a\", 1].")
		}
		s["params"] = list
	}
	return one("run_sql", s)
}

func filePath(a args) string {
	parts := strings.Split(strings.TrimPrefix(a.s("path"), "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return "storage/files/" + url.PathEscape(a.s("name")) + "/objects/" + strings.Join(parts, "/")
}
