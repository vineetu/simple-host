package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicAPIAudiences(t *testing.T) {
	source, err := embeddedStatic.ReadFile("static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, audience := range []string{"public-host", "public-hack"} {
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
						continue
					}
					if tags[0] == "Deprecated: saved data" && (audience != "public-host" || operation["deprecated"] != true) {
						t.Errorf("legacy operation: %s %s", method, path)
					}
				}
			}
			if audience == "public-host" {
				for _, path := range []string{"/v1/me/home", "/v1/me/bio", "/v1/u/{handle}/showcase.json", "/v1/sites/{sitename}/showcase", "/v1/skills", "/v1/sites/{sitename}/storage/resources", "/v1/me/address-families"} {
					if spec.Paths[path] == nil {
						t.Errorf("missing current path: %s", path)
					}
				}
				if spec.Tags[len(spec.Tags)-1].Name != "Deprecated: saved data" {
					t.Error("deprecated section must be last")
				}
			} else {
				if spec.Paths["/v1/hack/events"] == nil || spec.Paths["/v1/hack/events/{slug}/website/storage/{rest}"] == nil {
					t.Error("Hack event API missing")
				}
				for _, path := range []string{"/v1/me/home", "/v1/me/bio", "/v1/me/address-families", "/v1/u/{handle}/showcase.json", "/v1/sites/{sitename}/domain"} {
					if spec.Paths[path] != nil {
						t.Errorf("Host-only path in Hack: %s", path)
					}
				}
			}
		})
	}
}

func TestHostServesPublicSpec(t *testing.T) {
	SetHackMode(false)
	SetInstanceHosts("simple-host.app", "", "")
	defer SetInstanceHosts("simple-host.app", "", "")
	mux := http.NewServeMux()
	RegisterUIRoutes(mux, "https://simple-host.app", chromeTestHandler())
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("GET", "/hack-llms.txt", nil))
	if recorder.Code != http.StatusNotFound {
		t.Error("raw Hack listing exposed on Host")
	}
	var first string
	for _, path := range []string{"/openapi.json", "/openapi.yaml"} {
		r := httptest.NewRecorder()
		mux.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatalf("%s: %d", path, r.Code)
		}
		if strings.Contains(r.Body.String(), "/v1/admin/") || strings.Contains(r.Body.String(), "/v1/hack/") || strings.Contains(r.Body.String(), "/v1/events") {
			t.Errorf("%s: mixed reference", path)
		}
		if first != "" && first != r.Body.String() {
			t.Error("JSON/YAML reference drift")
		}
		first = r.Body.String()
	}
}
