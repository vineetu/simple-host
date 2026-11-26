package mcp

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"simplehost/core/web"
)

// tool is one row of the table: what the AI app sees, and the route the
// call replays with the caller's identity. A route func returns the method,
// path and JSON body; method "" means the answer is body itself (an in-chat
// screen opens instead of a call, or the tool made its own calls).
//
// The hints follow ONE rule, OpenAI's and MCP's definitions, checked by
// TestToolHintsFollowTheRule (tools_test.go) against the routes each tool
// really replays:
//   - readOnly: it only reads (every route it calls is a GET, or a POST that
//     only reads: an SQL query, the table list).
//   - destructive: it can delete, overwrite or replace what exists, or end or
//     narrow someone's access (any PUT, PATCH or DELETE, or an SQL statement).
//     Being undoable (the bin, older versions) does not make it false.
//   - openWorld: it reaches past the person's own account: it emails an
//     address they name, connects an outside domain, or can make content
//     readable by anyone on the internet (publishing, listing, opening a
//     site, rules, a public file store).
//   - idempotent: calling it again with the same arguments changes nothing
//     more (reads, and PUT/PATCH/DELETE that set a given state; never a POST
//     that adds, a publish, which makes a new version, or a generated code).
type tool struct {
	name, title, about string
	input, output      map[string]any
	readOnly           bool
	destructive        bool
	openWorld          bool
	idempotent         bool
	screen             string // the in-chat screen shown with the result (mcp.go)
	appOnly            bool   // called by an in-chat screen, hidden from the model
	operator           bool   // listed and answered for the operator only
	route              func(c *call) (method, path string, body any, err error)
}

