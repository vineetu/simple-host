package compat

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"simplehost/core/limits"
	"simplehost/core/web"
)

// routes are the old addresses outside callers still use (scripts with a
// key, installed skills, links handed out), each answered through the
// current routes with the old request and answer shapes.
func (c *compat) routes() {
	o := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		c.old.Handle(pattern, web.Func(fn))
	}
	// Publishing.
	o("POST /v1/sites/{site}", c.publish)
	o("POST /v1/sites/{site}/files", c.publish)
	o("PUT /v1/sites/{site}/files", c.publish)
	o("POST /v1/sites/{site}/restore", c.restore)
	// Versions.
	o("GET /v1/sites/{site}/versions", c.versions)
	o("PUT /v1/sites/{site}/active-version", c.rollback)
	o("GET /v1/sites/{site}/versions/{version}/files", c.versionFiles)
	o("GET /v1/sites/{site}/versions/{version}/files/{path...}", c.versionFile)
	// Who can open a site.
	o("GET /v1/sites/{site}/access", c.access)
	o("PUT /v1/sites/{site}/access", c.access)
	o("POST /v1/sites/{site}/viewers", c.access)
	o("DELETE /v1/sites/{site}/viewers/{email}", c.access)
	o("GET /v1/sites/{site}/lock", c.lock)
	o("PUT /v1/sites/{site}/visibility", c.visibility)
	// Saved data.
	o("GET /v1/sites/{site}/storage/resources", c.resources)
	o("PUT /v1/sites/{site}/storage/resources/{name}", c.setResource)
	o("GET /v1/sites/{site}/storage/usage", c.usage)
	// The site's own domain.
	o("GET /v1/sites/{site}/domain", c.domain)
	o("POST /v1/sites/{site}/domain", c.domain)
	o("POST /v1/sites/{site}/domain/check", c.domain)
	// Keys.
	o("POST /v1/me/keys", c.makeKey)
	o("DELETE /v1/me/keys/{id}", c.revokeKey)
	// The skill and the docs.
	o("GET /v1/skills", c.skills)
	o("GET /v1/skills/{name}", c.skill)
	o("GET /v1/skills/{name}/SKILL.md", c.skill)
	o("GET /v1/skills/{name}/references/{file}", c.skill)
	o("GET /llms.md", c.forward("/llms.txt"))
	o("GET /index.md", c.forward("/llms.txt"))
	o("GET /openapi.json", c.forward("/v1/openapi.json")) // dropped: the service's "changed" refusal
	o("GET /openapi.yaml", c.forward("/v1/openapi.json"))
	o("GET /v1/push/sw.js", c.forward("/push-sw.js"))
	// A company's sign-out link.
	o("GET /v1/sso/signed-out", c.signedOut)
	o("POST /v1/sso/signed-out", c.signedOut)

	// Still-current addresses, the old way: an old query (?create=1,
	// ?keep=1, ...) or the old skill's header.
	c.shaped.Handle("PUT /v1/sites/{site}", web.Func(c.publish))
	c.shaped.Handle("GET /v1/sites", web.Func(c.list))
	c.shaped.Handle("GET /v1/sites/{site}", web.Func(c.site))
}

// sitePath is the current address of the request's site plus rest, with
// ?owner= kept and extra added.
func sitePath(r *http.Request, rest string, extra ...string) string {
	q := url.Values{}
	if o := r.URL.Query().Get("owner"); o != "" {
		q.Set("owner", o)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		q.Set(extra[i], extra[i+1])
	}
	p := "/v1/sites/" + url.PathEscape(r.PathValue("site")) + rest
	if len(q) > 0 {
		p += "?" + q.Encode()
	}
	return p
}

func (c *compat) view(r *http.Request) (map[string]any, error) {
	return c.do(r, "GET", sitePath(r, ""), nil)
}

func (c *compat) change(r *http.Request, body map[string]any) (map[string]any, error) {
	return c.do(r, "PATCH", sitePath(r, ""), body)
}

