package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/edition"
)

func TestHackPublicDocsShowCurrentStorage(t *testing.T) {
	source, err := embeddedStatic.ReadFile("static/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := hackOpenAPISpec(source)
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
		if LegacyStoragePath(path) {
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
	hackAssets := newAssetPipeline(edition.ForKind(edition.Hack), "")
	w := httptest.NewRecorder()
	hackAssets.serveDocs(w, httptest.NewRequest(http.MethodGet, "/docs/", nil))
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

func TestHackAuthJSHasOnlyCurrentStorage(t *testing.T) {
	raw, err := embeddedStatic.ReadFile("static/auth.js")
	if err != nil {
		t.Fatal(err)
	}
	hack := string(hackAuthJS(raw))
	for _, keep := range []string{"    requireSignIn: function", "    storage: {", "    mount: function", "    email: {"} {
		if !strings.Contains(hack, keep) {
			t.Errorf("Hack auth.js lost %q", keep)
		}
	}
	for _, removed := range []string{"    state: {", "    collection: function", "    data: function", "SH.data(", "SH.collection("} {
		if strings.Contains(hack, removed) {
			t.Errorf("Hack auth.js still offers %q", removed)
		}
	}
	if !strings.Contains(string(raw), "    state: {") || !strings.Contains(string(raw), "    data: function") {
		t.Fatal("Host auth.js lost its legacy API")
	}
	rawStr := string(raw)
	for _, m := range []string{"    state: {\n", "    storage: {\n", "    collection: function (name) {\n", "    mount: function (target) {\n"} {
		if strings.Count(rawStr, m) != 1 {
			t.Errorf("auth.js marker %q appears %d times, want 1", m, strings.Count(rawStr, m))
		}
	}
}

func TestHelpPagesExcludePersonalPresentationOnHack(t *testing.T) {
	hostAssets := newAssetPipeline(edition.ForKind(edition.Host), "")
	hackAssets := newAssetPipeline(edition.ForKind(edition.Hack), "")
	for _, name := range []string{"features.html", "architecture.html"} {
		host, err := hostAssets.chromePage(name, ChromeData{})
		if err != nil {
			t.Fatal(err)
		}
		needle := "selected home"
		if name == "features.html" {
			needle = "Your home page"
		}
		if !strings.Contains(string(host), needle) {
			t.Fatalf("Host %s lost home-page help", name)
		}
		hack, err := hackAssets.chromePage(name, ChromeData{})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(hack), needle) || strings.Contains(string(hack), "/v1/me/home") {
			t.Errorf("Hack %s advertises personal presentation", name)
		}
	}
}
