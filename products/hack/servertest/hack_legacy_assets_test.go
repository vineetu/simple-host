package servertest

import (
	"encoding/json"
	productedition "github.com/vsriram/simple-host/products/hack/edition"
	"strings"
	"testing"

	webassets "github.com/vsriram/simple-host/products/hack/webassets"
)

func TestHackPublicDocsShowCurrentStorage(t *testing.T) {
	source, err := webassets.Static.ReadFile("static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	filtered := source
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(filtered, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("Hack OpenAPI has no paths")
	}
	for _, personal := range []string{"/v1/me/home", "/v1/me/bio", "/v1/me/address-families", "/v1/sites/{sitename}/domain", "/v1/sites/{sitename}/showcase", "/v1/u/{handle}/showcase.json"} {
		if _, ok := spec.Paths[personal]; ok {
			t.Errorf("personal presentation route in Hack OpenAPI: %s", personal)
		}
	}
	for _, path := range []string{"/v1/hack/events", "/v1/hack/events/{slug}/website/storage/{rest}"} {
		if spec.Paths[path] == nil {
			t.Errorf("Hack event API missing: %s", path)
		}
	}
	if strings.Contains(string(spec.Paths["/v1/me"]), `"home_site"`) {
		t.Fatal("Hack account reference offers a personal home")
	}
	if !strings.Contains(string(filtered), "/storage/resources") {
		t.Fatal("current storage missing from Hack OpenAPI")
	}
}

func TestHelpPagesExcludePersonalPresentationOnHack(t *testing.T) {
	host := chromeTestMux(t, productedition.Host)
	hack := chromeTestMux(t, productedition.Hack)
	for _, name := range []string{"features", "architecture.html"} {
		needle := "selected home"
		if name == "features" {
			needle = "Your home page"
		}
		if !strings.Contains(get(t, host, "", "/"+name).Body.String(), needle) {
			t.Fatalf("Host %s lost help", name)
		}
		body := get(t, hack, "", "/"+name).Body.String()
		if strings.Contains(body, needle) || strings.Contains(body, "/v1/me/home") {
			t.Errorf("Hack %s advertises personal presentation", name)
		}
	}
}