// oldSite is a site (route 23's answer, or one site of the list) in the
// old answer's fields: site_url, active_version and the rest scripts read.
func oldSite(v map[string]any) map[string]any {
	version := v["live_version"]
	if version == nil {
		version = v["version"]
	}
	out := map[string]any{"name": v["name"], "owner": v["owner"], "site_url": v["url"], "live_url": v["url"], "active_version": version,
		"open_to_anyone": v["access"] == "open", "passcode_protected": v["access"] == "passcode", "access": "anyone", "visibility": "unlisted"}
	if v["access"] == "viewers" {
		out["access"] = "specific"
	}
	if v["listed"] == true {
		out["visibility"] = "public"
	}
	for _, k := range []string{"offline", "note"} {
		if v[k] != nil {
			out[k] = v[k]
		}
	}
	if v["changed_at"] != nil {
		out["updated_at"] = v["changed_at"]
	}
	if d, ok := v["domain"].(map[string]any); ok {
		out["custom_domain"], out["domain_status"] = d["host"], oldStatus(d["status"])
	}
	return out
}

func oldStatus(s any) string {
	if s == "live" {
		return "active"
	}
	return "pending"
}

// publish is the old publishing: JSON {files, files_base64} on .../files
// (?keep=1 on PUT keeps the files not sent) or an archive on the site
// itself; ?create=1 is what happens anyway; access/viewers on a new site.
func (c *compat) publish(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	if p := q.Get("publish"); p == "false" || p == "0" || p == "no" {
		return web.Refuse(http.StatusGone, "removed", "Drafts and preview links were removed: publishing puts the files live. Publish without ?publish=false.")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, limits.SiteBytes.N/3*4+2<<20))
	if err != nil {
		return err
	}
	access, viewers := q.Get("access"), []string{}
	for _, v := range q["viewers"] {
		viewers = append(viewers, strings.Split(v, ",")...)
	}
	keep := "false"
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var in struct {
			Files       map[string]string `json:"files"`
			FilesBase64 map[string]string `json:"files_base64"`
			RemoveFiles []string          `json:"remove_files"`
			Access      string            `json:"access"`
			Viewers     []string          `json:"viewers"`
		}
		if json.Unmarshal(body, &in) != nil {
			return web.Refuse(http.StatusBadRequest, "bad_json", `Send {"files": {"index.html": "..."}} and binary files in "files_base64".`)
		}
		if len(in.RemoveFiles) > 0 {
			return web.Refuse(http.StatusBadRequest, "send_whole_files", "remove_files is gone: send every file the site should keep, without ?keep=1.")
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.Pattern, "/files") && (q.Get("keep") == "1" || q.Get("keep") == "true") {
			keep = "true"
		}
		if in.Access != "" {
			access = in.Access
		}
		viewers = append(viewers, in.Viewers...)
		body, _ = json.Marshal(map[string]any{"files": in.Files, "files_base64": in.FilesBase64})
	}
	_, err = c.view(r)
	isNew := err != nil
	pub, err := c.do(r, "PUT", sitePath(r, "", "keep_other_files", keep), body)
	if err != nil {
		return err
	}
	if access == "specific" {
		if _, err := c.change(r, map[string]any{"access": "viewers", "viewers": viewers}); err != nil {
			return err
		}
	}
	v, err := c.view(r)
	if err != nil {
		return err
	}
	out := oldSite(v)
	if pub["note"] != nil {
		out["note"] = pub["note"]
	}
	status := http.StatusOK
	if isNew {
		status = http.StatusCreated
	}
	web.JSON(w, status, out)
	return nil
}

func (c *compat) restore(w http.ResponseWriter, r *http.Request) error {
	v, err := c.change(r, map[string]any{"restore": true})
	if err != nil {
		return err
	}
	out := oldSite(v)
	out["note"] = "Restored with all its versions and saved data."
	web.JSON(w, http.StatusOK, out)
	return nil
}

