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
	type route struct {
		name, title, description, method, suffix string
		args                                     []string
		body                                     string
		annotation                               map[string]any
		policy                                   toolPolicy
	}
	routes := []route{
		{name: "storage_list_resources", title: "List storage resources", description: storageRulesListDescription, method: "GET", suffix: "/resources", args: nil, body: "", annotation: readOnly()},
		{name: "storage_get_usage", title: "Read site storage usage", description: "Show how much of a site's saved-data allowance is used: KV and SQLite together (10,000,000 bytes) and files separately (10 MB). The site's own pages and images are counted elsewhere (list_sites).", method: "GET", suffix: "/usage", args: nil, body: "", annotation: readOnly()},
		{policy: toolPolicy{noSpamCheck: withoutSpamCheck}, name: "storage_set_resource", title: "Create a storage resource or set its rules", description: storageRulesSetDescription + storageRulesSpamCheckSentence, method: "PUT", suffix: "/resources/{name}", args: []string{"name"}, body: "resource", annotation: writes(true, true, true)},
		{name: "storage_delete_resource", title: "Delete storage resource", description: "Remove a resource and everything saved in it, for good. Ask the person to confirm this exact resource first.", method: "DELETE", suffix: "/resources/{name}", args: []string{"name"}, body: "", annotation: writes(true, true, true)},
		{policy: toolPolicy{pagedStorage: true, visitorResult: "items"}, name: "storage_list_kv_keys", title: "List KV keys", description: "List the keys in a KV resource, optionally only those starting with a prefix.", method: "GET", suffix: "/kv/{name}/keys", args: []string{"name"}, body: "", annotation: readOnly()},
		{policy: toolPolicy{visitorResult: "item"}, name: "storage_get_kv", title: "Read KV value", description: "Read one key's JSON value from a KV resource. The answer is {key, value}.", method: "GET", suffix: "/kv/{name}/keys/{key}", args: []string{"name", "key"}, body: "", annotation: readOnly()},
		{name: "storage_put_kv", title: "Set KV value", description: "Set one key in a KV resource to a JSON value, replacing what was there. It changes a page only when the page's code already reads that key (SH.storage.kv(name).get(key)); text written in the page itself is changed with update_site (edits), so read the page first. This is how the owner changes page info a page reads live (a menu, prices, opening hours, settings) without republishing the site. Ask before overwriting a value visitors wrote.", method: "PUT", suffix: "/kv/{name}/keys/{key}", args: []string{"name", "key"}, body: "value", annotation: writes(true, true, true)},
		{name: "storage_delete_kv", title: "Delete KV value", description: "Delete one key and its value from a KV resource. Ask first.", method: "DELETE", suffix: "/kv/{name}/keys/{key}", args: []string{"name", "key"}, body: "", annotation: writes(true, true, true)},
		{policy: toolPolicy{visitorParams: true, visitorResult: "rows"}, name: "storage_sql_query", title: "Query SQLite resource", description: storageRulesQueryDescription, method: "POST", suffix: "/sqlite/{name}/query", args: []string{"name"}, body: "sql", annotation: readOnly()},
		{policy: toolPolicy{visitorParams: true}, name: "storage_sql_execute", title: "Execute SQLite statement", description: "Change rows in a SQLite resource as the owner: INSERT, UPDATE, or DELETE with ? placeholders, for example setting an order's status; on Simple Host, visitor_id values from results can be passed back as parameters. Ask before changing or deleting what visitors sent. Repeating a successful statement can add duplicate rows or apply a change again, so query the affected records before retrying after an uncertain result. " + sqlNoPragmaHint, method: "POST", suffix: "/sqlite/{name}/execute", args: []string{"name"}, body: "sql", annotation: writes(true, false, true)},
		{name: "storage_sql_schema", title: "Change SQLite schema", description: storageRulesSchemaDescription, method: "POST", suffix: "/sqlite/{name}/schema", args: []string{"name"}, body: "sql", annotation: writes(true, false, true)},
		{policy: toolPolicy{pagedStorage: true, visitorResult: "items"}, name: "storage_list_file_objects", title: "List stored files", description: "List the files in a files resource, optionally only those under a path prefix.", method: "GET", suffix: "/files/{name}/objects", args: []string{"name"}, body: "", annotation: readOnly()},
		{policy: toolPolicy{analytics: analyticsPolicy{fileData: true}},
			name: "storage_put_file", title: "Upload stored file", description: "Upload one file into a files resource as the owner (base64, under 1 MiB; resize photos first): a gallery photo, a menu PDF, a download. A page shows it with SH.storage.files(name).url(path). Images that are part of the page's design go in create_site or update_site files_base64 instead. Ask before replacing an existing file.", method: "PUT", suffix: "/files/{name}/objects/{path...}", args: []string{"name", "path"}, body: "file", annotation: writes(true, true, true)},
		{name: "storage_delete_file", title: "Delete stored file", description: "Delete one file from a files resource. Ask first.", method: "DELETE", suffix: "/files/{name}/objects/{path...}", args: []string{"name", "path"}, body: "", annotation: writes(true, true, true)},
		{policy: toolPolicy{analytics: analyticsPolicy{secretText: true, secretURL: true}},
			name: "storage_file_download_link", title: "Get stored file download link", description: "Make a private link, good for ten minutes, to download one stored file in full, for example a photo a visitor uploaded, so the person can open it. Do not post it anywhere public.", method: "POST", suffix: "/files/{name}/download-link", args: []string{"name", "path"}, body: "link", annotation: readOnly()},
	}
	tools := make([]Tool, 0, len(routes))
	for _, route := range routes {
		r := route
		props := map[string]any{"site": str("The site's name, as in its address (list_sites shows the exact names).")}
		required := []string{"site"}
		argDescriptions := map[string]string{
			"name": "The resource's name (storage_list_resources shows the names; storage_set_resource creates one).",
			"key":  "The key within the KV resource, e.g. menu or settings.",
			"path": "The file's path within the files resource, e.g. photos/cake.webp (no leading slash).",
		}
		for _, arg := range r.args {
			props[arg] = str(argDescriptions[arg])
			required = append(required, arg)
		}
		if r.policy.pagedStorage {
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
			if r.policy.visitorParams {
				props["params"] = map[string]any{"anyOf": []any{props["params"], map[string]any{"type": "string"}}, "description": "Values for the ? placeholders, in order, as an array or a JSON-encoded array. Omit or [] when there are none."}
			}
			required = append(required, "sql")
		case "file":
			props["content_base64"] = str("The whole file in standard base64, at most 1 MiB decoded.")
			props["content_type"] = str("The file's MIME type, e.g. image/webp, image/jpeg, or application/pdf.")
			required = append(required, "content_base64", "content_type")
		}
		r.policy.analytics.emailIdentity = true
		tool := Tool{
			policy: r.policy, Name: r.name, Title: r.title, Description: r.description,
			InputSchema:  object(props, required...),
			OutputSchema: outObject(map[string]any{"response": anyJSON("Storage response with site-scoped visitor references on Simple Host."), "status": outInteger("REST status code.")}, "response", "status"),
			Annotations:  r.annotation,
		}
		tool.run = func(c *call, args map[string]any) (output, error) {
			return runStorageRoute(c, r.method, r.suffix, r.name, r.args, r.body, args)
		}
		if r.name == "storage_sql_execute" {
			tool = withOwnerFor(tool)
		}
		tools = append(tools, tool)
	}
	return tools
}

