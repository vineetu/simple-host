package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHackLegacyStorageGate(t *testing.T) {
	old := HackMode()
	t.Cleanup(func() { SetHackMode(old) })
	paths := []string{
		"/v1/sites/team/state", "/v1/sites/team/state/history/1",
		"/v1/sites/team/collections/entries/items/1", "/v1/sites/team/data/name/kind",
		"/v1/sites/team/savers/block", "/v1/sites/team/history",
		"/v1/sites/team/allowed-origins", "/v1/sites/team/allow-anonymous-writes",
		"/v1/u/owner/sites/team/state", "/v1/u/owner/sites/team/collections/entries",
		"/v1/u/owner/sites/team/data/name/history/1", "/v1/u/owner/sites/team/history",
		"/v1/admin/sites/id/collections/private/export.csv", "/v1/admin/data-watch",
		"/v1/data-notify/stop",
	}
	for _, path := range paths {
		if !hackLegacyStoragePath(path) {
			t.Errorf("legacy path missed: %s", path)
		}
	}
	for _, path := range []string{
		"/v1/sites/team/storage/resources", "/v1/sites/team/storage/kv/settings/keys/theme",
		"/v1/sites/team/visitor/auth", "/v1/sites/team/me", "/v1/sites/team/lock",
		"/v1/hack/events/event/website/storage/resources", "/v1/hack/events/event/state",
		"/v1/admin/sites/id/versions", "/v1/visitor/establish",
	} {
		if hackLegacyStoragePath(path) {
			t.Errorf("current route blocked: %s", path)
		}
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	gate := HackLegacyStorageGate(next)
	SetHackMode(true)
	for _, path := range paths {
		r := httptest.NewRecorder()
		gate.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != http.StatusGone || !strings.Contains(r.Body.String(), "legacy_storage_removed") {
			t.Errorf("Hack %s: %d %s", path, r.Code, r.Body.String())
		}
	}
	SetHackMode(false)
	r := httptest.NewRecorder()
	gate.ServeHTTP(r, httptest.NewRequest(http.MethodGet, paths[0], nil))
	if r.Code != http.StatusNoContent {
		t.Errorf("Host legacy route: %d", r.Code)
	}
}

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
		if hackLegacyStoragePath(path) {
			t.Errorf("retired Hack path in public OpenAPI: %s", path)
		}
	}
	if !strings.Contains(string(filtered), "/storage/resources") {
		t.Fatal("current storage missing from Hack OpenAPI")
	}
	old := HackMode()
	t.Cleanup(func() { SetHackMode(old) })
	SetHackMode(true)
	w := httptest.NewRecorder()
	serveStaticPage("docs.html").ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/docs.html", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("Hack docs: %d", w.Code)
	}
	page := w.Body.String()
	if !strings.Contains(page, "Simple Hack REST API") || !strings.Contains(page, "<title>API Docs — Simple Hack</title>") || !strings.Contains(page, `href="/get-started"`) {
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
}
