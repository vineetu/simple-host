package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/testdb"
	"github.com/vsriram/simple-host/internal/workers"
)

func TestWiring(t *testing.T) {
	data, err := os.ReadFile("../../docs/generated/inventory.json")
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
	for _, product := range []string{"host", "hack"} {
		t.Run(product, func(t *testing.T) {
			t.Setenv("DB_DSN", testdb.Fresh(t))
			t.Setenv("DATA_DIR", t.TempDir())
			t.Setenv("GEOIP_DIR", t.TempDir())
			t.Setenv("ADMIN_API_KEY", "wiring-test-admin")
			t.Setenv("SITE_DOMAIN", "wiring.example")
			t.Setenv("CONTENT_HOST", "sites.wiring.example")
			t.Setenv("PUBLIC_BASE_URL", "https://wiring.example")
			t.Setenv("EVENTS", "")
			if product == "hack" {
				t.Setenv("EVENTS", "hosted")
			}
			// No provider clients or workers run during a wiring snapshot.
			for _, key := range []string{"RESEND_API_KEY", "PHOTODNA_API_KEY", "PHOTODNA_API_KEY_FILE", "EVENT_DNS_TOKEN", "EVENT_NAME_PEER", "BRAIN_DIR", "ANALYTICS_LOG", "ASSISTANT_BASE_URL", "ASSISTANT_MODEL", "HACK_DEMO_EVENT_SLUG"} {
				t.Setenv(key, "")
			}
			t.Setenv("PAPER_METRICS", "on")
			t.Setenv("MIGRATE_ON_START", "false")
			mux, set := buildWiring(t)
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
			checkWiringSnapshot(t, "routes-"+product+".txt", routes.String())
			var names strings.Builder
			names.WriteString("registration order\n")
			for _, name := range set.Order() {
				fmt.Fprintln(&names, name)
			}
			names.WriteString("\nsorted status names\n")
			for _, status := range set.Status() {
				fmt.Fprintln(&names, status.Name)
			}
			checkWiringSnapshot(t, "workers-"+product+".txt", names.String())
		})
	}
}

func buildWiring(t *testing.T) (*http.ServeMux, *workers.Set) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	app, err := Build(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close)
	return app.Mux, app.Workers
}

func checkWiringSnapshot(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s changed\nwant:\n%s\ngot:\n%s", name, want, got)
	}
}

func TestRunVersion(t *testing.T) {
	// Version dispatch must work without loading configuration or a database.
	t.Setenv("DB_DSN", "")
	t.Setenv("ADMIN_API_KEY", "")
	for _, arg := range []string{"version", "--version"} {
		if code := Run([]string{arg}); code != 0 {
			t.Errorf("Run(%q) = %d", arg, code)
		}
	}
}

func TestRunUnknownDatabase(t *testing.T) {
	t.Setenv("DB_DSN", "host=unknown-database.invalid port=1 dbname=wiring connect_timeout=1")
	t.Setenv("ADMIN_API_KEY", "wiring-test-admin")
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("MIGRATE_ON_START", "false")
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	if code := Run(nil); code == 0 {
		t.Fatal("Run succeeded with an unknown database address")
	}
	if !strings.Contains(logs.String(), "schema check:") {
		t.Fatalf("Run did not reach the database: %s", logs.String())
	}
}
