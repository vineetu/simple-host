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
		{"storage_list_resources", "List storage resources", "List a site's saved-data resources (KV, SQLite, files) with each one's name, kind, and who may read and write it. Call it before reading or changing saved data, to get the exact resource name.", "GET", "/resources", nil, "", readOnly()},
		{"storage_get_usage", "Read site storage usage", "Show how much of a site's saved-data allowance is used: KV and SQLite together (1,000,000 bytes) and files separately (10 MB). The site's own pages and images are counted elsewhere (list_sites).", "GET", "/usage", nil, "", readOnly()},
		{"storage_set_resource", "Create or set storage resource policy", "Create a saved-data resource on a site, or change who may read and write an existing one. kind: kv (one JSON value per key), sqlite (tables), or files (uploads and downloads); the kind cannot change later. read: anyone, signed-in, own (each signed-in visitor reads only what they added), or owner. write: anyone, signed-in, or owner, with write_mode full (change and delete too) or add (add new rows, keys, or files only; required when read is own and visitors write). Pick by need: each person's records = sqlite, read own, write signed-in, add; a form only the owner reads = sqlite, read owner, write signed-in (or anyone), add; page info the owner writes = kv, read anyone, write owner; a gallery visitors add to = files, read anyone, write signed-in, add; a gallery only the owner fills = files, read anyone, write owner. Create the resource, and its tables with storage_sql_schema, before publishing the page that uses it. Ask the person before making data readable or writable by more people.", "PUT", "/resources/{name}", []string{"name"}, "resource", writes(true, true, true)},
		{"storage_delete_resource", "Delete storage resource", "Remove a resource and everything saved in it, for good. Ask the person to confirm this exact resource first.", "DELETE", "/resources/{name}", []string{"name"}, "", writes(true, true, false)},
		{"storage_list_kv_keys", "List KV keys", "List the keys in a KV resource, optionally only those starting with a prefix.", "GET", "/kv/{name}/keys", []string{"name"}, "", readOnly()},
		{"storage_get_kv", "Read KV value", "Read one key's JSON value from a KV resource. The answer is {key, value}.", "GET", "/kv/{name}/keys/{key}", []string{"name", "key"}, "", readOnly()},
		{"storage_put_kv", "Set KV value", "Set one key in a KV resource to a JSON value, replacing what was there. This is how the owner changes page info a page reads live (a menu, prices, opening hours, settings) without republishing the site; the page reads it with SH.storage.kv(name).get(key). Ask before overwriting a value visitors wrote.", "PUT", "/kv/{name}/keys/{key}", []string{"name", "key"}, "value", writes(true, true, true)},
		{"storage_delete_kv", "Delete KV value", "Delete one key and its value from a KV resource. Ask first.", "DELETE", "/kv/{name}/keys/{key}", []string{"name", "key"}, "", writes(true, true, false)},
		{"storage_sql_query", "Query SQLite resource", "Read rows from a SQLite resource with a SELECT, as the owner: it sees every person's rows, including on a read-own resource. Read-only; put values in params with ? placeholders. Use it to show the person their orders, entries, or messages.", "POST", "/sqlite/{name}/query", []string{"name"}, "sql", readOnly()},
		{"storage_sql_execute", "Execute SQLite statement", "Change rows in a SQLite resource as the owner: INSERT, UPDATE, or DELETE with ? placeholders, for example setting an order's status. Ask before changing or deleting what visitors sent.", "POST", "/sqlite/{name}/execute", []string{"name"}, "sql", writes(true, false, true)},
		{"storage_sql_schema", "Change SQLite schema", "Create or change tables in a SQLite resource: CREATE TABLE, CREATE INDEX, CREATE TRIGGER, ALTER TABLE, one statement per call, with IF NOT EXISTS so a rerun is harmless. Use id INTEGER PRIMARY KEY and a created_at TEXT column; the server fills both. Do not declare visitor_id: on a read-own resource the server adds an indexed visitor_id column to every table itself. Ask before a change that drops or reshapes data.", "POST", "/sqlite/{name}/schema", []string{"name"}, "sql", writes(true, false, false)},
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
			r.description += " Organiser only on this event's custom website."
			if r.name == "hack_event_storage_set_resource" {
				r.description += " On Simple Hack event sites read own and write_mode add are not available: use read anyone, signed-in, or owner."
			}
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
			body := object(map[string]any{
				"kind":          map[string]any{"type": "string", "enum": []string{"kv", "sqlite", "files"}, "description": "kv (one JSON value per key), sqlite (tables), or files (uploads). Cannot change once created."},
				"read":          map[string]any{"type": "string", "enum": []string{"anyone", "signed-in", "own", "owner"}, "description": "Who may read: anyone, signed-in (any visitor signed in on the site), own (each signed-in visitor reads only what they added themselves), or owner (only you, through these tools). Default owner."},
				"write":         map[string]any{"type": "string", "enum": []string{"anyone", "signed-in", "owner"}, "description": "Who may write: anyone, signed-in (visitors signed in on the site), or owner (only you). Default owner."},
				"write_mode":    map[string]any{"type": "string", "enum": []string{"full", "add"}, "description": "add: visitors may add new rows, keys, or files but never change or delete anything (use this for orders, entries, forms, and uploads; required when read is own and visitors write). full: visitors may also change and delete. Default full for a new resource; omitted on an existing one keeps its mode."},
				"site_passcode": map[string]any{"type": "string", "enum": []string{"inherit", "off"}, "description": "inherit (default): when the site has a passcode, visitors must have entered it before reading or writing here. off: public reads skip the passcode."},
			}, "kind")
			body["description"] = "The resource's kind and policy, e.g. {\"kind\": \"sqlite\", \"read\": \"own\", \"write\": \"signed-in\", \"write_mode\": \"add\"}."
			props["body"] = body
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