func runStorageRoute(c *call, method, suffix, toolName string, pathArgs []string, bodyKind string, args map[string]any) (output, error) {
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	path := "/v1/sites/" + url.PathEscape(site) + "/storage" + suffix
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
	if c.policy.pagedStorage {
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
			if list, ok := listArg(params); ok {
				params = list
			}
			body["params"] = params
			if c.caller.Mode != CallerModeEvents && c.policy.visitorParams {
				body["params"], e = c.storageVisitorParams(site, params)
				if e != nil {
					return output{}, e
				}
			}
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
		switch {
		case bodyKind == "resource":
			res.resourceKind, _ = args["body"].(map[string]any)["kind"].(string)
		case strings.HasPrefix(suffix, "/sqlite/"):
			res.resourceKind = "sqlite"
		case strings.HasPrefix(suffix, "/kv/"):
			res.resourceKind = "kv"
		}
		return output{}, restError(toolName, res)
	}
	var response any
	if len(res.body) > 0 {
		if err := json.Unmarshal(res.body, &response); err != nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON: %w", toolName, err)
		}
	}
	if c.caller.Mode != CallerModeEvents {
		if err := c.storageVisitorRefs(site, response); err != nil {
			return output{}, err
		}
	}
	structured := map[string]any{"response": response, "status": res.status}
	return output{Text: jsonText(structured), Structured: structured}, nil
}

