package mcp

import (
	"net/http"
	"net/url"
	"strings"

	"simplehost/core/web"
)

// tool is one row of the table: what the AI app sees, and the route the
// call replays with the caller's identity. A route func returns the method,
// path and JSON body; method "" means the answer is body itself (an in-chat
// screen opens instead of a call).
type tool struct {
	name, about string
	input       map[string]any
	readOnly    bool
	asks        bool   // deletes or widens access: the app asks the person first
	screen      string // the in-chat screen shown with the result
	route       func(c *call) (method, path string, body any, err error)
}

// Schema helpers.
func str(d string) map[string]any     { return map[string]any{"type": "string", "description": d} }
func boolean(d string) map[string]any { return map[string]any{"type": "boolean", "description": d} }
func integer(d string) map[string]any { return map[string]any{"type": "integer", "description": d} }
func object(d string) map[string]any  { return map[string]any{"type": "object", "description": d} }
func list(d string, item map[string]any) map[string]any {
	return map[string]any{"type": "array", "description": d, "items": item}
}

// in is an input schema: name, schema pairs; names ending in "!" are required.
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

var emails = list("Email addresses.", str("An email address."))

var tools = []tool{
	// Account
	{name: "who_am_i", about: "Me: email, handle, teams and invitations, key names, connected apps and use against the limits.", input: in(), readOnly: true,
		route: fixed("GET", "/v1/me")},
	{name: "update_account", about: "Change the handle, display name, home site (a site shown at the handle's own address) or domain for all sites, of me or a team. Ask first before changing a handle: old links stop working and the old handle can never be used again.",
		input: in("owner", str(`"me" (default) or a team handle.`), "handle", str("New handle."), "display_name", str("Display name."),
			"home_site", str(`One of the owner's sites, or "" for the list of listed sites.`), "domain", str(`A domain for all sites, like example.com; "" removes it.`)), asks: true,
		route: func(c *call) (string, string, any, error) {
			return "PATCH", "/v1/owners/" + url.PathEscape(c.or("owner", "me")), c.pick("handle", "display_name", "home_site", "domain"), nil
		}},
	{name: "download_my_data", about: "Queue a download of everything: every site, its files and saved data, and account details. The link comes by email.", input: in(),
		route: fixed("POST", "/v1/me/export")},
	{name: "delete_account", about: "Delete my account: every site, its data, keys and app connections, at once. Cannot be undone. Ask first.", input: in(), asks: true,
		route: fixed("DELETE", "/v1/me")},
	// Teams
	{name: "create_team", about: "Make a team. It has its own handle, so its sites have their own addresses.", input: in("handle!", str("The team's handle."), "display_name", str("Its display name.")),
		route: func(c *call) (string, string, any, error) {
			return "POST", "/v1/teams", c.pick("handle", "display_name"), nil
		}},
	{name: "invite_to_team", about: "Invite someone by email. Nobody is a member until they accept.", input: in("team!", str("The team's handle."), "email!", str("Their email.")),
		route: func(c *call) (string, string, any, error) {
			return "POST", "/v1/teams/" + url.PathEscape(c.str("team")) + "/invites", c.pick("email"), nil
		}},
	{name: "accept_invite", about: "Accept an invitation (its id is in who_am_i).", input: in("id!", str("The invitation's id.")),
		route: func(c *call) (string, string, any, error) {
			return "POST", "/v1/invites/" + url.PathEscape(c.str("id")) + "/accept", map[string]any{}, nil
		}},
	{name: "remove_from_team", about: "Remove a member from a team, or leave it. Ask first.", input: in("team!", str("The team's handle."), "person!", str("The member's id (from who_am_i).")), asks: true,
		route: func(c *call) (string, string, any, error) {
			return "DELETE", "/v1/teams/" + url.PathEscape(c.str("team")) + "/members/" + url.PathEscape(c.str("person")), nil, nil
		}},
	{name: "delete_team", about: "Delete a team that has no sites. Ask first.", input: in("team!", str("The team's handle.")), asks: true,
		route: func(c *call) (string, string, any, error) {
			return "DELETE", "/v1/teams/" + url.PathEscape(c.str("team")), nil, nil
		}},
	// Sites
	{name: "list_sites", about: "My sites and my teams' sites, most recently changed first.", readOnly: true,
		input: in("q", str("Text to search site names for."), "bin", boolean("List the bin (deleted in the last 7 days) instead.")),
		route: func(c *call) (string, string, any, error) {
			q := url.Values{"q": {c.str("q")}}
			if c.args["bin"] == true {
				q.Set("bin", "true")
			}
			return "GET", "/v1/sites?" + q.Encode(), nil, nil
		}},
	{name: "get_site", about: "Everything about one site: address, versions, who can open it, domain status, saved data, rules, notifications and use.", input: siteIn(), readOnly: true,
		route: func(c *call) (string, string, any, error) { p, err := c.site("", nil); return "GET", p, nil, err }},
	{name: "publish_site", about: "Publish files and get the live address back. Makes the site if it is new. With keep_other_files, the live files not sent stay.",
		input: siteIn("files", object("Path -> text content, like {\"index.html\": \"...\"}."), "files_base64", object("Path -> base64 content, for images and other binary files."),
			"keep_other_files", boolean("Keep the live files not sent (to change a few files).")),
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", url.Values{"keep_other_files": {c.str("keep_other_files")}})
			return "PUT", p, c.pick("files", "files_base64"), err
		}},
	{name: "create_upload_link", about: "With a namespace: the photo and file picker, shown in the chat (photos are shrunk on the device), saving to that namespace's file store. Without: a one-time link for a big site as one archive.",
		input: siteIn("namespace", str("Where photos and files go, like photos."), "kind", str(`"files" or "archive"; follows from namespace when left out.`)), screen: "photos",
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("/upload-links", nil)
			kind := c.or("kind", "archive")
			if c.str("namespace") != "" && c.str("kind") == "" {
				kind = "files"
			}
			return "POST", p, map[string]any{"kind": kind, "namespace": c.str("namespace")}, err
		}},
	{name: "read_site_file", about: "Read one published file of any site you may open.", input: siteIn("path!", str("The file's path, like index.html.")), readOnly: true,
		route: func(c *call) (string, string, any, error) {
			parts := strings.Split(strings.TrimPrefix(c.str("path"), "/"), "/")
			for i := range parts {
				parts[i] = url.PathEscape(parts[i])
			}
			p, err := c.site("/files/"+strings.Join(parts, "/"), nil)
			return "GET", p, nil, err
		}},
	{name: "change_site", about: "Take a site offline or back online, list it on my public page, make an older version live, or restore it from the bin. Ask first.", asks: true,
		input: siteIn("offline", boolean("Offline shows a plain offline page; everything is kept."), "listed", boolean("Show it on the owner's public page."),
			"live_version", integer("A kept version to make live."), "restore", boolean("Bring it back from the bin.")),
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("offline", "listed", "live_version", "restore"), err
		}},
	{name: "delete_site", about: "Move a site to the bin, with its data. It can be restored for 7 days. Ask first.", input: siteIn(), asks: true,
		route: func(c *call) (string, string, any, error) { p, err := c.site("", nil); return "DELETE", p, nil, err }},
	{name: "set_site_access", about: "Who may open the site: open, a passcode, or people named by email; or sign everyone out. For a passcode, call with access \"passcode\" and no passcode: the person types it in the screen shown, so it never enters the chat. Ask first.",
		input: siteIn("access", str(`"open", "passcode" or "viewers".`), "passcode", str("Only from the passcode screen."), "viewers", emails,
			"viewers_add", emails, "viewers_remove", emails, "sign_everyone_out", boolean("End every unlock and visitor sign-in.")), asks: true, screen: "passcode",
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			if c.str("access") == "passcode" && c.str("passcode") == "" && err == nil {
				return "", "", map[string]any{"site": c.str("site"), "owner": c.str("owner"),
					"says": "The passcode screen is open in the chat. The person sets the passcode there; it never enters the chat."}, nil
			}
			return "PATCH", p, c.pick("access", "passcode", "viewers", "viewers_add", "viewers_remove", "sign_everyone_out"), err
		}},
	{name: "set_site_address", about: "Connect the site's own domain (answers with the DNS records to add and what is missing), or give it a free short name (<name>.simple-host.app, live at once). \"\" removes either. Ask first.", asks: true,
		input: siteIn("domain", str("Like shop.example.com."), "short_name", str("Like bakery.")),
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("domain", "short_name"), err
		}},
	{name: "site_stats", about: "Visit numbers: visits per day, top pages, referrers and countries. Shown in the chat.", input: siteIn("days", integer("Days back, 1 to 365 (30).")), readOnly: true, screen: "stats",
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("/stats", url.Values{"days": {c.str("days")}})
			return "GET", p, nil, err
		}},
	{name: "set_notifications", about: "What the owner is told about on this site, in the AI app and by browser push: data changes in a table, _values or _files, or someone asking for access. An empty list turns them off.",
		input: siteIn("notifications!", list(`Like {"kind": "data", "namespace": "bookings", "target": "bookings"} or {"kind": "access"}.`, object("One notification."))),
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("", nil)
			return "PATCH", p, c.pick("notifications"), err
		}},
	// Saved data
	{name: "data", about: "Rows, values and stored files of any site you may open, with your rights (the routes pages use). path is under the site, like storage/sqlite/bookings/tables/bookings/rows, storage/kv/menu/keys/today, storage/files/photos/objects.",
		input: siteIn("method", str("GET (default), POST, PATCH, PUT or DELETE."), "path!", str("Starts with storage/."), "body", object("The JSON body, for POST, PATCH and PUT.")),
		route: func(c *call) (string, string, any, error) {
			rest, query, _ := strings.Cut(c.str("path"), "?")
			if !strings.HasPrefix(rest, "storage/") {
				return "", "", nil, web.Refuse(http.StatusBadRequest, "bad_path", "Give a path that starts with storage/, like storage/sqlite/<namespace>/tables/<table>/rows.")
			}
			q, _ := url.ParseQuery(query)
			p, err := c.site("/"+rest, q)
			return strings.ToUpper(c.or("method", "GET")), p, c.args["body"], err
		}},
	{name: "run_sql", about: "The owner's SQL on a namespace of their site: any statement (made if new). Without sql: the tables and space used. Values are in _values, stored files' records in _files.",
		input: siteIn("namespace!", str("Like bookings."), "sql", str("One statement."), "params", list("Values for ? in order.", map[string]any{})),
		route: func(c *call) (string, string, any, error) {
			word, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(c.str("sql"))), " ")
			which := map[string]string{"": "schema", "select": "query", "with": "query", "pragma": "query", "explain": "query", "values": "query"}[word]
			if which == "" {
				which = "execute"
			}
			p, err := c.site("/storage/sqlite/"+url.PathEscape(c.str("namespace"))+"/"+which, nil)
			return "POST", p, c.pick("sql", "params"), err
		}},
	{name: "set_data_rules", about: "Who may read, add, change and delete each table, _values and _files of a namespace. No rules: only the owner. Ask first when it widens who can read or write.",
		input: siteIn("namespace!", str("Like bookings."), "rules!", object("Table -> rule set, as in the recipes."), "spam_check", boolean("Anonymous adds must pass the spam check (auth.checked).")), asks: true,
		route: func(c *call) (string, string, any, error) {
			p, err := c.site("/storage/"+url.PathEscape(c.str("namespace"))+"/rules", nil)
			return "PUT", p, c.pick("rules", "spam_check"), err
		}},
	{name: "get_recipe", about: "A page recipe: the data setup, a working page, and who can see what. Topics: inbox (contact, quote, sign-up forms), public (menu, prices), personal (bookings), comments (guestbook), portal (per-client pages), gallery (visitors' photos).",
		input: in("topic!", str("inbox, public, personal, comments, portal or gallery."), "site", str("The site's name, filled into the recipe.")), readOnly: true,
		route: func(c *call) (string, string, any, error) {
			return "GET", "/v1/recipes/" + url.PathEscape(c.str("topic")) + "?" + url.Values{"site": {c.str("site")}}.Encode(), nil, nil
		}},
	// Operator
	{name: "admin_overview", about: "Operator only: people, sites, sizes, sign-ups, reports, network use and API growth.", input: in("range", str("7d, 14d, 30d or 6m.")), readOnly: true,
		route: func(c *call) (string, string, any, error) {
			return "GET", "/v1/admin/overview?" + url.Values{"range": {c.str("range")}}.Encode(), nil, nil
		}},
	{name: "set_hold", about: "Operator only: take a site down or restore it (site, taken_down), or suspend or restore an account (person, suspended). Ask first.", asks: true,
		input: in("site", str("A site's name or address."), "owner", str("Its owner's handle."), "taken_down", boolean("Take it down, or restore it."),
			"person", str("An account's id."), "suspended", boolean("Suspend it, or restore it."), "reason", str("Shown to the owner.")),
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
