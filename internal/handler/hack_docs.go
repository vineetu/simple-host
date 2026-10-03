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
		if hackLegacyStoragePath(path) || path == "/v1/sites/{sitename}/domain" || path == "/v1/sites/{sitename}/domain/check" {
			delete(paths, path)
			continue
		}
		if strings.Contains(path, "/export") {
			methods, _ := paths[path].(map[string]any)
			for _, value := range methods {
				if operation, ok := value.(map[string]any); ok {
					operation["description"] = "Download an owner-authorised archive of website files and stored data. It may include historical private data from before the previous storage model was retired. Keep the archive and any download link private."
				}
			}
		}
	}
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
	replacement := []byte(`<p>Use the <a href="/get-started">five current Simple Hack skills</a> with the signed-in connector. Team and custom event websites use KV, SQLite and file resources.</p>`)
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