// withOwnerForMode adds storage_sql_execute's dual mode (SIM-358, 2026-11-06):
// an optional for, naming a signed-in visitor (a visitor reference, from
// storage_visitor_emails or a row's visitor_id, or the email they signed in
// with) this one row is added for, instead of as the owner's own raw SQL.
// for replaces sql and params with table and values (the row's column
// values, the same shape POST .../tables/{t}/rows takes); it is refused
// (visitor_unknown) if the person has never signed in on this site, and the
// row is stamped written by the owner, never looking like the visitor's own.
//
// This reuses storage_sql_execute's existing tool rather than adding a new
// one (an input property is a smaller tool-list change than a new tool, and
// the output shape — {response, status} — does not change either way) and
// still goes through the one REST row-add path (POST .../tables/{t}/rows)
// that auth.js's table().add(values, {for}) and the owner's API key use, so
// every path shares the same app-service authorization (ResolveOwnerFor,
// TableAdd). An alternative considered and rejected: parsing for out of an
// ordinary INSERT statement's SQL text, so for could stay inside sql/params
// unchanged; table+values avoids that parsing risk entirely, at the cost of
// this one tool accepting two mutually exclusive input shapes.
func withOwnerFor(tool Tool) Tool {
	props := map[string]any{}
	for k, v := range tool.InputSchema["properties"].(map[string]any) {
		props[k] = v
	}
	props["for"] = str("Optional: add this row for one signed-in visitor instead of running sql, on a table whose read is own (records, personal only; refused for_requires_own elsewhere) — a visitor reference (storage_visitor_emails, or a row's visitor_id) or the email they signed in with. Requires table and values in place of sql and params. Refused visitor_unknown if they have never signed in on this site (resolve or check first with storage_visitor_emails if unsure), or visitor_email_ambiguous if more than one signed-in person currently matches that email (use their reference instead). The row is stamped written by you, is visible to that visitor as yours rather than theirs, and does not notify your own subscriptions; a table's own trigger that fights that is caught and the whole add rolled back (written_by_tampered) rather than shipped half right. Available to account owners.")
	props["table"] = str("With for: the table to add the row to (as storage_sql_schema created it).")
	props["values"] = map[string]any{"type": "object", "additionalProperties": anyJSON("A column value."), "description": "With for: the new row's column values, exactly as POST .../tables/{table}/rows takes them. Never set id, visitor_id, created_at, updated_at, or written_by; the server fills them and ignores anything sent for them."}
	var required []string
	for _, r := range tool.InputSchema["required"].([]string) {
		if r != "sql" {
			required = append(required, r)
		}
	}
	schema := map[string]any{}
	for k, v := range tool.InputSchema {
		schema[k] = v
	}
	schema["properties"] = props
	schema["required"] = required
	tool.InputSchema = schema
	inner := tool.run
	name, suffix := tool.Name, "/sqlite/{name}/tables/{table}/rows"
	tool.run = func(c *call, args map[string]any) (output, error) {
		forRef, _ := args["for"].(string)
		if strings.TrimSpace(forRef) == "" {
			return inner(c, args)
		}
		if _, has := args["sql"]; has {
			return output{}, fmt.Errorf("for replaces sql and params with table and values; send one pair or the other")
		}
		return runOwnerForAdd(c, name, suffix, args)
	}
	return tool
}

