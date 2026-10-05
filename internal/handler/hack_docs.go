package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// hackOpenAPISpec derives the hosted Hack reference from the shared contract.
// JSON is valid YAML 1.2, so both public spec URLs serve this same filtered
// document while Simple Host keeps the original YAML and JSON unchanged.
func hackOpenAPISpec(source []byte) ([]byte, error) {
	var spec map[string]any
	if err := json.Unmarshal(source, &spec); err != nil {
		return nil, err
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openapi paths missing")
	}
	for path := range paths {
		if hackLegacyStoragePath(path) || path == "/v1/sites/{sitename}/domain" || path == "/v1/sites/{sitename}/domain/check" ||
			path == "/v1/me/home" || path == "/v1/me/bio" || path == "/v1/sites/{sitename}/showcase" || path == "/v1/u/{handle}/showcase.json" {
			delete(paths, path)
			continue
		}
		methods, _ := paths[path].(map[string]any)
		for method, value := range methods {
			operation, ok := value.(map[string]any)
			if !ok {
				continue
			}
			if description := hackOpenAPIDescription(path, method); description != "" {
				operation["description"] = description
			}
		}
	}
	// Hack accounts have event roles and team sites, without personal homes.
	me, _ := paths["/v1/me"].(map[string]any)
	get, _ := me["get"].(map[string]any)
	responses, _ := get["responses"].(map[string]any)
	okResponse, _ := responses["200"].(map[string]any)
	content, _ := okResponse["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	properties, _ := schema["properties"].(map[string]any)
	delete(properties, "home_site")
	spec["info"] = map[string]any{
		"title":       "Simple Hack API",
		"version":     "2.0.0",
		"description": "Hosted hackathons, team and custom event websites, and owner-declared KV, SQLite and raw-file resources. Website storage has resource-wide access policies. Use the signed-in connector or the trusted Simple Hack browser for event work.",
	}
	spec["servers"] = []any{map[string]any{"url": "https://simple-hack.app"}}
	if tags, ok := spec["tags"].([]any); ok {
		current := make([]any, 0, len(tags))
		for _, value := range tags {
			tag, _ := value.(map[string]any)
			name, _ := tag["name"].(string)
			if name != "State" && name != "Collections" {
				current = append(current, value)
			}
		}
		spec["tags"] = current
	}
	// Keep component definitions only when a retained operation references
	// them, including references reached through another component.
	if components, ok := spec["components"].(map[string]any); ok {
		if responses, ok := components["responses"].(map[string]any); ok {
			if tooLarge, ok := responses["TooLarge"].(map[string]any); ok {
				tooLarge["description"] = "The request or deployment exceeds its applicable size limit."
			}
		}
		if schemas, ok := components["schemas"].(map[string]any); ok {
			if errSchema, ok := schemas["Error"].(map[string]any); ok {
				if properties, ok := errSchema["properties"].(map[string]any); ok {
					if code, ok := properties["code"].(map[string]any); ok {
						code["description"] = "Stable snake_case refusal code; see the response for details."
					}
				}
			}
		}
		used := map[string]bool{}
		var scan func(any)
		scan = func(value any) {
			switch v := value.(type) {
			case map[string]any:
				if ref, ok := v["$ref"].(string); ok && strings.HasPrefix(ref, "#/components/") && !used[ref] {
					used[ref] = true
					parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
					if len(parts) == 2 {
						if section, ok := components[parts[0]].(map[string]any); ok {
							scan(section[parts[1]])
						}
					}
				}
				for _, child := range v {
					scan(child)
				}
			case []any:
				for _, child := range v {
					scan(child)
				}
			}
		}
		scan(paths)
		for sectionName, value := range components {
			if sectionName == "securitySchemes" {
				continue
			}
			section, ok := value.(map[string]any)
			if !ok {
				continue
			}
			for name := range section {
				if !used["#/components/"+sectionName+"/"+name] {
					delete(section, name)
				}
			}
		}
	}
	return json.MarshalIndent(spec, "", "  ")
}

// Shared Host operation descriptions for these still-live routes explain the
// retired state/collection model. Hack keeps the routes but describes its
// current website resources instead.
func hackOpenAPIDescription(path, method string) string {
	switch path {
	case "/v1/admin/export.tar.gz", "/v1/export", "/v1/me/export.tar.gz", "/v1/me/export.zip", "/v1/sites/{sitename}/export-link", "/v1/sites/{sitename}/export.tar.gz":
		return "Download an owner-authorised archive of website files and stored data. It may include historical private data from before the previous storage model was retired. Keep the archive and any download link private."
	}
	switch path + " " + method {
	case "/v1/admin/users/{id}/key post":
		return "Replace a participant's lost account key. The new key is shown once; their websites and event work remain intact."
	case "/v1/idle/restore post":
		return "Restore a website from Recently deleted with its versions and stored resources, using the emailed confirmation link."
	case "/v1/me delete":
		return "Permanently delete the signed-in account and its websites, stored resources and connected apps. This cannot be undone; offer the account export first."
	case "/v1/me/deleted-sites get":
		return "List websites recently deleted by this account, including their restoration deadline."
	case "/v1/me/keys post":
		return "Create an account key for a trusted CI client or another machine. A deploy-only key can publish the account's permitted websites but cannot manage account settings or website storage. The key is returned once; keep it outside chat."
	case "/v1/sites/{sitename} delete":
		return "Take the website offline and move it to Recently deleted, retaining its versions and stored resources until the restoration deadline."
	case "/v1/sites/{sitename} patch":
		return "Change the website's offline status or name. While offline, visitor storage reads and writes are refused; versions and stored resources remain available to the owner."
	case "/v1/sites/{sitename}/restore post":
		return "Restore a website from Recently deleted with its versions, stored resources and connected address."
	case "/v1/sites/{sitename}/lock put":
		return "Set or generate the owner's website passcode through this trusted REST route. Visitors must unlock each host before viewing the site. A passcode is a site gate, not per-person privacy for a storage resource."
	case "/v1/sites/{sitename}/storage/usage get":
		return "The default limit is 1,000,000 bytes pooled across this website's KV, SQLite and raw-file resources. Deployed assets and historical data have separate limits."
	}
	return ""
}

func hackDocsHTML(body []byte) []byte {
	body = bytes.Replace(body, []byte("<title>API Docs — Simple Host</title>"), []byte("<title>API Docs — Simple Hack</title>"), 1)
	body = bytes.Replace(body, []byte("<h1>Simple Host REST API</h1>"), []byte("<h1>Simple Hack REST API</h1>"), 1)
	start := bytes.Index(body, []byte("<details class=\"install\" id=\"install-skills\">"))
	if start < 0 {
		return body
	}
	end := bytes.Index(body[start:], []byte("</details>"))
	if end < 0 {
		return body
	}
	end += start + len("</details>")
	replacement := []byte(`<p><a href="/get-started">Add Simple Hack to your AI</a>, then ask to run, join or judge an event. Team and custom event websites use KV, SQLite and file resources.</p>`)
	body = append(append([]byte(nil), body[:start]...), append(replacement, body[end:]...)...)
	// The old install panel had a script to rewrite Host installer URLs.
	// Remove it along with the panel on Simple Hack.
	start = bytes.Index(body, []byte("    // docs.html is not host-rewritten"))
	if start >= 0 {
		end = bytes.Index(body[start:], []byte("    SwaggerUIBundle({"))
		if end >= 0 {
			end += start
			body = append(append([]byte(nil), body[:start]...), body[end:]...)
		}
	}
	return body
}

// Shared help pages omit personal presentation when served on Hack.
func hackPersonalPresentationHTML(name string, body []byte) []byte {
	switch name {
	case "features.html":
		start := bytes.Index(body, []byte(`<div class="feat" id="your-home-page">`))
		if start >= 0 {
			end := bytes.Index(body[start:], []byte("</div>"))
			if end >= 0 {
				return append(append([]byte{}, body[:start]...), body[start+end+len("</div>"):]...)
			}
		}
	case "architecture.html":
		return bytes.ReplaceAll(body, []byte("showcase or selected home, with a live feed, bio, pins and order"), []byte("showcase"))
	}
	return body
}
