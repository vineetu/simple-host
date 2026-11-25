package compat

import (
	"encoding/json"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"simplehost/core/web"
	"simplehost/sites/manage"
)

// Old answers. An AI app that holds the old tool list also holds each old
// tool's outputSchema and validates structuredContent against it, so an old
// name's answer carries the old fields (testdata/old-tools.json is the old
// list; TestOldToolAnswersMatchTheOldOutputSchemas checks every name). The
// text the model reads stays the current answer. A refusal is an error
// with no structuredContent, which apps do not validate.
//
// Names both lists have (current) keep the current fields too, with the old
// ones added: the current list has no output schemas, its in-chat screens
// read these answers, and the old apps do not reject extra fields. Old-only
// names get exactly the old fields.

// current are the current tool names the old list also had, with an old
// outputSchema: compat answers them too (OldArgs).
var current = map[string]bool{"who_am_i": true, "list_sites": true, "get_site": true, "read_site_file": true,
	"create_upload_link": true, "delete_site": true, "set_site_access": true, "create_team": true, "delete_team": true}

type runner func(tool string, args map[string]any) (map[string]any, bool)

// oldShapes build an old tool's structuredContent from the current answer
// (out, its structuredContent; res, the whole answer).
var oldShapes = map[string]func(a args, out, res map[string]any, run runner) (map[string]any, error){
	"list_sites":         listSites,
	"list_deleted_sites": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		sites := []any{}
		for _, s := range list(out["sites"]) {
			s, _ := s.(map[string]any)
			o := map[string]any{"name": str(s["name"]), "deleted_at": str(s["deleted_at"]), "purge_at": ""}
			if at, err := time.Parse(time.RFC3339Nano, str(s["deleted_at"])); err == nil {
				o["purge_at"] = at.Add(manage.BinDays * 24 * time.Hour).UTC().Format(time.RFC3339)
			}
			sites = append(sites, o)
		}
		return map[string]any{"sites": sites, "count": len(sites)}, nil
	},
	"get_site":           func(a args, out, _ map[string]any, _ runner) (map[string]any, error) { return toolSite(out), nil },
	"rollback_site":      func(a args, out, _ map[string]any, _ runner) (map[string]any, error) { return toolSite(out), nil },
	"restore_site":       func(a args, out, _ map[string]any, _ runner) (map[string]any, error) { return toolSite(out), nil },
	"create_site":        published,
	"update_site":        published,
	"read_site_file":     readFile,
	"list_versions": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		live := num(out["live_version"])
		versions := []any{}
		for _, v := range list(out["versions"]) {
			v, _ := v.(map[string]any)
			versions = append(versions, map[string]any{"version": num(v["n"]), "live": num(v["n"]) == live, "published_at": str(v["at"])})
		}
		return map[string]any{"site": str(out["name"]), "versions": versions}, nil
	},
	"delete_site": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		days := 7
		if until, err := time.Parse(time.RFC3339Nano, str(out["restore_until"])); err == nil {
			days = int(math.Ceil(time.Until(until).Hours() / 24))
		}
		return map[string]any{"deleted": str(out["deleted"]), "restorable_days": days}, nil
	},
	"set_visibility": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		v := "unlisted"
		if out["listed"] == true {
			v = "public"
		}
		return map[string]any{"site": str(out["name"]), "visibility": v}, nil
	},
	"set_site_offline": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"site": str(out["name"]), "offline": out["offline"] == true, "url": str(out["url"])}, nil
	},
	"set_home_page": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"site": out["home_site"]}, nil
	},
	"site_analytics": analytics,
	"export_site": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		// The archive now comes by email (the text says so): no link here.
		return map[string]any{"site": a.s("site"), "url": "", "expires_at": ""}, nil
	},
	"get_page_recipe": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"topic": a.s("topic"), "recipe": str(out["text"])}, nil
	},
	"set_site_access":        access,
	"grant_site_viewer":      access,
	"revoke_site_viewer":     access,
	"list_site_viewers":      access,
	"get_site_passcode":      passcode,
	"set_site_passcode":      passcode,
	"remove_site_passcode":   passcode,
	"sign_out_site_visitors": passcode,
	"connect_domain":         domain,
	"domain_status":          domain,
	"remove_domain": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		o := map[string]any{"removed": a.s("confirm_domain")}
		if out["name"] != nil {
			o["site"], o["url"] = str(out["name"]), str(out["url"])
		}
		return o, nil
	},
	"list_teams": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		teams := []any{}
		for _, t := range list(out["teams"]) {
			teams = append(teams, oldTeam(t))
		}
		invitations := []any{}
		for _, i := range list(out["invitations"]) {
			i, _ := i.(map[string]any)
			invitations = append(invitations, map[string]any{"id": str(i["id"]), "team": str(i["team"])})
		}
		return map[string]any{"teams": teams, "invitations": invitations}, nil
	},
	"list_team_members": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		for _, t := range list(out["teams"]) {
			if m, _ := t.(map[string]any); str(m["handle"]) == a.s("team") {
				members := []any{}
				for _, p := range list(m["members"]) {
					p, _ := p.(map[string]any)
					members = append(members, map[string]any{"id": str(p["id"])})
				}
				return map[string]any{"team": oldTeam(t), "members": members}, nil
			}
		}
		return nil, web.Refuse(http.StatusNotFound, "not_found", "You are not in a team with the handle "+a.s("team")+". who_am_i lists your teams.")
	},
	"create_team": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"id": str(out["id"]), "handle": str(out["handle"]), "members": 1, "created_at": ""}, nil
	},
	"add_team_member": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"invited": true}, nil
	},
	"remove_team_member": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"removed": a.s("person")}, nil
	},
	"delete_team": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"deleted": a.s("team")}, nil
	},
	"answer_team_invitation": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"team": str(out["team_id"]), "answer": "accept"}, nil
	},
	"storage_set_resource":      storage,
	"storage_put_kv":            storage,
	"storage_get_kv":            storage,
	"storage_delete_kv":         storage,
	"storage_list_kv_keys":      storage,
	"storage_list_file_objects": storage,
	"storage_put_file":          storage,
	"storage_delete_file":       storage,
	"storage_sql_query":         storage,
	"storage_sql_execute":       storage,
	"storage_sql_schema":        storage,
	"storage_list_resources": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"status": 200, "response": map[string]any{"resources": list(out["namespaces"])}}, nil
	},
	"storage_get_usage": func(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
		return map[string]any{"status": 200, "response": out["use"]}, nil
	},
}

