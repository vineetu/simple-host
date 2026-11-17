package webassets

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The public API references are derived here so the server and the docs
// reference generator (scripts/inventory openapi-json) share one filter.

// PublicOpenAPISpec selects operations from a contract by audience.
// Internal operations remain in the contract for coverage, never in a public spec.
func PublicOpenAPISpec(source []byte, audience string) ([]byte, error) {
	var spec map[string]any
	if err := json.Unmarshal(source, &spec); err != nil {
		return nil, err
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openapi paths missing")
	}
	usedTags := map[string]bool{}
	for path, value := range paths {
		methods, _ := value.(map[string]any)
		retained := false
		for method, value := range methods {
			if !OpenAPIMethod(method) {
				continue
			}
			operation, _ := value.(map[string]any)
			audiences, _ := operation["x-audience"].([]any)
			include := false
			for _, value := range audiences {
				include = include || value == audience
			}
			if !include {
				delete(methods, method)
				continue
			}
			retained = true
			delete(operation, "x-audience")
			tags, _ := operation["tags"].([]any)
			for _, tag := range tags {
				name, _ := tag.(string)
				usedTags[name] = true
			}
		}
		if !retained {
			delete(paths, path)
		}
	}
	if tags, ok := spec["tags"].([]any); ok {
		current := make([]any, 0, len(tags))
		for _, value := range tags {
			tag, _ := value.(map[string]any)
			name, _ := tag["name"].(string)
			if usedTags[name] {
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

func OpenAPIMethod(method string) bool {
	switch method {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	}
	return false
}
