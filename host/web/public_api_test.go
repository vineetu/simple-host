package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/webassets"
)

func TestPublicAPIAudiences(t *testing.T) {
	source, err := EmbeddedTree.ReadFile("static/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{"public-host"} {
		t.Run(audience, func(t *testing.T) {
			body, err := webassets.PublicOpenAPISpec(source, audience)
			if err != nil {
				t.Fatal(err)
			}
			var spec struct {
				Paths map[string]map[string]any `json:"paths"`
			}
			if err := json.Unmarshal(body, &spec); err != nil {
				t.Fatal(err)
			}
			if len(spec.Paths) == 0 {
				t.Fatal("empty public reference")
			}
			for path := range spec.Paths {
				if strings.HasPrefix(path, "/v1/admin/") || strings.HasPrefix(path, "/internal/") || strings.HasPrefix(path, "/v1/setup/") {
					t.Errorf("internal path: %s", path)
				}
				if audience == "public-host" && (strings.HasPrefix(path, "/v1/hack/") || strings.HasPrefix(path, "/v1/events")) {
					t.Errorf("Hack path: %s", path)
				}
			}
		})
	}
}