// oldAnswer is res, the answer to old name, with the old structuredContent.
// res itself is left as it is (it is also logged).
func oldAnswer(name string, a args, res map[string]any, run runner) map[string]any {
	out := map[string]any{}
	for k, v := range res {
		out[k] = v
	}
	if res["isError"] == true {
		delete(out, "structuredContent")
		return out
	}
	shape, ok := oldShapes[name]
	if !ok {
		return out
	}
	got, _ := res["structuredContent"].(map[string]any)
	if got == nil {
		got = map[string]any{}
	}
	old, err := shape(a, got, res, run)
	if err != nil {
		return refusalOnly(err)
	}
	if current[name] && name != "read_site_file" { // a read file's own JSON is not the answer's
		old = overlay(got, old).(map[string]any)
	}
	out["structuredContent"] = old
	return out
}

// refusalOnly is a refusal without structuredContent.
func refusalOnly(err error) map[string]any {
	r := refusal(err)
	delete(r, "structuredContent")
	return r
}

// overlay is the current value with the old one's fields set over it,
// object by object and, for lists of the same length, item by item.
func overlay(cur, old any) any {
	switch o := old.(type) {
	case map[string]any:
		c, ok := cur.(map[string]any)
		if !ok {
			return old
		}
		m := map[string]any{}
		for k, v := range c {
			m[k] = v
		}
		for k, v := range o {
			m[k] = overlay(c[k], v)
		}
		return m
	case []any:
		c, ok := cur.([]any)
		if !ok || len(c) != len(o) {
			return old
		}
		l := make([]any, len(o))
		for i := range o {
			l[i] = overlay(c[i], o[i])
		}
		return l
	}
	return old
}

// toolSite is a site as the old tools gave it.
func toolSite(s map[string]any) map[string]any {
	o := map[string]any{"name": str(first(s["name"], s["site"])), "url": str(s["url"]),
		"active_version": num(first(s["live_version"], s["version"])), "listed": s["listed"] == true}
	if d, _ := s["domain"].(map[string]any); d != nil && d["kind"] == "own" {
		o["custom_domain"], o["domain_status"] = str(d["host"]), map[string]string{"live": "active"}[str(d["status"])]
		if o["domain_status"] == "" {
			o["domain_status"] = "pending"
		}
	}
	return o
}

