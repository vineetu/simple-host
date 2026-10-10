package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// storageTools adapt the Simple Host storage REST API. The REST handler owns
// resource policy, visitor identity, SQL authorization, quotas and validation.
// These tools run as the site owner through the caller's existing credential;
// visitor access remains on the site's own origin through REST.
func storageTools() []Tool {
	return storageToolsFor(false)
}

func eventStorageTools() []Tool {
	return storageToolsFor(true)
}

func storageToolsFor(event bool) []Tool {
	type route struct {
		name, title, description, method, suffix string
		args                                     []string
		body                                     string
		annotation                               map[string]any
	}
	routes := []route{
		{"storage_list_resources", "List storage resources", "List a site's saved-data resources (KV, SQLite, files) with each one's name, kind, preset, and access (who may read, add, edit, and delete), plus each SQLite table's own preset. preset legacy marks a resource saved before presets that keeps its old rules until you set a preset on it. Call it before reading or changing saved data, to get the exact resource name.", "GET", "/resources", nil, "", readOnly()},
		{"storage_get_usage", "Read site storage usage", "Show how much of a site's saved-data allowance is used: on Simple Host, KV and SQLite together (10,000,000 bytes) and files separately (10 MB); on Simple Hack, one 1,000,000-byte pool for all three. The site's own pages and images are counted elsewhere (list_sites).", "GET", "/usage", nil, "", readOnly()},
		{"storage_set_resource", "Create a storage resource or set its preset", storageSetResourceDescription, "PUT", "/resources/{name}", []string{"name"}, "resource", writes(true, true, true)},
		{"storage_delete_resource", "Delete storage resource", "Remove a resource and everything saved in it, for good. Ask the person to confirm this exact resource first.", "DELETE", "/resources/{name}", []string{"name"}, "", writes(true, true, false)},
		{"storage_list_kv_keys", "List KV keys", "List the keys in a KV resource, optionally only those starting with a prefix.", "GET", "/kv/{name}/keys", []string{"name"}, "", readOnly()},
		{"storage_get_kv", "Read KV value", "Read one key's JSON value from a KV resource. The answer is {key, value}.", "GET", "/kv/{name}/keys/{key}", []string{"name", "key"}, "", readOnly()},
		{"storage_put_kv", "Set KV value", "Set one key in a KV resource to a JSON value, replacing what was there. This is how the owner changes page info a page reads live (a menu, prices, opening hours, settings) without republishing the site; the page reads it with SH.storage.kv(name).get(key). Ask before overwriting a value visitors wrote.", "PUT", "/kv/{name}/keys/{key}", []string{"name", "key"}, "value", writes(true, true, true)},
		{"storage_delete_kv", "Delete KV value", "Delete one key and its value from a KV resource. Ask first.", "DELETE", "/kv/{name}/keys/{key}", []string{"name", "key"}, "", writes(true, true, false)},
		{"storage_sql_query", "Query SQLite resource", "Read rows from a SQLite resource with a SELECT, as the owner: it sees every person's rows, whatever the preset. Read-only; put values in params with ? placeholders. Use it to show the person their orders, entries, or messages.", "POST", "/sqlite/{name}/query", []string{"name"}, "sql", readOnly()},
		{"storage_sql_execute", "Execute SQLite statement", "Change rows in a SQLite resource as the owner: INSERT, UPDATE, or DELETE with ? placeholders, for example setting an order's status. Ask before changing or deleting what visitors sent.", "POST", "/sqlite/{name}/execute", []string{"name"}, "sql", writes(true, false, true)},
		{"storage_sql_schema", "Change SQLite schema", "Create or change tables in a SQLite resource: CREATE TABLE, CREATE INDEX, CREATE TRIGGER, ALTER TABLE, one statement per call, with IF NOT EXISTS so a rerun is harmless. Use id INTEGER PRIMARY KEY and a created_at TEXT column (and updated_at TEXT where rows are edited); the server fills them. Do not declare visitor_id: on every table whose preset uses own or lets visitors add (inbox, wall, records, personal, board) the server adds an indexed visitor_id column itself. A foreign key with ON DELETE CASCADE stops pages deleting the parent row, since that would change another table; leave cascades out of tables pages delete from. Ask before a change that drops or reshapes data.", "POST", "/sqlite/{name}/schema", []string{"name"}, "sql", writes(true, false, false)},
		{"storage_list_file_objects", "List stored files", "List the files in a files resource, optionally only those under a path prefix.", "GET", "/files/{name}/objects", []string{"name"}, "", readOnly()},
		{"storage_put_file", "Upload stored file", "Upload one file into a files resource as the owner (base64, under 1 MiB; resize photos first): a gallery photo, a menu PDF, a download. A page shows it with SH.storage.files(name).url(path). Images that are part of the page's design go in create_site or update_site files_base64 instead. Ask before replacing an existing file.", "PUT", "/files/{name}/objects/{path...}", []string{"name", "path"}, "file", writes(true, true, true)},
		{"storage_delete_file", "Delete stored file", "Delete one file from a files resource. Ask first.", "DELETE", "/files/{name}/objects/{path...}", []string{"name", "path"}, "", writes(true, true, false)},
		{"storage_file_download_link", "Get stored file download link", "Make a private link, good for ten minutes, to download one stored file in full, for example a photo a visitor uploaded, so the person can open it. Do not post it anywhere public.", "POST", "/files/{name}/download-link", []string{"name", "path"}, "link", writes(false, false, false)},
	}
	tools := make([]Tool, 0, len(routes))
	for _, route := range routes {
		r := route
		siteField := "site"
		siteDescription := "The site's name, as in its address (list_sites shows the exact names)."
		if event {
			siteField = "event"
			siteDescription = "The event's name (slug) on Simple Hack."
			r.name = "hack_event_" + r.name
			r.title = "Event website: " + r.title
			r.description = strings.ReplaceAll(r.description, "storage_", "hack_event_storage_")
			r.description = strings.Replace(r.description, storageOwnerSentence, hackEventOwnerSentence, 1)
			r.description += " Organiser only on this event's custom website."
		}
		props := map[string]any{siteField: str(siteDescription)}
		required := []string{siteField}
		argDescriptions := map[string]string{
			"name": "The resource's name (storage_list_resources shows the names; storage_set_resource creates one).",
			"key":  "The key within the KV resource, e.g. menu or settings.",
			"path": "The file's path within the files resource, e.g. photos/cake.webp (no leading slash).",
		}
		for _, arg := range r.args {
			props[arg] = str(argDescriptions[arg])
			required = append(required, arg)
		}
		if strings.HasSuffix(r.name, "storage_list_kv_keys") || strings.HasSuffix(r.name, "storage_list_file_objects") {
			props["prefix"] = str("Optional: only keys or paths that start with this.")
			props["after"] = str("Optional: the next_after value from the previous answer, to get the next page.")
			props["limit"] = map[string]any{"type": "integer", "description": "Optional: at most this many results (the server caps it)."}
		}
		switch r.body {
		case "resource":
			props["body"] = storageResourceBody()
			required = append(required, "body")
		case "value":
			props["value"] = anyJSON("The JSON value to store: a string, number, object, array, true, false, or null.")
			required = append(required, "value")
		case "sql":
			props["sql"] = str("One SQLite statement, with ? placeholders for values.")
			props["params"] = map[string]any{"type": "array", "items": anyJSON("One value for a ? placeholder."), "description": "Values for the ? placeholders, in order. Omit or [] when there are none."}
			required = append(required, "sql")
		case "file":
			props["content_base64"] = str("The whole file in standard base64, at most 1 MiB decoded.")
			props["content_type"] = str("The file's MIME type, e.g. image/webp, image/jpeg, or application/pdf.")
			required = append(required, "content_base64", "content_type")
		}
		tool := Tool{
			Name: r.name, Title: r.title, Description: r.description,
			InputSchema:  object(props, required...),
			OutputSchema: outObject(map[string]any{"response": anyJSON("Unchanged REST JSON response."), "status": outInteger("REST status code.")}, "response", "status"),
			Annotations:  r.annotation,
		}
		tool.run = func(c *call, args map[string]any) (output, error) {
			return runStorageRoute(c, r.method, r.suffix, r.name, r.args, r.body, args, event)
		}
		tools = append(tools, tool)
	}
	return tools
}

