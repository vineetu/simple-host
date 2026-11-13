// Package servertest provides product-independent fixtures for the composed server.
package servertest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/server"
)

// Build constructs the program without starting workers and closes it after the test.
// Callers supply the extensions; this package imports no product.
func Build(t *testing.T, cfg config.Config, exts []*extension.Extension) *server.App {
	t.Helper()
	app, err := server.Build(cfg, exts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app
}

// Config loads isolated fixture settings using the caller's disposable database.
func Config(t *testing.T, dsn string) config.Config {
	t.Helper()
	t.Setenv("DB_DSN", dsn)
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("GEOIP_DIR", t.TempDir())
	t.Setenv("ADMIN_API_KEY", "wiring-test-admin")
	t.Setenv("SITE_DOMAIN", "wiring.example")
	t.Setenv("CONTENT_HOST", "sites.wiring.example")
	t.Setenv("PUBLIC_BASE_URL", "https://wiring.example")
	t.Setenv("EVENTS", "")
	// No provider clients or workers run during a wiring snapshot.
	for _, key := range []string{"RESEND_API_KEY", "PHOTODNA_API_KEY", "PHOTODNA_API_KEY_FILE", "EVENT_DNS_TOKEN", "EVENT_NAME_PEER", "BRAIN_DIR", "ANALYTICS_LOG", "ASSISTANT_BASE_URL", "ASSISTANT_MODEL", "HACK_DEMO_EVENT_SLUG"} {
		t.Setenv(key, "")
	}
	t.Setenv("PAPER_METRICS", "on")
	t.Setenv("MIGRATE_ON_START", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// CheckWiring compares route registration and worker order with the original snapshots.
func CheckWiring(t *testing.T, app *server.App, root, product string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs/generated/inventory.json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Routes []struct{ Method, Path string }
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Routes) == 0 {
		t.Fatal("empty route inventory")
	}
	wildcard := regexp.MustCompile(`\{[^}]+\}`)
	mux, set := app.Mux, app.Workers
	var routes strings.Builder
	for _, route := range inventory.Routes {
		method := route.Method
		if method == "ANY" {
			method = http.MethodGet
		}
		path := wildcard.ReplaceAllString(strings.ReplaceAll(route.Path, "{$}", ""), "sample")
		req := httptest.NewRequest(method, "https://wiring.example"+path, nil)
		_, matched := mux.Handler(req)
		fmt.Fprintf(&routes, "%s %s -> %q\n", route.Method, route.Path, matched)
	}
	checkWiringSnapshot(t, root, "routes-"+product+".txt", routes.String())
	var names strings.Builder
	names.WriteString("registration order\n")
	for _, name := range set.Order() {
		fmt.Fprintln(&names, name)
	}
	names.WriteString("\nsorted status names\n")
	for _, status := range set.Status() {
		fmt.Fprintln(&names, status.Name)
	}
	checkWiringSnapshot(t, root, "workers-"+product+".txt", names.String())
}
func checkWiringSnapshot(t *testing.T, root, name, got string) {
	t.Helper()
	path := filepath.Join(root, "internal/server/testdata", name)

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s changed\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}