// listSites honours the old list's order, offset and limit; query became
// the current q.
func listSites(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
	sites := []map[string]any{}
	for _, s := range list(out["sites"]) {
		s, _ := s.(map[string]any)
		o := toolSite(s)
		if s["offline"] == true {
			o["offline"] = true
		}
		if owner := str(s["owner"]); owner != "" {
			o["owner"] = owner
		}
		o["changed_at"] = str(s["changed_at"]) // for sorting; dropped below
		sites = append(sites, o)
	}
	switch a.s("order") {
	case "name":
		sort.SliceStable(sites, func(i, j int) bool { return sites[i]["name"].(string) < sites[j]["name"].(string) })
	case "updated", "":
		sort.SliceStable(sites, func(i, j int) bool { return sites[i]["changed_at"].(string) > sites[j]["changed_at"].(string) })
	}
	if n := int(num(a["offset"])); n > 0 {
		sites = sites[min(n, len(sites)):]
	}
	if n := int(num(a["limit"])); n > 0 && n < len(sites) {
		sites = sites[:n]
	}
	l := []any{}
	for _, s := range sites {
		delete(s, "changed_at")
		l = append(l, s)
	}
	return map[string]any{"sites": l, "count": len(l)}, nil
}

// published is create_site and update_site: the site after the publish.
func published(a args, out, _ map[string]any, run runner) (map[string]any, error) {
	o := toolSite(out)
	if _, ok := out["listed"]; !ok {
		if got, ok := run("get_site", a.keep("site", "owner")); ok && got["isError"] != true {
			s, _ := got["structuredContent"].(map[string]any)
			o["listed"] = s["listed"] == true
		}
	}
	files, _ := a["files"].(map[string]any)
	binary, _ := a["files_base64"].(map[string]any)
	o["file_count"] = len(files) + len(binary)
	return o, nil
}

// binaryFile is the current read_site_file's answer for a file that is not text.
const binaryFile = "A binary file; open it at the site's address."

func readFile(a args, out, res map[string]any, run runner) (map[string]any, error) {
	version := 0.0
	if got, ok := run("get_site", a.keep("site", "owner")); ok && got["isError"] != true {
		s, _ := got["structuredContent"].(map[string]any)
		version = num(s["live_version"])
	}
	read := func(path string, text string) map[string]any {
		f := map[string]any{"path": path, "size": len(text), "content": text}
		var bin map[string]any
		if json.Unmarshal([]byte(text), &bin) == nil && bin["says"] == binaryFile {
			f = map[string]any{"path": path, "size": num(bin["bytes"]), "binary": true}
		}
		return f
	}
	texts := []string{}
	for _, c := range list(res["content"]) {
		if c, _ := c.(map[string]any); c["type"] == "text" {
			texts = append(texts, str(c["text"]))
		}
	}
	paths := append(a.list("path"), a.list("paths")...)
	o := map[string]any{"site": a.s("site"), "version": version}
	if a["paths"] == nil && len(paths) == 1 && len(texts) > 0 {
		for k, v := range read(str(paths[0]), texts[0]) {
			o[k] = v
		}
		return o, nil
	}
	files := []any{}
	for i, p := range paths {
		if i < len(texts) {
			files = append(files, read(str(p), texts[i]))
		}
	}
	o["files"] = files
	return o, nil
}

func analytics(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
	split := func(views, people, bots float64) map[string]any {
		pair := func(v, p float64) map[string]any { return map[string]any{"views": v, "visitors": p} }
		return map[string]any{"person": pair(views, people), "bot": pair(bots, 0), "infra": pair(0, 0), "unknown": pair(0, 0)}
	}
	last := split(0, 0, 0)
	if days := list(out["days"]); len(days) > 0 {
		d, _ := days[len(days)-1].(map[string]any)
		last = split(num(d["views"]), num(d["people"]), num(d["bots"]))
	}
	top := func(key string, items any) []any {
		l := []any{}
		for _, c := range list(items) {
			c, _ := c.(map[string]any)
			l = append(l, map[string]any{key: str(c["key"]), "views": num(c["n"])})
		}
		return l
	}
	days := num(a["days"])
	if days == 0 {
		days = 30
	}
	return map[string]any{"site": a.s("site"), "range_days": days, "totals": split(num(out["views"]), num(out["people"]), num(out["bots"])),
		"last_24h": last, "top_pages": top("path", out["pages"]), "top_referrers": top("domain", out["referrers"])}, nil
}