func runStorageRoute(c *call, method, suffix, toolName string, pathArgs []string, bodyKind string, args map[string]any, event bool) (output, error) {
	var site string
	var err error
	if event {
		site, err = stringArg(args, "event")
	} else {
		site, err = siteArg(args)
	}
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(site) + "/storage" + suffix
	if event {
		path = "/v1/hack/events/" + url.PathEscape(site) + "/website/storage" + suffix
	}
	for _, name := range pathArgs {
		value, err := stringArg(args, name)
		if err != nil {
			return output{}, err
		}
		escaped := url.PathEscape(value)
		if name == "path" {
			segments := strings.Split(value, "/")
			for i := range segments {
				segments[i] = url.PathEscape(segments[i])
			}
			escaped = strings.Join(segments, "/")
		}
		path = strings.Replace(path, "{"+name+"...}", escaped, 1)
		path = strings.Replace(path, "{"+name+"}", escaped, 1)
	}
	if strings.HasSuffix(toolName, "storage_list_kv_keys") || strings.HasSuffix(toolName, "storage_list_file_objects") {
		query := url.Values{}
		for _, name := range []string{"prefix", "after"} {
			value, err := optionalString(args, name)
			if err != nil {
				return output{}, err
			}
			if value != "" {
				query.Set(name, value)
			}
		}
		if rawLimit, ok := args["limit"]; ok && rawLimit != nil {
			query.Set("limit", fmt.Sprint(rawLimit))
		}
		if len(query) > 0 {
			path += "?" + query.Encode()
		}
	}
	var raw []byte
	var headers map[string]string
	switch bodyKind {
	case "resource":
		body, ok := args["body"].(map[string]any)
		if !ok {
			return output{}, fmt.Errorf("body must be a JSON object")
		}
		raw, err = json.Marshal(body)
	case "value":
		value, ok := args["value"]
		if !ok {
			return output{}, fmt.Errorf("value is required")
		}
		raw, err = json.Marshal(map[string]any{"value": value})
	case "sql":
		sql, e := stringArg(args, "sql")
		if e != nil {
			return output{}, e
		}
		body := map[string]any{"sql": sql}
		if params, ok := args["params"]; ok {
			body["params"] = params
		}
		raw, err = json.Marshal(body)
	case "link":
		filePath, e := stringArg(args, "path")
		if e != nil {
			return output{}, e
		}
		raw, err = json.Marshal(map[string]string{"path": filePath})
	case "file":
		encoded, e := stringArg(args, "content_base64")
		if e != nil {
			return output{}, e
		}
		if len(encoded) > base64.StdEncoding.EncodedLen(1<<20) {
			return output{}, fmt.Errorf("file exceeds this tool's 1 MiB limit; use direct REST")
		}
		raw, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return output{}, fmt.Errorf("content_base64: %w", err)
		}
		if len(raw) > 1<<20 {
			return output{}, fmt.Errorf("file exceeds this tool's 1 MiB limit; use direct REST")
		}
		contentType, e := stringArg(args, "content_type")
		if e != nil {
			return output{}, e
		}
		headers = map[string]string{"Content-Type": contentType}
	}
	if err != nil {
		return output{}, err
	}
	res := c.do(method, path, raw, headers)
	if !res.ok() {
		return output{}, restError(toolName, res)
	}
	var response any
	if len(res.body) > 0 {
		if err := json.Unmarshal(res.body, &response); err != nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON: %w", toolName, err)
		}
	}
	structured := map[string]any{"response": response, "status": res.status}
	return output{Text: jsonText(structured), Structured: structured}, nil
}