func (c *compat) list(w http.ResponseWriter, r *http.Request) error {
	path := "/v1/sites"
	if s := r.URL.Query().Get("query"); s != "" {
		path += "?" + url.Values{"q": {s}}.Encode()
	}
	v, err := c.do(r, "GET", path, nil)
	if err != nil {
		return err
	}
	list, _ := v["sites"].([]any)
	out := []any{} // the old list was a bare array
	for _, s := range list {
		if m, ok := s.(map[string]any); ok {
			out = append(out, oldSite(m))
		}
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

// site is route 23 with the old fields added.
func (c *compat) site(w http.ResponseWriter, r *http.Request) error {
	v, err := c.view(r)
	if err != nil {
		return err
	}
	for k, x := range oldSite(v) {
		if _, ok := v[k]; !ok {
			v[k] = x
		}
	}
	web.JSON(w, http.StatusOK, v)
	return nil
}

func (c *compat) versions(w http.ResponseWriter, r *http.Request) error {
	v, err := c.view(r)
	if err != nil {
		return err
	}
	list, _ := v["versions"].([]any)
	out := []any{}
	for _, x := range list {
		m, _ := x.(map[string]any)
		live := m["n"] == v["live_version"]
		status := "kept"
		if live {
			status = "live"
		}
		out = append(out, map[string]any{"version_number": m["n"], "status": status, "author": m["author"], "created_at": m["at"], "is_active": live})
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

func (c *compat) rollback(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Version int `json:"version_number"`
	}
	if err := web.Read(r, &in); err != nil {
		return err
	}
	v, err := c.change(r, map[string]any{"live_version": in.Version})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, oldSite(v))
	return nil
}

// versionDir is the folder of the version asked for ("live" or a number)
// of a site the caller manages.
func (c *compat) versionDir(r *http.Request) (string, int, bool, error) {
	if err := limits.Allow(r, limits.Requests, ""); err != nil {
		return "", 0, false, err
	}
	site, err := c.a.Sites.Of(r)
	if err == nil {
		err = c.a.Sites.Manage(r, site)
	}
	if err != nil {
		return "", 0, false, err
	}
	n := site.LiveVersion
	if v := r.PathValue("version"); v != "live" {
		if n, err = strconv.Atoi(v); err != nil {
			return "", 0, false, web.NotFound
		}
	}
	dir := c.a.Sites.VersionDir(site.ID, n)
	if _, err := os.Stat(dir); err != nil {
		return "", 0, false, web.Refuse(http.StatusNotFound, "no_version", "That version is not kept. List them with GET /v1/sites/"+site.Name+".")
	}
	return dir, n, n == site.LiveVersion, nil
}

func (c *compat) versionFiles(w http.ResponseWriter, r *http.Request) error {
	dir, n, live, err := c.versionDir(r)
	if err != nil {
		return err
	}
	files := []map[string]any{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			info, _ := d.Info()
			rel, _ := filepath.Rel(dir, p)
			files = append(files, map[string]any{"path": filepath.ToSlash(rel), "size": info.Size()})
		}
		return nil
	})
	info, _ := os.Stat(dir)
	web.JSON(w, http.StatusOK, map[string]any{"version_number": n, "is_active": live, "created_at": info.ModTime().UTC().Format(time.RFC3339), "files": files})
	return nil
}

func (c *compat) versionFile(w http.ResponseWriter, r *http.Request) error {
	dir, _, _, err := c.versionDir(r)
	if err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	name := r.PathValue("path")
	body, err := fs.ReadFile(root.FS(), name)
	if err != nil {
		return web.NotFound
	}
	h := w.Header()
	h.Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
	if h.Get("Content-Type") == "" {
		h.Set("Content-Type", "application/octet-stream")
	}
	h.Set("Cache-Control", "private, no-store")
	h.Set("Content-Security-Policy", "sandbox")
	w.Write(body)
	return nil
}

