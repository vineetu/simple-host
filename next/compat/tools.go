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
	if current[name] && !oldArgs(name, in) { // a current call: the current answer, with the old fields added
		res, _ := run(name, in)
		return oldAnswer(name, args(in), res, run), true
	}
	var res map[string]any
	switch name {
	case "create_upload_link": // only the old shape gets here (OldArgs)
		res = photoPicker(args(in), run)
	case "find_tools":
		res = map[string]any{"content": []any{text(list)}, "structuredContent": findTools(list)}
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
		// The old create_site never overwrote: an existing site is refused,
		// so a repeated call does not publish a second version.
		if name == "create_site" && err == nil {
			if got, ok := run("get_site", args(in).keep("site", "owner")); ok && got["isError"] != true {
				res, steps = refusal(web.Refuse(http.StatusConflict, "site_exists", "This account already has a site named "+args(in).s("site")+
					", and create_site never overwrites. To change it, use update_site; for a separate site, pick another name.")), nil
			}
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
	if name != "run_tool" && name != "find_tools" {
		res = oldAnswer(name, args(in), res, run)
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
		"Photos now go to this site's photos namespace, not images/. When they say done, list them with list_files (namespace photos), "+
		"and show one on a page with <img src=\"/v1/sites/"+url.PathEscape(a.s("site"))+"/storage/files/photos/objects/<path>\">. "+
		"Anyone who can open the site sees them, as with images/; only the owner adds them."))
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
			steps = append(steps, step{"allow_only_named_people", a.keep("site", "owner").with("emails", a.list("viewers"))})
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
	// The old set_site_access {access: anyone|specific} and yesterday's
	// (2026-11-25) {access, viewers, viewers_add, viewers_remove,
	// sign_everyone_out}, split into single-purpose tools.
	"set_site_access": setSiteAccess,
	"change_site":     changeSite,
	"read_site_file": func(a args) ([]step, error) {
		if a["version"] != nil {
			return nil, web.Refuse(http.StatusGone, "removed", "Reading a file of an older version was removed: read the live one (no version), or make the older version live with rollback_site.")
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
	// rollback_site, restore_site and sign_out_site_visitors are current
	// names again, with the old arguments (shapes.go current).
	"set_site_offline": func(a args) ([]step, error) {
		if a["offline"] == true {
			return siteOnly("take_site_offline")(a)
		}
		return siteOnly("put_site_online")(a)
	},
	"set_visibility": func(a args) ([]step, error) {
		return one("set_site_listed", a.keep("site", "owner").with("listed", a.s("visibility") == "public"))
	},
	"grant_site_viewer": func(a args) ([]step, error) {
		return one("add_site_viewers", a.keep("site", "owner").with("emails", a.list("emails")))
	},
	"revoke_site_viewer": func(a args) ([]step, error) {
		return one("remove_site_viewers", a.keep("site", "owner").with("emails", append(a.list("emails"), a.list("email")...)))
	},
	// A passcode never passes through the chat: the passcode screen sets
	// and shows it, so a passcode an old call sends is not used.
	"get_site_passcode": siteOnly("passcode_screen_open"),
	"set_site_passcode": siteOnly("require_passcode"),
	"remove_site_passcode": func(a args) ([]step, error) {
		if a["confirm"] != true {
			return nil, web.Refuse(http.StatusBadRequest, "ask_first", "Tell the person anyone with the link will be able to open the site, then call again with confirm: true once they agree.")
		}
		return siteOnly("open_site_to_anyone")(a)
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
	"export_site":       func(a args) ([]step, error) { return one("download_my_data", args{}) },
	"set_home_page":     func(a args) ([]step, error) { return one("update_account", args{"home_site": a.s("site")}) },
	"create_team":       func(a args) ([]step, error) { return one("create_team", args{"handle": a.s("name")}) },
	"list_teams":        func(a args) ([]step, error) { return one("who_am_i", args{}) },
	"list_team_members": func(a args) ([]step, error) { return one("who_am_i", args{"include_email": true}) },
	"add_team_member":   func(a args) ([]step, error) { return one("invite_to_team", a.keep("team", "email")) },
	"remove_team_member": func(a args) ([]step, error) {
		if p := a.s("person"); strings.Contains(p, "@") { // list_team_members gives emails now
			return one("remove_from_team", args{"team": a.s("team"), "email": p})
		}
		return one("remove_from_team", a.keep("team", "person"))
	},
	"answer_team_invitation": func(a args) ([]step, error) {
		if a.s("answer") != "accept" {
			return nil, removed("Declining an invitation")
		}
		return one("accept_invite", args{"team": a.s("invitation")}) // list_teams gives the team's handle as the id
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
	"storage_put_kv":            func(a args) ([]step, error) { return one("set_value", ns(a, "key").with("value", a["value"])) },
	"storage_get_kv":            func(a args) ([]step, error) { return one("read_value", ns(a, "key")) },
	"storage_delete_kv":         func(a args) ([]step, error) { return one("delete_value", ns(a, "key")) },
	"storage_list_kv_keys":      func(a args) ([]step, error) { return one("list_values", ns(a, "prefix")) },
	"storage_list_file_objects": func(a args) ([]step, error) { return one("list_files", ns(a, "prefix")) },
	"storage_sql_query":         func(a args) ([]step, error) { return sql("query_sql", a) },
	"storage_sql_execute":       func(a args) ([]step, error) { return sql("run_sql", a) },
	"storage_sql_schema": func(a args) ([]step, error) {
		if a.s("sql") == "" {
			return one("list_tables", ns(a))
		}
		steps, err := sql("run_sql", a)
		return append(steps, step{"list_tables", ns(a)}), err
	},
	"storage_put_file": func(a args) ([]step, error) {
		return one("save_file", ns(a, "path", "content_base64", "content_type"))
	},
	"storage_delete_file": func(a args) ([]step, error) { return one("delete_file", ns(a, "path")) },
	// Yesterday's names (2026-11-25), split into single-purpose tools.
	"data":                       data,
	"passcode_screen":            passcodeScreen,
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
	"visitor_add_entry":          gone("Separate visitor tools"),
	"visitor_change_entry":       gone("Separate visitor tools"),
}

// recipeTopics are the old recipe topics that have a current one.
var recipeTopics = map[string]string{"contact": "inbox", "quote": "inbox", "waitlist": "inbox", "form": "inbox",
	"menu": "public", "guestbook": "comments", "booking": "personal", "reservation": "personal", "appointment": "personal"}

// OldArgs reports whether compat answers a call of a current tool name; mcp
// then sends it to Tool. It does when the call carries arguments only the
// old tool of that name took, and for every name the old list also had with
// an outputSchema, so the answer carries the old fields as well (shapes.go).
func OldArgs(name string, in map[string]any) bool {
	return !over() && (current[name] || oldArgs(name, in))
}

// oldArgs reports whether a call carries arguments only the old tool of
// that name took.
func oldArgs(name string, in map[string]any) bool {
	a := args(in)
	switch name {
	case "create_upload_link": // the old {site} (photo picker) or {site, archive: true}
		return a.s("namespace") == "" && a.s("kind") == ""
	case "read_site_file":
		return a["paths"] != nil || a["version"] != nil
	case "list_sites":
		return a["query"] != nil || a["limit"] != nil || a["offset"] != nil || a["order"] != nil
	case "create_team":
		return a["name"] != nil && a["handle"] == nil
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

// setSiteAccess is the old and yesterday's set_site_access: the access
// tool for each part, in the order the route ran them.
func setSiteAccess(a args) ([]step, error) {
	site, steps := func() args { return a.keep("site", "owner") }, []step{}
	switch a.s("access") {
	case "open", "anyone":
		steps = append(steps, step{"open_site_to_anyone", site()})
	case "passcode":
		steps = append(steps, step{"require_passcode", site()})
	case "viewers", "specific":
		s := site()
		if a["viewers"] != nil {
			s["emails"] = a.list("viewers")
		}
		steps = append(steps, step{"allow_only_named_people", s})
	case "":
		if a["viewers"] != nil {
			steps = append(steps, step{"allow_only_named_people", site().with("emails", a.list("viewers"))})
		}
	default:
		return nil, web.Refuse(http.StatusBadRequest, "bad_access", "set_site_access was split: use open_site_to_anyone, require_passcode or allow_only_named_people.")
	}
	if add := a.list("viewers_add"); len(add) > 0 {
		steps = append(steps, step{"add_site_viewers", site().with("emails", add)})
	}
	if remove := a.list("viewers_remove"); len(remove) > 0 {
		steps = append(steps, step{"remove_site_viewers", site().with("emails", remove)})
	}
	if a["sign_everyone_out"] == true {
		steps = append(steps, step{"sign_out_site_visitors", site()})
	}
	if len(steps) == 0 {
		return siteOnly("get_site")(a)
	}
	return steps, nil
}

// changeSite is yesterday's change_site {restore, offline, listed,
// live_version}: the tool for each part, in the order the route ran them.
func changeSite(a args) ([]step, error) {
	site, steps := func() args { return a.keep("site", "owner") }, []step{}
	if a["restore"] == true {
		steps = append(steps, step{"restore_site", site()})
	}
	switch a["offline"] {
	case true:
		steps = append(steps, step{"take_site_offline", site()})
	case false:
		steps = append(steps, step{"put_site_online", site()})
	}
	if l, ok := a["listed"].(bool); ok {
		steps = append(steps, step{"set_site_listed", site().with("listed", l)})
	}
	if a["live_version"] != nil {
		steps = append(steps, step{"rollback_site", site().with("version", a["live_version"])})
	}
	if len(steps) == 0 {
		return siteOnly("get_site")(a)
	}
	return steps, nil
}

func gone(feature string) func(args) ([]step, error) {
	return func(args) ([]step, error) { return nil, removed(feature) }
}

func removed(feature string) error {
	return web.Refuse(http.StatusGone, "removed", feature+" was removed from Simple Host.")
}

// ns is the old storage tools' arguments for a current data tool: the
// resource name is the namespace.
func ns(a args, keep ...string) args {
	return a.keep(append([]string{"site", "owner"}, keep...)...).with("namespace", a.s("name"))
}

func sql(tool string, a args) ([]step, error) {
	s := ns(a, "sql", "params")
	if p, ok := a["params"].(string); ok { // the old tools also took the list as JSON text
		var list []any
		if json.Unmarshal([]byte(p), &list) != nil {
			return nil, web.Refuse(http.StatusBadRequest, "bad_params", "Send params as a list, like [\"a\", 1].")
		}
		s["params"] = list
	}
	return one(tool, s)
}

// data is yesterday's data {method, path, body, body_base64, type}: the
// route under the site it named, as the one current tool for it.
func data(a args) ([]step, error) {
	rest, query, _ := strings.Cut(a.s("path"), "?")
	q, _ := url.ParseQuery(query)
	method := strings.ToUpper(a.s("method"))
	if method == "" {
		method = "GET"
		if a.s("body_base64") != "" {
			method = "PUT"
		}
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	for i := range parts {
		parts[i], _ = url.PathUnescape(parts[i])
	}
	bad := web.Refuse(http.StatusBadRequest, "bad_path", "data was split into single-purpose tools: list_rows, read_row, add_row, change_row, delete_row, "+
		"list_values, read_value, set_value, delete_value, list_files, save_file and delete_file. Use those.")
	if len(parts) < 4 || parts[0] != "storage" {
		return nil, bad
	}
	base := a.keep("site", "owner").with("namespace", parts[2])
	page := func(s args, keys ...string) args {
		for _, k := range keys {
			if v := q.Get(k); v != "" {
				s[k] = v
			}
		}
		return s
	}
	body, _ := a["body"].(map[string]any)
	switch {
	case parts[1] == "sqlite" && len(parts) >= 6 && parts[3] == "tables" && parts[5] == "rows":
		base["table"] = parts[4]
		if len(parts) == 6 {
			switch method {
			case "GET":
				s := page(base, "order", "after", "limit")
				where := map[string]any{}
				for k, v := range q {
					if col, ok := strings.CutPrefix(k, "where."); ok {
						where[col] = v[0]
					}
				}
				if len(where) > 0 {
					s["where"] = where
				}
				if d := q.Get("desc"); d == "1" || d == "true" {
					s["desc"] = true
				}
				return one("list_rows", s)
			case "POST":
				return one("add_row", base.with("row", body))
			}
		} else if len(parts) == 7 {
			base["id"] = parts[6]
			switch method {
			case "GET":
				return one("read_row", base)
			case "PATCH":
				return one("change_row", base.with("row", body))
			case "DELETE":
				return one("delete_row", base)
			}
		}
	case parts[1] == "kv" && parts[3] == "keys":
		if len(parts) == 4 && method == "GET" {
			return one("list_values", page(base, "prefix", "after", "limit"))
		}
		if len(parts) == 5 {
			base["key"] = parts[4]
			switch method {
			case "GET":
				return one("read_value", base)
			case "PUT":
				return one("set_value", base.with("value", body["value"]))
			case "DELETE":
				return one("delete_value", base)
			}
		}
	case parts[1] == "files" && parts[3] == "objects":
		if len(parts) == 4 && method == "GET" {
			return one("list_files", page(base, "prefix", "after", "limit"))
		}
		if len(parts) > 4 {
			base["path"] = strings.Join(parts[4:], "/")
			switch method {
			case "PUT":
				return one("save_file", base.with("content_base64", a.s("body_base64")).with("content_type", a.s("type")))
			case "DELETE":
				return one("delete_file", base)
			case "GET":
				return nil, web.Refuse(http.StatusBadRequest, "open_at_site", "Open a stored file at the site's address: /v1/sites/<site>/"+strings.Join(parts, "/")+".")
			}
		}
	}
	return nil, bad
}

// passcodeScreen is yesterday's passcode_screen {action, ...}: the screen
// tool for that step.
func passcodeScreen(a args) ([]step, error) {
	tool := map[string]string{"begin": "passcode_screen_open", "show": "passcode_screen_show", "set": "passcode_screen_set", "generate": "passcode_screen_generate"}[a.s("action")]
	if tool == "" {
		return nil, web.Refuse(http.StatusBadRequest, "bad_action", `Set "action" to "begin", "show", "set" or "generate".`)
	}
	s := args{}
	for k, v := range a {
		if k != "action" {
			s[k] = v
		}
	}
	return one(tool, s)
}