const storageSetResourceDescription = "Create a saved-data resource on a site, or set who may read, add, edit, and delete in it. kind: kv (one JSON value per key), sqlite (tables), or files (uploads); it cannot change later. Pick a preset; the server enforces it on every request from the site's pages, whatever the page code does. " +
	"The presets (read / add / edit / delete): " +
	"public (anyone / owner / owner / owner): menus, catalogues, price lists, opening hours, schedules, FAQs, a gallery you fill. " +
	"inbox (owner / anyone / owner / owner): contact forms, feedback, surveys, quote requests, sign-ups; people send, only you read. " +
	"wall (anyone / signed-in / owner / own): guestbooks, comments, reviews, a public RSVP or who's-coming list; authors can take back their own. " +
	"records (own / signed-in / owner / owner): orders, bookings, appointments, applications, support tickets; each person sees only their own, you see all and set a status. " +
	"personal (own / signed-in / own / own): profiles, saved settings, wishlists, notes, bookmarks, a cart that follows the person. " +
	"board (signed-in / signed-in / signed-in / owner): potluck and sign-up sheets, shared task or shopping lists, a club roster; everyone signed in edits, only you delete. " +
	"private (all owner, the default): admin data, inventory, internal notes, drafts. " +
	"Lookalikes: a sign-up sheet people change is board, but one where each person adds and removes only their own entry is wall; a form only you read is inbox, but one where each person sees their own submissions is records; a wishlist only its owner sees is personal, but a wishlist others may view is wall; a gallery you fill is public on files, one visitors add to is wall on files. " +
	storageOwnerSentence +
	"For sqlite, the preset is the database default and tables can each have their own: {\"kind\":\"sqlite\",\"preset\":\"private\",\"tables\":{\"products\":{\"preset\":\"public\"},\"orders\":{\"preset\":\"records\"}}}. " +
	"A preset takes single-action overrides, checked by the server, e.g. {\"preset\":\"inbox\",\"add\":\"signed-in\"}; preset custom sets all four. The server refuses: edit or delete anyone; add own; own anywhere with add anyone; edit or delete wider than read. " +
	"Create the resource, and its tables with storage_sql_schema, before publishing the page that uses it; get_page_recipe has a page per preset. Ask the person before making data readable or changeable by more people."

