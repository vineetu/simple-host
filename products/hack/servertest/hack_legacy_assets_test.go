package servertest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/webassets"
	"github.com/vsriram/simple-host/products/hack/hackhttp"
)

func TestHackPublicDocsShowCurrentStorage(t *testing.T) {
	source, err := webassets.Static.ReadFile("static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := hackhttp.NewAssets(chromeTestHandler(edition.Hack).AssetServices(), "").Hooks().OpenAPISpec(source)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(filtered, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Paths) == 0 {
		t.Fatal("Hack OpenAPI has no paths")
	}
	for path := range spec.Paths {
		if handler.LegacyStoragePath(path) {
			t.Errorf("retired Hack path in public OpenAPI: %s", path)
		}
	}
	for _, personal := range []string{"/v1/me/home", "/v1/me/bio", "/v1/sites/{sitename}/showcase", "/v1/u/{handle}/showcase.json"} {
		if _, ok := spec.Paths[personal]; ok {
			t.Errorf("personal presentation route in Hack OpenAPI: %s", personal)
		}
	}
	if strings.Contains(string(spec.Paths["/v1/me"]), `"home_site"`) {
		t.Fatal("Hack account reference offers a personal home")
	}
	if !strings.Contains(string(filtered), "/storage/resources") {
		t.Fatal("current storage missing from Hack OpenAPI")
	}
	hackSite := chromeTestHandler(edition.Hack)
	mux := http.NewServeMux()
	handler.RegisterUIRoutes(mux, "https://simple-hack.app", hackSite)
	mountHackFiles(mux, hackSite)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("Hack docs: %d", w.Code)
	}
	page := w.Body.String()
	if !strings.Contains(page, "Simple Hack") || !strings.Contains(page, "<title>Docs | Simple Hack</title>") || !strings.Contains(page, `href="/get-started"`) {
		t.Fatal("Hack docs missing current heading or skills link")
	}
	for _, stale := range []string{"npx skills add", "https://simple-host.app/skills.zip", "<h1>Simple Host REST API</h1>"} {
		if strings.Contains(page, stale) {
			t.Errorf("Hack docs still contains %q", stale)
		}
	}
}

func TestHelpPagesExcludePersonalPresentationOnHack(t *testing.T) {
	host := chromeTestMux(t, edition.Host)
	hack := chromeTestMux(t, edition.Hack)
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
