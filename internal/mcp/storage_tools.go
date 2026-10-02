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
		{"storage_list_resources", "List storage resources", "List this site's KV, SQLite and file resources and their policies. Owner only.", "GET", "/resources", nil, "", readOnly()},
		{"storage_get_usage", "Read site storage usage", "Read used, remaining and limit bytes for this site's pooled KV, SQLite and file storage, with a per-type breakdown. Owner only; deployed website assets and legacy saved data are separate.", "GET", "/usage", nil, "", readOnly()},
		{"storage_set_resource", "Create or set storage resource policy", "Create a named resource or change its read, write and site-passcode policy. Kind is immutable. Setting read or write to anyone can expose data or permit anonymous writes; ask the owner before changing policy.", "PUT", "/resources/{name}", []string{"name"}, "resource", writes(true, true, true)},
		{"storage_delete_resource", "Delete storage resource", "Permanently remove a resource and all its data. Ask the owner to confirm this exact resource first. Owner only.", "DELETE", "/resources/{name}", []string{"name"}, "", writes(true, true, false)},
		{"storage_list_kv_keys", "List KV keys", "List keys in a KV resource, optionally by prefix. Owner only through this connector.", "GET", "/kv/{name}/keys", []string{"name"}, "", readOnly()},
		{"storage_get_kv", "Read KV value", "Read one JSON value from a KV resource. Owner only through this connector.", "GET", "/kv/{name}/keys/{key}", []string{"name", "key"}, "", readOnly()},
		{"storage_put_kv", "Set KV value", "Set or replace one JSON value in a KV resource. It may become public under the resource policy; ask before overwriting existing data. Owner only through this connector.", "PUT", "/kv/{name}/keys/{key}", []string{"name", "key"}, "value", writes(true, true, true)},
		{"storage_delete_kv", "Delete KV value", "Delete one KV key and its value. Ask first. Owner only through this connector.", "DELETE", "/kv/{name}/keys/{key}", []string{"name", "key"}, "", writes(true, true, false)},
		{"storage_sql_query", "Query SQLite resource", "Run a read-only parameterized SQLite query; the REST SQLite authorizer enforces read-only access. Owner only through this connector.", "POST", "/sqlite/{name}/query", []string{"name"}, "sql", readOnly()},
		{"storage_sql_execute", "Execute SQLite statement", "Run a parameterized SQLite data-changing statement. Rows may become public under the resource policy; ask before changing or deleting existing rows. Owner only through this connector.", "POST", "/sqlite/{name}/execute", []string{"name"}, "sql", writes(true, false, true)},
		{"storage_sql_schema", "Change SQLite schema", "Run a parameterized SQLite schema statement. Schema changes can remove or reshape data; ask first. Owner only.", "POST", "/sqlite/{name}/schema", []string{"name"}, "sql", writes(true, false, false)},
		{"storage_list_file_objects", "List stored files", "List file paths in a files resource, optionally by prefix. Owner only through this connector.", "GET", "/files/{name}/objects", []string{"name"}, "", readOnly()},
		{"storage_put_file", "Upload stored file", "Upload or replace one file from base64 bytes, at most 1 MiB through this tool; use direct REST for larger files. Files may become public under the resource policy. Ask before overwriting. This is durable data, separate from deployed website files.", "PUT", "/files/{name}/objects/{path...}", []string{"name", "path"}, "file", writes(true, true, true)},
		{"storage_delete_file", "Delete stored file", "Delete one stored file. Ask first. Owner only through this connector.", "DELETE", "/files/{name}/objects/{path...}", []string{"name", "path"}, "", writes(true, true, false)},
		{"storage_file_download_link", "Get stored file download link", "Mint a scoped, short-lived owner download URL for the full file bytes; the link expires after ten minutes and is rechecked when used. Keep it private. Owner only.", "POST", "/files/{name}/download-link", []string{"name", "path"}, "link", writes(false, false, false)},
	}
	tools := make([]Tool, 0, len(routes))
	for _, route := range routes {
		r := route
		siteField := "site"
		if event {
			siteField = "event"
			r.name = "hack_event_" + r.name
			r.title = "Event website: " + r.title
			r.description += " Organiser only on this event's custom website."
		}
		props := map[string]any{siteField: str("Site name or event slug on Simple Hack.")}
		required := []string{siteField}
		for _, arg := range r.args {
			props[arg] = str("REST resource, key or path parameter: " + arg + ".")
			required = append(required, arg)
		}
		if strings.HasSuffix(r.name, "storage_list_kv_keys") || strings.HasSuffix(r.name, "storage_list_file_objects") {
			props["prefix"] = str("Optional key or file-path prefix.")
			props["after"] = str("Optional pagination cursor from the previous response.")
			props["limit"] = map[string]any{"type": "integer", "description": "Optional maximum results; REST validates its range."}
		}
		switch r.body {
		case "resource":
			props["body"] = object(map[string]any{
				"kind":          str("kv, sqlite or files; immutable after creation."),
				"read":          str("anyone, signed-in or owner."),
				"write":         str("anyone, signed-in or owner."),
				"site_passcode": str("inherit or off; inherit requires the site's existing visitor unlock when configured."),
			})
			required = append(required, "body")
		case "value":
			props["value"] = anyJSON("JSON value to store, including null.")
			required = append(required, "value")
		case "sql":
			props["sql"] = str("SQLite statement. Use ? placeholders for values.")
			props["params"] = map[string]any{"type": "array", "items": anyJSON("One bound SQLite parameter.")}
			required = append(required, "sql")
		case "file":
			props["content_base64"] = str("Complete file bytes in standard base64, at most 1 MiB decoded.")
			props["content_type"] = str("File MIME type, e.g. image/png or application/pdf.")
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