// Who "owner" is, in storage_set_resource: on Simple Host the site's owner;
// on Simple Hack the team or the event's organisers (owner decision
// 2026-10-10).
const storageOwnerSentence = "\"own\" means the signed-in person who added the entry; \"owner\" means you: these tools, and you signed in on the site's own address through its sign-in box, where a page you write can act as an admin page (all orders, set a status). \"nobody\" keeps data to these tools only. "

const hackTeamOwnerSentence = "\"own\" means the signed-in person who added the entry; \"owner\" means the team: these tools with the selected team, and a team member signed in on the team site's own address with the email they use on Simple Hack, where a page can act as an admin page (all orders, set a status). That stops at the team's submission deadline, with the team's other changes; after it a member there is an ordinary visitor. \"nobody\" keeps data to these tools only. "

const hackEventOwnerSentence = "\"own\" means the signed-in person who added the entry; \"owner\" means the event's organisers: these hack_event_storage_* tools, and an organiser signed in on the event website's own address with the email they use on Simple Hack, where a page can act as an admin page (all questions, mark one answered). That stops when the event ends. \"nobody\" keeps data to these tools only. "

func storageWhoSchema(description string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": description}
}

func storageResourceBody() map[string]any {
	who := []string{"nobody", "owner", "own", "signed-in", "anyone"}
	presets := []string{"public", "inbox", "wall", "records", "personal", "board", "private", "custom"}
	table := object(map[string]any{
		"preset": storageWhoSchema("This table's preset.", presets...),
		"read":   storageWhoSchema("Optional override for read.", who...),
		"add":    storageWhoSchema("Optional override for add.", who...),
		"edit":   storageWhoSchema("Optional override for edit.", who...),
		"delete": storageWhoSchema("Optional override for delete.", who...),
	}, "preset")
	body := object(map[string]any{
		"kind":          map[string]any{"type": "string", "enum": []string{"kv", "sqlite", "files"}, "description": "kv (one JSON value per key), sqlite (tables), or files (uploads). Cannot change once created."},
		"preset":        storageWhoSchema("public, inbox, wall, records, personal, board, or private (the default); custom with all four actions set.", presets...),
		"read":          storageWhoSchema("Optional override: who may list and view.", who...),
		"add":           storageWhoSchema("Optional override: who may add a new entry (never own).", who...),
		"edit":          storageWhoSchema("Optional override: who may change an existing entry (never anyone).", who...),
		"delete":        storageWhoSchema("Optional override: who may remove an entry (never anyone).", who...),
		"tables":        map[string]any{"type": "object", "additionalProperties": table, "description": "sqlite only: a preset per table, e.g. {\"products\": {\"preset\": \"public\"}, \"orders\": {\"preset\": \"records\"}}. Tables not listed use the resource's preset. Omit to keep the current table presets; {} clears them."},
		"site_passcode": map[string]any{"type": "string", "enum": []string{"inherit", "off"}, "description": "inherit (default): when the site has a passcode, visitors must have entered it before reading or writing here. off: public reads skip the passcode."},
	}, "kind")
	body["description"] = "The resource's kind and preset, e.g. {\"kind\": \"sqlite\", \"preset\": \"records\"}."
	return body
}