func runOwnerForAdd(c *call, toolName, suffix string, args map[string]any) (output, error) {
	site, err := siteArg(args)
	if err != nil {
		return output{}, err
	}
	resource, err := stringArg(args, "name")
	if err != nil {
		return output{}, err
	}
	table, err := stringArg(args, "table")
	if err != nil {
		return output{}, fmt.Errorf("table is required with for")
	}
	values, ok := args["values"].(map[string]any)
	if !ok {
		return output{}, fmt.Errorf("values is required with for: the new row's column values")
	}
	forRef, _ := args["for"].(string)
	forRef = strings.TrimSpace(forRef)
	if c.caller.Mode != CallerModeEvents && strings.HasPrefix(forRef, "v_") {
		siteID, e := c.visitorSiteID(site)
		if e != nil {
			return output{}, e
		}
		forRef, e = c.server.cfg.VisitorRefs.Decode(siteID, forRef)
		if e != nil {
			return output{}, fmt.Errorf("for: invalid visitor reference")
		}
	}
	body, err := json.Marshal(values)
	if err != nil {
		return output{}, err
	}
	base := "/v1/sites/" + url.PathEscape(site) + "/storage"
	path := base + strings.Replace(suffix, "{name}", url.PathEscape(resource), 1)
	path = strings.Replace(path, "{table}", url.PathEscape(table), 1)
	path += "?for=" + url.QueryEscape(forRef)
	res := c.do("POST", path, body, nil)
	if !res.ok() {
		res.resourceKind = "sqlite"
		return output{}, restError(toolName, res)
	}
	var response any
	if len(res.body) > 0 {
		if json.Unmarshal(res.body, &response) != nil {
			return output{}, fmt.Errorf("%s: server returned invalid JSON", toolName)
		}
	}
	structured := map[string]any{"response": response, "status": res.status}
	return output{Text: jsonText(structured), Structured: structured}, nil
}

// sqlSchemaIntrospectionHint is how to read a table's columns and a table's
// or index's exact SQL, since PRAGMA (and pragma_* table functions) are not
// available here; said upfront in storage_sql_schema's own description so a
// model does not have to hit the refusal first.
const sqlSchemaIntrospectionHint = "SELECT name, sql FROM sqlite_schema"

// sqlNoPragmaHint is the same fact for storage_sql_query and
// storage_sql_execute, whose own job is not introspection.
const sqlNoPragmaHint = "No PRAGMA: list tables and columns with " + sqlSchemaIntrospectionHint + "."

// withoutSpamCheck is storage_set_resource where the spam check is not
// offered (Simple Hack, or a server without Turnstile keys).
func withoutSpamCheck(tool Tool) Tool {
	tool.Description = strings.Replace(tool.Description, storageRulesSpamCheckSentence, "", 1)
	return tool
}

// storageResourceBody: every kind takes rules.
func storageResourceBody() map[string]any {
	table := object(map[string]any{"rules": storageRulesSchema()})
	body := object(map[string]any{
		"rules":         storageRulesSchema(),
		"kind":          map[string]any{"type": "string", "enum": []string{"kv", "sqlite", "files"}, "description": "kv (one JSON value per key), sqlite (tables), or files (uploads). Cannot change once created."},
		"tables":        map[string]any{"type": "object", "additionalProperties": table, "description": "sqlite only: rules per table, e.g. {\"orders\": {\"rules\": {...}}}. Tables not listed use the resource's rules. Omit to keep the current table rules; {} clears them."},
		"site_passcode": map[string]any{"type": "string", "enum": []string{"inherit", "off"}, "description": "inherit (default): when the site has a passcode, visitors must have entered it before reading or writing here. off: public reads skip the passcode."},
	}, "kind")
	body["description"] = "The resource's kind and access rules, e.g. {\"kind\": \"files\", \"rules\": {...}}."
	return body
}

// storageRulesSchema is shared by resource defaults and table overrides in both products.
func storageRulesSchema() map[string]any {
	policy := object(map[string]any{
		"name":        map[string]any{"type": "string"},
		"command":     map[string]any{"type": "string", "enum": []string{"read", "add", "edit", "delete", "all"}},
		"using":       map[string]any{"type": "string", "description": "Predicate over the existing row, at most 2048 bytes."},
		"check":       map[string]any{"type": "string", "description": "Predicate over the new row, at most 2048 bytes."},
		"restrictive": map[string]any{"type": "boolean"},
	}, "name", "command")
	set := object(map[string]any{
		"version":  map[string]any{"type": "integer", "enum": []int{1}},
		"policies": map[string]any{"type": "array", "items": policy, "description": "At most 32 policies."},
	}, "policies")
	set["type"] = []string{"object", "null"}
	set["description"] = "Rules for who may read, add, edit and delete (kv, sqlite and files). An empty policies array denies visitors; null is treated as absent."
	return set
}