// Input schema helpers.
func str(d string) map[string]any     { return map[string]any{"type": "string", "description": d} }
func boolean(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func integer(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
func object(d string) map[string]any  { return map[string]any{"type": "object", "description": d} }
func list(d string, item map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": d, "items": item}
}

// in is an object schema: name, schema pairs; names ending in "!" are required.
func in(pairs ...any) map[string]any {
	props, req := map[string]any{}, []string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		name := pairs[i].(string)
		if n, ok := strings.CutSuffix(name, "!"); ok {
			name = n
			req = append(req, name)
		}
		props[name] = pairs[i+1]
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

// site, owner: every site tool takes them.
func siteIn(pairs ...any) map[string]any {
	return in(append([]any{"site!", str("The site's name, or its address (any site you may open)."),
		"owner", str("The team handle (or the person's handle) that owns the site, when it is not yours.")}, pairs...)...)
}

// nsIn: a namespace of a site; tableIn: one table in it.
func nsIn(pairs ...any) map[string]any {
	return siteIn(append([]any{"namespace!", str("The namespace (one SQLite database of the site), like bookings.")}, pairs...)...)
}
func tableIn(pairs ...any) map[string]any {
	return nsIn(append([]any{"table!", str("The table, like bookings.")}, pairs...)...)
}

var emails = list("Email addresses.", str("An email address."))

// Output schema helpers: the fields a tool's structuredContent always or
// may have. Objects stay open (more fields may come).
var (
	oStr  = map[string]any{"type": "string"}
	oNum  = map[string]any{"type": "number"}
	oInt  = map[string]any{"type": "integer"}
	oBool = map[string]any{"type": "boolean"}
	oAny  = map[string]any{}
	oObj  = map[string]any{"type": "object"}
)

func oList(item map[string]any) map[string]any { return map[string]any{"type": "array", "items": item} }
func oNull(t string) map[string]any            { return map[string]any{"type": []any{t, "null"}} }

// out is an output schema: like in, plus the description.
func out(pairs ...any) map[string]any { return in(pairs...) }

var (
	outRows  = out("columns!", oList(oStr), "rows!", oList(oList(oAny)), "next_after!", oStr, "mine", oList(oBool))
	outItems = func(item map[string]any) map[string]any { return out("items!", oList(item), "next_after!", oStr) }
	outDone  = out("deleted!", oBool)
	outSQL   = out("changes!", oInt, "last_insert_id!", oInt)
	outSite  = out("name!", oStr, "owner!", oStr, "url!", oStr, "live_version!", oInt)
)

var tools = []tool{
	// Account
	{name: "who_am_i", title: "Who am I",
		about: "The signed-in account: handle, display name, teams (with member counts) and invitations waiting, key and app names, and use against the limits. " +
			"Leaves out email addresses unless include_email is true.",
		input: in("include_email", boolean("True only when the person asks which email they are signed in with, or for their team members' emails.")),
		output: out("email!", oStr, "handle!", oStr, "display_name!", oStr, "operator!", oBool, "home_site!", oAny, "domain!", oStr,
			"teams!", oList(out("handle!", oStr, "display_name!", oStr, "members!", oInt, "invited!", oInt, "member_emails", oList(oStr), "invited_emails", oList(oStr))),
			"invitations!", oList(out("team!", oStr, "display_name!", oStr)), "keys!", oList(out("name!", oStr)), "apps!", oList(out("name!", oStr)),
			"devices!", oList(out("name!", oStr)), "use!", oObj),
		readOnly: true, idempotent: true, route: whoAmI},
	{name: "update_account", title: "Change account names",
		about: "Change the handle, display name, home site (a site shown at the handle's own address) or the domain for all sites, of me or of a team. " +
			"Ask first before changing a handle: old links stop working and the old handle can never be used again.",
		input: in("owner", str(`"me" (default) or a team handle.`), "handle", str("New handle."), "display_name", str("Display name."),
			"home_site", str(`One of the owner's sites, or "" for the list of listed sites.`), "domain", str(`A domain for all sites, like example.com; "" removes it.`)),
		output: out("handle", oStr, "display_name", oStr), destructive: true, openWorld: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			return "PATCH", "/v1/owners/" + url.PathEscape(c.or("owner", "me")), c.pick("handle", "display_name", "home_site", "domain"), nil
		}},
	{name: "download_my_data", title: "Download my data",
		about: "Queue a download of everything: every site, its files and saved data, and account details. A link to it is emailed to the account's own address.",
		input: in(), output: out("status!", oStr, "says!", oStr),
		route: fixed("POST", "/v1/me/export")},
	{name: "delete_account", title: "Delete my account",
		about: "Delete my account: every site, its data, keys and app connections, at once. Cannot be undone. Ask first.", input: in(),
		output: out("deleted!", oBool, "sites!", oInt), destructive: true, idempotent: true,
		route: fixed("DELETE", "/v1/me")},
	// Teams
	{name: "create_team", title: "Create a team",
		about: "Make a team, with me as its first member. It has its own handle, so its sites have their own addresses.",
		input: in("handle!", str("The team's handle."), "display_name", str("Its display name.")), output: out("handle!", oStr),
		route: func(c *call) (string, string, any, error) {
			return "POST", "/v1/teams", c.pick("handle", "display_name"), nil
		}},
	{name: "invite_to_team", title: "Invite to a team",
		about: "Invite someone to a team: Simple Host sends an invitation email to the address given. They become a member only when they accept.",
		input: in("team!", str("The team's handle."), "email!", str("Their email.")), output: out("id!", oStr), openWorld: true,
		route: func(c *call) (string, string, any, error) {
			return "POST", "/v1/teams/" + url.PathEscape(c.str("team")) + "/invites", c.pick("email"), nil
		}},
	{name: "accept_invite", title: "Accept a team invitation",
		about: "Accept an invitation to a team (who_am_i lists the invitations waiting) and become a member.",
		input: in("team!", str("The team's handle, as who_am_i lists the invitation.")), output: out("team_id!", oStr),
		route: acceptInvite},
	{name: "remove_from_team", title: "Remove from a team",
		about: "Remove a member from a team by their email, or leave it (email \"me\"). They lose access to the team's sites. Ask first.",
		input: in("team!", str("The team's handle."), "email!", str(`The member's email, or "me" to leave.`)), output: out("removed!", oBool),
		destructive: true, idempotent: true, route: removeFromTeam},
	{name: "delete_team", title: "Delete a team",
		about: "Delete a team that has no sites. Its handle can never be used again. Ask first.", input: in("team!", str("The team's handle.")),
		output: out("deleted!", oStr), destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			return "DELETE", "/v1/teams/" + url.PathEscape(c.str("team")), nil, nil
		}},
	// Sites
	{name: "list_sites", title: "List my sites",
		about:    "My sites and my teams' sites, most recently changed first, with their addresses. Shown in the chat.",
		input:    in("q", str("Text to search site names for."), "bin", boolean("List the bin (deleted in the last 7 days) instead.")),
		output:   out("sites!", oList(out("name!", oStr, "owner!", oStr, "url!", oStr))),
		readOnly: true, idempotent: true, screen: "my-sites",
		route: func(c *call) (string, string, any, error) {
			q := url.Values{"q": {c.str("q")}}
			if c.args["bin"] == true {
				q.Set("bin", "true")
			}
			return "GET", "/v1/sites?" + q.Encode(), nil, nil
		}},
	{name: "get_site", title: "Get a site",
		about: "Everything about one site: address, versions, who can open it, domain status, saved data (namespaces and tables), rules, notifications and use.",
		input: siteIn(), output: outSite, readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) { p, err := c.site("", nil); return "GET", p, nil, err }},
	{name: "publish_site", title: "Publish a site",
		about: "Publish files and get the live address back; anyone with the address can open a new site. Makes the site if it is new. " +
			"Replaces the live files: those not sent are removed, unless keep_other_files is true.",
		input: siteIn("files", object("Path -> text content, like {\"index.html\": \"...\"}."), "files_base64", object("Path -> base64 content, for images and other binary files."),
			"keep_other_files", boolean("Keep the live files not sent (to change a few files).")),
		output: out("url!", oStr, "version!", oInt, "site!", oStr), destructive: true, openWorld: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", url.Values{"keep_other_files": {c.str("keep_other_files")}})
			return "PUT", p, c.pick("files", "files_base64"), err
		}},
	{name: "create_upload_link", title: "Add photos",
		about: "The photo picker, shown in the chat, where the person adds photos (shrunk on their device) straight to a namespace's file store: photos unless namespace says otherwise. " +
			"A new photos namespace can be read by anyone who can open the site, and written only by the owner. The answer's url is the same picker as a page, for apps without screens. " +
			"With kind archive: a one-time link to upload a big site as one archive.",
		input:  siteIn("namespace", str("Where photos and files go; photos when left out."), "kind", str(`"files" (the default) or "archive".`)),
		output: out("url!", oStr, "kind!", oStr, "namespace!", oStr, "expires_at!", oStr), openWorld: true, screen: "photo-picker",
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("/upload-links", nil)
			kind, ns := c.or("kind", "files"), c.or("namespace", "photos")
			if kind == "archive" {
				ns = ""
			}
			return "POST", p, map[string]any{"kind": kind, "namespace": ns, "public": c.str("namespace") == "" && kind == "files"}, err
		}},
	{name: "read_site_file", title: "Read a site file",
		about:    "Read one published file of any site you may open, like index.html, before changing it.",
		input:    siteIn("path!", str("The file's path, like index.html.")),
		output:   out("text", oStr, "type", oStr, "bytes", oInt, "says", oStr),
		readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			parts := strings.Split(strings.TrimPrefix(c.str("path"), "/"), "/")
			for i := range parts {
				parts[i] = url.PathEscape(parts[i])
			}
			p, err := c.site("/files/"+strings.Join(parts, "/"), nil)
			return "GET", p, nil, err
		}},
	{name: "change_site", title: "Change a site",
		about: "Take a site offline or back online, list it on the owner's public page, make an older version live, or restore it from the bin. Ask first.",
		input: siteIn("offline", boolean("Offline shows a plain offline page; everything is kept."), "listed", boolean("Show it on the owner's public page."),
			"live_version", integer("A kept version to make live."), "restore", boolean("Bring it back from the bin.")),
		output: outSite, destructive: true, openWorld: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("offline", "listed", "live_version", "restore"), err
		}},
	{name: "delete_site", title: "Delete a site",
		about: "Move a site to the bin, with its saved data. It can be restored for 7 days. Ask first.", input: siteIn(),
		output: out("deleted!", oStr, "restore_until!", oStr), destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) { p, err := c.site("", nil); return "DELETE", p, nil, err }},
	{name: "set_site_access", title: "Set who can open a site",
		about: "Who may open the site: anyone (open), a passcode, or people named by email; or sign everyone out. " +
			"For a passcode, call with access \"passcode\": the person sets or sees it in the screen shown, so it never enters the chat. Ask first.",
		input: siteIn("access", str(`"open", "passcode" or "viewers".`), "viewers", emails,
			"viewers_add", emails, "viewers_remove", emails, "sign_everyone_out", boolean("End every unlock and visitor sign-in.")),
		output: out("site", oStr), destructive: true, openWorld: true, idempotent: true, screen: "passcode",
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			if c.str("access") == "passcode" && c.str("passcode") == "" && err == nil {
				return "", "", map[string]any{"site": c.str("site"), "owner": c.str("owner"),
					"says": "The passcode screen is open in the chat. The person sets the passcode there; it never enters the chat."}, nil
			}
			// passcode: only the old tools' callers send it (next/compat).
			return "PATCH", p, c.pick("access", "passcode", "viewers", "viewers_add", "viewers_remove", "sign_everyone_out"), err
		}},
	// The passcode screen's own calls (passcode.go): hidden from the model.
	{name: "passcode_screen_open", title: "Passcode screen: open", appOnly: true,
		about:    "The passcode screen's first call: the site's passcode state and, with client_key, a sealed session.",
		input:    siteIn("client_key", str("The screen's P-256 public key, base64url.")),
		output:   out("site!", oStr, "owner!", oStr, "passcode_protected!", oBool, "server_key!", oStr, "session", oStr),
		readOnly: true, idempotent: true, route: passcodeStep("begin")},
	{name: "passcode_screen_show", title: "Passcode screen: show", appOnly: true,
		about:    "Show the site's passcode in the screen, sealed for it alone, so it never enters the chat.",
		input:    siteIn("session!", str("The session passcode_screen_open gave.")),
		output:   out("site!", oStr, "passcode_protected!", oBool, "sealed", out("nonce!", oStr, "ciphertext!", oStr), "note", oStr),
		readOnly: true, idempotent: true, route: passcodeStep("show")},
	{name: "passcode_screen_set", title: "Passcode screen: set", appOnly: true,
		about:  "Set the passcode the person typed in the screen, sent sealed. Replaces the old passcode: visitors need the new one.",
		input:  siteIn("session!", str("The session passcode_screen_open gave."), "nonce!", str("AES-GCM nonce, base64url."), "ciphertext!", str("The sealed passcode, base64url.")),
		output: out("site!", oStr, "passcode_protected!", oBool), destructive: true, idempotent: true, route: passcodeStep("set")},
	{name: "passcode_screen_generate", title: "Passcode screen: new passcode", appOnly: true,
		about:  "Make a new random passcode and show it sealed in the screen. Replaces the old passcode: visitors need the new one.",
		input:  siteIn("session!", str("The session passcode_screen_open gave.")),
		output: out("site!", oStr, "passcode_protected!", oBool, "sealed", out("nonce!", oStr, "ciphertext!", oStr)), destructive: true, route: passcodeStep("generate")},
	{name: "set_site_address", title: "Set a site's address",
		about:  "Connect the site's own domain (answers with the DNS records to add and what is missing), or give it a free short name (<name>.simple-host.app, live at once). \"\" removes either. Ask first.",
		input:  siteIn("domain", str("Like shop.example.com."), "short_name", str("Like bakery.")),
		output: outSite, destructive: true, openWorld: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("domain", "short_name"), err
		}},
	{name: "site_stats", title: "Site visitor numbers",
		about:  "Visitor numbers of a site: visits per day, top pages, referrers and countries. Shown in the chat.",
		input:  siteIn("days", integer("Days back, 1 to 365 (30).")),
		output: out("days!", oList(oObj), "views!", oInt, "people!", oInt, "bots!", oInt), readOnly: true, idempotent: true, screen: "dashboard",
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("/stats", url.Values{"days": {c.str("days")}})
			return "GET", p, nil, err
		}},
	{name: "set_notifications", title: "Set notifications",
		about:  "What the owner is told about on this site, in the AI app and by browser push: data changes in a table, _values or _files, or someone asking for access. Replaces the list; an empty list turns them off.",
		input:  siteIn("notifications!", list(`Like {"kind": "data", "namespace": "bookings", "target": "bookings"} or {"kind": "access"}.`, object("One notification."))),
		output: outSite, destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("notifications"), err
		}},
	// Saved data: rows, values and stored files, with the caller's own rights
	// (the routes pages use), on any site they may open.
	{name: "list_rows", title: "List rows",
		about: "Read rows of a table in a site's namespace, with your rights (as the site's pages do). Filter by column values; page with after.",
		input: tableIn("where", object(`Column -> value: only rows where each column equals it, like {"status": "new"}.`), "order", str("A column to order by (default: oldest first)."),
			"desc", boolean("Newest or largest first."), "after", str("next_after from the previous page."), "limit", integer("Rows per page, 1 to 500 (100).")),
		output: outRows, readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			q := url.Values{"order": {c.str("order")}, "after": {c.str("after")}, "limit": {c.str("limit")}}
			if c.args["desc"] == true {
				q.Set("desc", "1")
			}
			if w, ok := c.args["where"].(map[string]any); ok {
				for k, v := range w {
					q.Set("where."+k, (&call{args: map[string]any{"v": v}}).str("v"))
				}
			}
			p, err := c.rows("", q)
			return "GET", p, nil, err
		}},
	{name: "read_row", title: "Read a row",
		about:  "Read one row of a table by its id, with your rights.",
		input:  tableIn("id!", integer("The row's id.")),
		output: out("columns!", oList(oStr), "row!", oList(oAny), "mine", oBool), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.rows("/"+url.PathEscape(c.str("id")), nil)
			return "GET", p, nil, err
		}},
	{name: "add_row", title: "Add a row",
		about:  "Add one row to a table, with your rights (as a visitor's form does). Answers with its id.",
		input:  tableIn("row!", object(`Column -> value, like {"name": "Ana", "guests": 2}.`)),
		output: outSQL,
		route: func(c *call) (string, string, any, error) {
			p, err := c.rows("", nil)
			return "POST", p, c.obj("row"), err
		}},
	{name: "change_row", title: "Change a row",
		about:  "Change columns of one row by its id, with your rights. The values given replace the old ones.",
		input:  tableIn("id!", integer("The row's id."), "row!", object("Column -> new value.")),
		output: out("changes!", oInt), destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.rows("/"+url.PathEscape(c.str("id")), nil)
			return "PATCH", p, c.obj("row"), err
		}},
	{name: "delete_row", title: "Delete a row",
		about:  "Delete one row by its id, with your rights. Cannot be undone. Ask first.",
		input:  tableIn("id!", integer("The row's id.")),
		output: outDone, destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.rows("/"+url.PathEscape(c.str("id")), nil)
			return "DELETE", p, nil, err
		}},
	{name: "list_values", title: "List values",
		about:  "Read the values (key -> JSON value, a namespace's _values) of a site, with your rights, like a menu or today's prices. Filter by key prefix; page with after.",
		input:  nsIn("prefix", str("Only keys that start with this."), "after", str("next_after from the previous page."), "limit", integer("Values per page, 1 to 500 (100).")),
		output: outItems(out("key!", oStr, "value!", oAny)), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.values("", url.Values{"prefix": {c.str("prefix")}, "after": {c.str("after")}, "limit": {c.str("limit")}})
			return "GET", p, nil, err
		}},
	{name: "read_value", title: "Read a value",
		about:  "Read one value by its key, with your rights.",
		input:  nsIn("key!", str("The key, like today.")),
		output: out("key!", oStr, "value!", oAny), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.values("/"+url.PathEscape(c.str("key")), nil)
			return "GET", p, nil, err
		}},
	{name: "set_value", title: "Set a value",
		about:  "Set one value by its key, with your rights: adds it, or replaces the value the key had.",
		input:  nsIn("key!", str("The key, like today."), "value!", map[string]any{"description": "Any JSON value."}),
		output: out("key!", oStr, "value!", oAny), destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.values("/"+url.PathEscape(c.str("key")), nil)
			return "PUT", p, map[string]any{"value": c.args["value"]}, err
		}},
	{name: "delete_value", title: "Delete a value",
		about:  "Delete one value by its key, with your rights. Cannot be undone. Ask first.",
		input:  nsIn("key!", str("The key.")),
		output: outDone, destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.values("/"+url.PathEscape(c.str("key")), nil)
			return "DELETE", p, nil, err
		}},
	{name: "list_files", title: "List stored files",
		about:  "List the files stored in a namespace's file store (photos, visitors' uploads), with your rights. Filter by path prefix; page with after.",
		input:  nsIn("prefix", str("Only paths that start with this."), "after", str("next_after from the previous page."), "limit", integer("Files per page, 1 to 500 (100).")),
		output: outItems(out("path!", oStr, "bytes!", oInt, "content_type!", oStr)), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.files("", url.Values{"prefix": {c.str("prefix")}, "after": {c.str("after")}, "limit": {c.str("limit")}})
			return "GET", p, nil, err
		}},
	{name: "save_file", title: "Store a file",
		about:  "Store one file in a namespace's file store, with your rights; a file already at that path is replaced. For the person's own photos, prefer create_upload_link.",
		input:  nsIn("path!", str("Where it goes, like menu/soup.jpg."), "content_base64!", str("The file's bytes, base64."), "content_type", str("Like image/jpeg.")),
		output: out("path!", oStr, "bytes!", oInt, "content_type!", oStr), destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.files("/"+escPath(c.str("path")), nil)
			if err != nil {
				return "", "", nil, err
			}
			b, err := base64.StdEncoding.DecodeString(c.str("content_base64"))
			if err != nil {
				return "", "", nil, web.Refuse(http.StatusBadRequest, "bad_base64", "Send content_base64 as standard base64.")
			}
			return "PUT", p, file{b, c.or("content_type", "application/octet-stream")}, nil
		}},
	{name: "delete_file", title: "Delete a stored file",
		about:  "Delete one file from a namespace's file store, with your rights. Cannot be undone. Ask first.",
		input:  nsIn("path!", str("The file's path, as list_files gives it.")),
		output: outDone, destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.files("/"+escPath(c.str("path")), nil)
			return "DELETE", p, nil, err
		}},
	// The owner's SQL on their own site's namespaces.
	{name: "list_tables", title: "List tables",
		about:  "The owner's view of a namespace of their site: every table with its CREATE statement, and the space the namespace and the site use.",
		input:  nsIn(),
		output: out("tables!", oList(out("name!", oStr, "sql!", oStr)), "bytes!", oInt, "site_bytes!", oInt, "site_limit!", oInt), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.sql("schema")
			return "POST", p, map[string]any{}, err
		}},
	{name: "query_sql", title: "Query with SQL",
		about:  "The owner's read-only SQL on a namespace of their site: one SELECT (or WITH ... SELECT). Values are in the table _values, stored files' records in _files. Statements that change anything are refused.",
		input:  nsIn("sql!", str("One SELECT statement."), "params", list("Values for ? in order.", map[string]any{})),
		output: out("columns!", oList(oStr), "rows!", oList(oList(oAny))), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.sql("query")
			return "POST", p, c.pick("sql", "params"), err
		}},
	{name: "run_sql", title: "Run an SQL statement",
		about: "The owner's SQL statement on a namespace of their site, made if new: CREATE, ALTER, INSERT, UPDATE, DELETE or DROP. It can change or delete saved data. Ask first before deleting or dropping. " +
			"To read, use query_sql.",
		input:  nsIn("sql!", str("One statement."), "params", list("Values for ? in order.", map[string]any{})),
		output: outSQL, destructive: true,
		route: func(c *call) (string, string, any, error) {
			word, _, _ := strings.Cut(strings.Join(strings.Fields(strings.ToLower(c.str("sql"))), " "), " ") // any space, a newline too
			if word == "select" || word == "values" || word == "explain" {
				return "", "", nil, web.Refuse(http.StatusBadRequest, "use_query_sql", "run_sql changes data and answers with no rows. To read, call query_sql with the same arguments.")
			}
			p, err := c.sql("execute")
			return "POST", p, c.pick("sql", "params"), err
		}},
	{name: "set_data_rules", title: "Set data rules",
		about:  "Who may read, add, change and delete each table, _values and _files of a namespace; replaces its rules. No rules: only the owner. Rules can let anyone on the internet read or write. Ask first when it widens who can read or write.",
		input:  nsIn("rules!", object("Table -> rule set, as in the recipes."), "spam_check", boolean("Anonymous adds must pass the spam check (auth.checked).")),
		output: out("namespace!", oStr, "rules!", oObj, "spam_check!", oBool), destructive: true, openWorld: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("/storage/"+url.PathEscape(c.str("namespace"))+"/rules", nil)
			return "PUT", p, c.pick("rules", "spam_check"), err
		}},
	{name: "get_recipe", title: "Get a page recipe",
		about: "A page recipe: the data setup, a working page, and who can see what. Use it before building a form or a page that saves entries. " +
			"Topics: inbox (contact, quote, sign-up forms), public (menu, prices), personal (bookings), comments (guestbook), portal (per-client pages), gallery (visitors' photos).",
		input:  in("topic!", str("inbox, public, personal, comments, portal or gallery."), "site", str("The site's name, filled into the recipe.")),
		output: out("topic!", oStr, "text!", oStr), readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			return "GET", "/v1/recipes/" + url.PathEscape(c.str("topic")) + "?" + url.Values{"site": {c.str("site")}}.Encode(), nil, nil
		}},
	// Operator
	{name: "admin_overview", title: "Service overview", operator: true,
		about: "Operator only: people, sites, sizes, sign-ups, reports, network use and API growth.", input: in("range", str("7d, 14d, 30d or 6m.")),
		output: oObj, readOnly: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			return "GET", "/v1/admin/overview?" + url.Values{"range": {c.str("range")}}.Encode(), nil, nil
		}},
	{name: "set_hold", title: "Take down or suspend", operator: true,
		about: "Operator only: take a site down or restore it (site, taken_down), or suspend or restore an account (person, suspended). Ask first.",
		input: in("site", str("A site's name or address."), "owner", str("Its owner's handle."), "taken_down", boolean("Take it down, or restore it."),
			"person", str("An account's id."), "suspended", boolean("Suspend it, or restore it."), "reason", str("Shown to the owner.")),
		output: oObj, destructive: true, idempotent: true,
		route: func(c *call) (string, string, any, error) {
			if c.str("person") != "" {
				return "PATCH", "/v1/admin/people/" + url.PathEscape(c.str("person")), c.pick("suspended", "reason"), nil
			}
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("taken_down", "reason"), err
		}},
}