// access answers the four old viewer routes with the old SiteAccess shape.
func (c *compat) access(w http.ResponseWriter, r *http.Request) error {
	var v map[string]any
	var err error
	extra := map[string]any{}
	switch r.Method + " " + path.Base(r.Pattern) {
	case "GET access":
		v, err = c.view(r)
	case "PUT access":
		var in struct{ Access string }
		if err := web.Read(r, &in); err != nil {
			return err
		}
		mode := map[string]string{"anyone": "open", "specific": "viewers"}[in.Access]
		if mode == "" {
			return web.Refuse(http.StatusBadRequest, "bad_access", `Set "access" to "anyone" or "specific".`)
		}
		v, err = c.change(r, map[string]any{"access": mode})
	case "POST viewers":
		var in struct{ Emails []string }
		if err := web.Read(r, &in); err != nil {
			return err
		}
		v, err = c.change(r, map[string]any{"access": "viewers", "viewers_add": in.Emails})
		extra["added"] = in.Emails
	default: // DELETE .../viewers/{email}
		v, err = c.change(r, map[string]any{"viewers_remove": []string{r.PathValue("email")}})
		extra["removed"] = r.PathValue("email")
	}
	if err != nil {
		return err
	}
	old := oldSite(v)
	viewers := []map[string]any{}
	list, _ := v["viewers"].([]any)
	for _, x := range list {
		m, _ := x.(map[string]any)
		viewers = append(viewers, map[string]any{"email": m["email"]})
	}
	extra["site"], extra["access"], extra["viewers"], extra["passcode_protected"] = v["name"], old["access"], viewers, old["passcode_protected"]
	web.JSON(w, http.StatusOK, extra)
	return nil
}

