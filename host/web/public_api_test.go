package web

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicAPIAudiences(t *testing.T) {
	source, err := EmbeddedTree.ReadFile("static/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{"public-host"} {
		t.Run(audience, func(t *testing.T) {
			body, err := publicOpenAPISpec(source, audience)
			if err != nil {
				t.Fatal(err)
			}
			var spec struct {
				Paths map[string]map[string]any `json:"paths"`
				Tags  []struct {
					Name string `json:"name"`
				} `json:"tags"`
			}
			if err := json.Unmarshal(body, &spec); err != nil {
				t.Fatal(err)
			}
			if len(spec.Paths) == 0 {
				t.Fatal("empty public reference")
			}
			for path, methods := range spec.Paths {
				if strings.HasPrefix(path, "/v1/admin/") || strings.HasPrefix(path, "/internal/") || strings.HasPrefix(path, "/v1/setup/") {
					t.Errorf("internal path: %s", path)
				}
				if audience == "public-host" && (strings.HasPrefix(path, "/v1/hack/") || strings.HasPrefix(path, "/v1/events")) {
					t.Errorf("Hack path: %s", path)
				}
				for method, op := range methods {
					if !openAPIMethod(method) {
						continue
					}
					operation, _ := op.(map[string]any)
					tags, _ := operation["tags"].([]any)
					if len(tags) != 1 {
						t.Errorf("%s %s: tags %v", method, path, tags)
					}
				}
			}
			if audience == "public-host" {
				for _, path := range []string{"/v1/me/home", "/v1/me/bio", "/v1/u/{handle}/showcase.json", "/v1/sites/{sitename}/showcase", "/v1/skills", "/v1/sites/{sitename}/storage/resources", "/v1/me/address-families"} {
					if spec.Paths[path] == nil {
						t.Errorf("missing current path: %s", path)
					}
				}

			}
		})
	}
}