func fixed(method, path string) func(*call) (string, string, any, error) {
	return func(*call) (string, string, any, error) { return method, path, map[string]any{}, nil }
}

func escPath(p string) string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// The saved-data routes of the call's site and namespace.
func (c *call) rows(rest string, q url.Values) (string, error) {
	return c.site("/storage/sqlite/"+url.PathEscape(c.str("namespace"))+"/tables/"+url.PathEscape(c.str("table"))+"/rows"+rest, q)
}
func (c *call) values(rest string, q url.Values) (string, error) {
	return c.site("/storage/kv/"+url.PathEscape(c.str("namespace"))+"/keys"+rest, q)
}
func (c *call) files(rest string, q url.Values) (string, error) {
	return c.site("/storage/files/"+url.PathEscape(c.str("namespace"))+"/objects"+rest, q)
}
func (c *call) sql(which string) (string, error) {
	return c.site("/storage/sqlite/"+url.PathEscape(c.str("namespace"))+"/"+which, nil)
}

// obj is an object argument ({} when missing).
func (c *call) obj(k string) map[string]any {
	if m, ok := c.args[k].(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// whoAmI is GET /v1/me as the AI sees it: no internal ids, and emails only
// when asked for.
func whoAmI(c *call) (string, string, any, error) {
	_, v, err := c.h.do(c.r, http.MethodGet, "/v1/me", nil)
	if err != nil {
		return "", "", nil, err
	}
	me, _ := v.(map[string]any)
	withEmail := c.args["include_email"] == true
	names := func(k string, keep ...string) []any {
		l := []any{}
		for _, x := range anyList(me[k]) {
			m, _ := x.(map[string]any)
			o := map[string]any{"name": m["name"]}
			for _, f := range keep {
				if m[f] != nil {
					o[f] = m[f]
				}
			}
			l = append(l, o)
		}
		return l
	}
	teams := []any{}
	for _, t := range anyList(me["teams"]) {
		t, _ := t.(map[string]any)
		members, invited := anyList(t["members"]), anyList(t["invites"])
		o := map[string]any{"handle": t["handle"], "display_name": t["display_name"], "members": len(members), "invited": len(invited)}
		if withEmail {
			o["member_emails"], o["invited_emails"] = field(members, "email"), field(invited, "email")
		}
		teams = append(teams, o)
	}
	invitations := []any{}
	for _, i := range anyList(me["invitations"]) {
		i, _ := i.(map[string]any)
		invitations = append(invitations, map[string]any{"team": i["team"], "display_name": i["display_name"]})
	}
	email := ""
	if withEmail {
		email, _ = me["email"].(string)
	}
	domain, _ := me["domain"].(string)
	return "", "", map[string]any{"email": email, "handle": me["handle"], "display_name": me["display_name"], "operator": me["operator"] == true,
		"home_site": me["home_site"], "domain": domain, "teams": teams, "invitations": invitations,
		"keys": names("keys", "created_at", "last_used_at"), "apps": names("apps", "connected_at"), "devices": names("devices", "created_at"), "use": me["use"]}, nil
}

func anyList(v any) []any { l, _ := v.([]any); return l }

func field(l []any, k string) []any {
	out := []any{}
	for _, x := range l {
		if m, ok := x.(map[string]any); ok {
			out = append(out, m[k])
		}
	}
	return out
}

// acceptInvite finds the invitation to the team named (or takes the old
// tools' invitation id) and accepts it.
func acceptInvite(c *call) (string, string, any, error) {
	id := c.str("id") // next/compat only
	if id == "" {
		_, v, err := c.h.do(c.r, http.MethodGet, "/v1/me", nil)
		if err != nil {
			return "", "", nil, err
		}
		me, _ := v.(map[string]any)
		for _, i := range anyList(me["invitations"]) {
			if i, _ := i.(map[string]any); strings.EqualFold(stringOf(i["team"]), c.str("team")) {
				id = stringOf(i["id"])
			}
		}
		if id == "" {
			return "", "", nil, web.Refuse(http.StatusNotFound, "no_invitation", "No invitation to the team "+c.str("team")+" is waiting. who_am_i lists the invitations.")
		}
	}
	return "POST", "/v1/invites/" + url.PathEscape(id) + "/accept", map[string]any{}, nil
}

// removeFromTeam finds the member by email ("me": the caller) in the team,
// or takes the old tools' person id.
func removeFromTeam(c *call) (string, string, any, error) {
	team, person := c.str("team"), c.str("person") // person: next/compat only
	if person == "" {
		_, v, err := c.h.do(c.r, http.MethodGet, "/v1/me", nil)
		if err != nil {
			return "", "", nil, err
		}
		me, _ := v.(map[string]any)
		email := c.str("email")
		if strings.EqualFold(email, "me") {
			email = stringOf(me["email"])
		}
		for _, t := range anyList(me["teams"]) {
			if t, _ := t.(map[string]any); strings.EqualFold(stringOf(t["handle"]), team) {
				for _, m := range anyList(t["members"]) {
					if m, _ := m.(map[string]any); strings.EqualFold(stringOf(m["email"]), email) {
						person = stringOf(m["id"])
					}
				}
			}
		}
		if person == "" {
			return "", "", nil, web.Refuse(http.StatusNotFound, "not_member", "Nobody with that email is a member of "+team+". who_am_i with include_email lists the members.")
		}
	}
	return "DELETE", "/v1/teams/" + url.PathEscape(team) + "/members/" + url.PathEscape(person), nil, nil
}

func stringOf(v any) string { s, _ := v.(string); return s }