func (c *compat) lock(w http.ResponseWriter, r *http.Request) error {
	v, err := c.view(r)
	if err != nil {
		return err
	}
	out := map[string]any{"site": v["name"], "passcode_protected": v["access"] == "passcode"}
	if p, ok := v["passcode"].(string); ok && p != "" {
		out["passcode"] = p
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

func (c *compat) visibility(w http.ResponseWriter, r *http.Request) error {
	var in struct{ Visibility string }
	if err := web.Read(r, &in); err != nil {
		return err
	}
	if in.Visibility != "public" && in.Visibility != "unlisted" {
		return web.Refuse(http.StatusBadRequest, "bad_visibility", `Set "visibility" to "public" or "unlisted".`)
	}
	v, err := c.change(r, map[string]any{"listed": in.Visibility == "public"})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusOK, map[string]any{"name": v["name"], "visibility": in.Visibility})
	return nil
}

// resources lists the namespaces as the old storage resources: a namespace
// whose only rules are for its values or its files reads as kv or files.
func (c *compat) resources(w http.ResponseWriter, r *http.Request) error {
	v, err := c.view(r)
	if err != nil {
		return err
	}
	list, _ := v["namespaces"].([]any)
	out := []any{}
	for _, x := range list {
		n, _ := x.(map[string]any)
		rules, _ := n["rules"].(map[string]any)
		res := map[string]any{"name": n["name"], "kind": "sqlite", "rules": map[string]any{}, "rules_status": "ok"}
		_, kv := rules["_values"]
		_, files := rules["_files"]
		switch {
		case len(rules) == 1 && kv:
			res["kind"], res["rules"] = "kv", rules["_values"]
		case len(rules) == 1 && files:
			res["kind"], res["rules"] = "files", rules["_files"]
		default:
			tables := map[string]any{}
			for t, rl := range rules {
				tables[t] = map[string]any{"rules": rl}
			}
			res["tables"] = tables
		}
		out = append(out, res)
	}
	web.JSON(w, http.StatusOK, map[string]any{"resources": out})
	return nil
}

func (c *compat) setResource(w http.ResponseWriter, r *http.Request) error {
	var in map[string]any
	if err := web.Read(r, &in); err != nil {
		return err
	}
	rules, spam, err := namespaceRules(in)
	if err != nil {
		return err
	}
	name := r.PathValue("name")
	if _, err := c.do(r, "PUT", sitePath(r, "/storage/"+url.PathEscape(name)+"/rules"), spamRules(rules, spam)); err != nil {
		return err
	}
	kind, _ := in["kind"].(string)
	web.JSON(w, http.StatusOK, map[string]any{"name": name, "kind": kind, "rules": in["rules"], "tables": in["tables"], "rules_status": "ok"})
	return nil
}

// namespaceRules turns an old resource body ({kind, rules, tables}, or
// {kind, preset, tables: {t: {preset}}} with read/add/edit/delete overrides)
// into a namespace's rules per table: kv rules are for _values, files for
// _files. spam is true when a preset lets anyone who passes the spam check add.
func namespaceRules(b args) (map[string]any, bool, error) {
	rules, spam := map[string]any{}, false
	set := func(table string, x args) {
		if x["preset"] == nil && x["read"] == nil && x["add"] == nil && x["edit"] == nil && x["delete"] == nil {
			if x["rules"] != nil {
				rules[table] = x["rules"]
			}
			return
		}
		policies, checked := presetPolicies(x, b["approve_first"] == true)
		rules[table], spam = map[string]any{"version": 1, "policies": policies}, spam || checked
	}
	switch b.s("kind") {
	case "kv":
		set("_values", b)
	case "files":
		set("_files", b)
	default:
		tables, _ := b["tables"].(map[string]any)
		for t, v := range tables {
			if m, ok := v.(map[string]any); ok {
				set(t, m)
			}
		}
		if len(rules) == 0 && (b["rules"] != nil || (b["preset"] != nil && b.s("preset") != "private")) {
			return nil, false, web.Refuse(http.StatusBadRequest, "rules_per_table", `SQLite rules are now set per table: make the tables first (with a visitor_id TEXT column where visitors' own rows matter), then send "tables", like {"orders": {"preset": "records"}}.`)
		}
	}
	for t, v := range rules {
		if v == nil {
			delete(rules, t)
		}
	}
	return rules, spam, nil
}

// spamRules is a rules body, with the spam check switched on when a preset needs it.
func spamRules(rules map[string]any, spam bool) map[string]any {
	if spam {
		return map[string]any{"rules": rules, "spam_check": true}
	}
	return map[string]any{"rules": rules}
}

// presets are the old read/add/edit/delete settings by name.
var presets = map[string][4]string{
	"public": {"anyone", "owner", "owner", "owner"}, "inbox": {"owner", "anyone", "owner", "owner"},
	"wall": {"anyone", "signed-in", "owner", "own"}, "comments": {"anyone", "checked", "owner", "owner"},
	"records": {"own", "signed-in", "owner", "owner"}, "personal": {"own", "signed-in", "own", "own"},
	"board": {"signed-in", "signed-in", "signed-in", "owner"}, "private": {"owner", "owner", "owner", "owner"},
}

// presetPolicies is a preset (with its overrides) as rules, the way the old
// service converted them; checked is true when adding needs the spam check.
func presetPolicies(x args, approveFirst bool) ([]any, bool) {
	m, ok := presets[x.s("preset")]
	if !ok {
		m = presets["private"]
	}
	policies, checked := []any{}, false
	for i, cmd := range []string{"read", "add", "edit", "delete"} {
		who := m[i]
		if o := x.s(cmd); o != "" {
			who = o
		}
		expr := map[string]string{"owner": `auth.role == "owner"`, "own": `visitor_id == auth.id || auth.role == "owner"`,
			"signed-in": "auth.signed_in", "checked": `auth.checked || auth.ai || auth.role == "owner"`, "anyone": "true"}[who]
		if expr == "" { // nobody
			continue
		}
		if cmd == "add" {
			checked = checked || who == "checked"
			policies = append(policies, map[string]any{"name": cmd, "command": cmd, "check": expr})
		} else {
			policies = append(policies, map[string]any{"name": cmd, "command": cmd, "using": expr})
		}
	}
	if approveFirst && checked {
		policies = append(policies, map[string]any{"name": "approve_first", "command": "read", "restrictive": true,
			"using": `auth.role == "owner" || approved_at is not null`})
	}
	return policies, checked
}

func (c *compat) usage(w http.ResponseWriter, r *http.Request) error {
	v, err := c.view(r)
	if err != nil {
		return err
	}
	use, _ := v["use"].(map[string]any)
	used, _ := use["data_bytes"].(float64)
	limit, _ := use["data_bytes_limit"].(float64)
	web.JSON(w, http.StatusOK, map[string]any{"used_bytes": int64(used), "limit_bytes": int64(limit), "remaining_bytes": int64(max(limit-used, 0))})
	return nil
}

// domain is the three old domain routes: read, connect (a free
// <name>.simple-host.app is a short name now) and check (checks run on
// their own now: it reads).
func (c *compat) domain(w http.ResponseWriter, r *http.Request) error {
	var v map[string]any
	var err error
	if r.Method == http.MethodPost && !strings.HasSuffix(r.Pattern, "/check") {
		var in struct{ Domain string }
		if err := web.Read(r, &in); err != nil {
			return err
		}
		d := strings.ToLower(strings.TrimSpace(in.Domain))
		if name, ok := strings.CutSuffix(d, "."+c.a.Config.AppDomain); ok && !strings.Contains(name, ".") {
			v, err = c.change(r, map[string]any{"short_name": name})
		} else {
			v, err = c.change(r, map[string]any{"domain": d})
		}
	} else {
		v, err = c.view(r)
	}
	if err != nil {
		return err
	}
	d, _ := v["domain"].(map[string]any)
	if d == nil {
		if strings.HasSuffix(r.Pattern, "/check") {
			return web.Refuse(http.StatusNotFound, "no_domain", "No domain is connected to this site.")
		}
		web.JSON(w, http.StatusOK, map[string]any{"domain": nil, "status": nil})
		return nil
	}
	out := map[string]any{"domain": d["host"], "status": oldStatus(d["status"])}
	if d["error"] != nil {
		out["last_error"] = d["error"]
	}
	records, _ := d["records"].([]any)
	for _, x := range records {
		m, _ := x.(map[string]any)
		rec := map[string]any{"type": m["type"], "host": m["host"], "value": m["value"]}
		if m["type"] == "TXT" {
			out["dns_txt"] = rec
		} else if out["dns"] == nil {
			out["dns"] = rec
		}
	}
	web.JSON(w, http.StatusOK, out)
	return nil
}

func (c *compat) makeKey(w http.ResponseWriter, r *http.Request) error {
	var in map[string]any
	if err := web.Read(r, &in); err != nil {
		return err
	}
	k, err := c.do(r, "POST", "/v1/keys", map[string]any{"name": in["name"]})
	if err != nil {
		return err
	}
	web.JSON(w, http.StatusCreated, map[string]any{"id": k["id"], "name": k["name"], "last4": k["last4"], "scope": "full",
		"created_at": time.Now().UTC().Format(time.RFC3339), "last_used_at": nil, "expires_at": nil, "api_key": k["key"],
		"message": "Keep this key: it is shown once."})
	return nil
}

func (c *compat) revokeKey(w http.ResponseWriter, r *http.Request) error {
	if _, err := c.do(r, "DELETE", "/v1/keys/"+url.PathEscape(r.PathValue("id")), nil); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// skillFile is the current skill's SKILL.md, from the one skill set.
func (c *compat) skillFile(r *http.Request) ([]byte, error) {
	a := c.send(r, "GET", "/skills.zip", nil)
	z, err := zip.NewReader(bytes.NewReader(a.body), int64(len(a.body)))
	if err != nil {
		return nil, err
	}
	for _, f := range z.File {
		if path.Base(f.Name) == "SKILL.md" {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, web.NotFound
}

// skills is the old catalog, listing the one current skill.
func (c *compat) skills(w http.ResponseWriter, r *http.Request) error {
	body, err := c.skillFile(r)
	if err != nil {
		return err
	}
	about := ""
	for _, line := range strings.Split(string(body), "\n") {
		if s, ok := strings.CutPrefix(line, "description: "); ok {
			about = s
			break
		}
	}
	base := "https://" + c.a.Config.AppDomain
	web.JSON(w, http.StatusOK, map[string]any{"plugin": "simple-host", "version": "2", "count": 1,
		"skills": []any{map[string]any{"name": "simple-host", "description": about, "url": base + "/v1/skills/simple-host"}}})
	return nil
}

// skill serves the current SKILL.md for any old skill name, so an installed
// skill that reads itself again gets the new instructions. The old
// references are folded into it.
func (c *compat) skill(w http.ResponseWriter, r *http.Request) error {
	body, err := c.skillFile(r)
	if err != nil {
		return err
	}
	if r.PathValue("file") != "" {
		body = append([]byte("> The Simple Host skill is now one file, and this reference is part of it (below). Reinstall the skill from https://"+
			c.a.Config.AppDomain+"/skills.zip.\n\n"), body...)
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write(body)
	return nil
}

// forward answers an old address with a current one's answer.
func (c *compat) forward(to string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		pass(w, c.send(r, "GET", to, nil))
		return nil
	}
}

func (c *compat) signedOut(w http.ResponseWriter, r *http.Request) error {
	http.Redirect(w, r, "/", http.StatusSeeOther)
	return nil
}