// site is the site's current state: out when it is the whole site (most
// answers that change a site are), else a get_site.
func site(a args, out map[string]any, run runner) map[string]any {
	if _, ok := out["access"]; ok {
		return out
	}
	if got, ok := run("get_site", a.keep("site", "owner")); ok && got["isError"] != true {
		s, _ := got["structuredContent"].(map[string]any)
		return s
	}
	return map[string]any{}
}

func access(a args, out, _ map[string]any, run runner) (map[string]any, error) {
	s := site(a, out, run)
	word := "anyone"
	if s["access"] == "viewers" {
		word = "specific"
	}
	viewers := []any{}
	for _, v := range list(s["viewers"]) {
		v, _ := v.(map[string]any)
		viewers = append(viewers, map[string]any{"email": str(v["email"]), "added_at": ""})
	}
	return map[string]any{"site": first(str(s["name"]), a.s("site")), "access": word, "viewers": viewers,
		"viewer_limit": 0, "passcode_protected": s["access"] == "passcode"}, nil
}

func passcode(a args, out, _ map[string]any, run runner) (map[string]any, error) {
	s := site(a, out, run)
	o := map[string]any{"site": first(str(s["name"]), a.s("site")), "passcode_protected": s["access"] == "passcode"}
	if says := str(out["says"]); says != "" {
		o["note"] = says
	}
	return o, nil
}

func domain(a args, out, _ map[string]any, run runner) (map[string]any, error) {
	if _, ok := out["access"]; !ok && out["name"] == nil && a.s("site") == "" { // update_account: the domain for all sites
		return map[string]any{"domain": strings.TrimPrefix(a.s("domain"), "*."), "status": "pending"}, nil
	}
	s := site(a, out, run)
	if d, _ := s["domain"].(map[string]any); d != nil {
		o := map[string]any{"site": str(s["name"]), "domain": str(d["host"]), "status": "pending", "certificate": "pending"}
		switch d["status"] {
		case "live":
			o["status"], o["certificate"], o["url"] = "active", "live", "https://"+str(d["host"])+"/"
		case "certificate":
			o["certificate"] = "issuing"
		}
		for _, r := range list(d["records"]) {
			r, _ := r.(map[string]any)
			rec := map[string]any{"type": str(r["type"]), "host": str(r["host"]), "value": str(r["value"])}
			if rec["type"] == "TXT" {
				o["ownership_record"] = rec
			} else {
				o["dns_record"] = rec
			}
		}
		if says := str(d["says"]); says != "" {
			o["note"] = says
		}
		return o, nil
	}
	if short := str(s["short_name"]); short != "" {
		host := short + ".simple-host.app"
		return map[string]any{"site": str(s["name"]), "domain": host, "status": "active", "url": "https://" + host + "/"}, nil
	}
	return map[string]any{"site": str(s["name"]), "domain": nil, "status": "none"}, nil
}

// storage is the old storage tools' {status, response}.
func storage(a args, out, _ map[string]any, _ runner) (map[string]any, error) {
	return map[string]any{"status": 200, "response": out}, nil
}

func oldTeam(t any) map[string]any {
	m, _ := t.(map[string]any)
	return map[string]any{"id": str(m["id"]), "handle": str(m["handle"]), "members": len(list(m["members"])), "created_at": ""}
}

// findTools is the old find_tools answer: the current tools, from list
// ("name: about" a line).
func findTools(listed string) map[string]any {
	tools := []any{}
	for _, line := range strings.Split(listed, "\n") {
		name, about, _ := strings.Cut(line, ": ")
		if name != "" {
			tools = append(tools, map[string]any{"name": name, "title": name, "description": about})
		}
	}
	return map[string]any{"tools": tools, "next": "Call a tool with run_tool, its name and arguments."}
}

func str(v any) string { s, _ := v.(string); return s }

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	return 0
}

func list(v any) []any {
	switch l := v.(type) {
	case []any:
		return l
	case []map[string]any:
		out := make([]any, len(l))
		for i := range l {
			out[i] = l[i]
		}
		return out
	}
	return nil
}

// first is the first of vs that is set.
func first(vs ...any) any {
	for _, v := range vs {
		if v != nil && v != "" {
			return v
		}
	}
	return nil
}
