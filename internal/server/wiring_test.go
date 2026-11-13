package server

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/extension"
)

func TestRunVersion(t *testing.T) {
	// Version dispatch must work without loading configuration or a database.
	t.Setenv("DB_DSN", "")
	t.Setenv("ADMIN_API_KEY", "")
	for _, arg := range []string{"version", "--version"} {
		if code := Run([]string{arg}, nil); code != 0 {
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
	if code := Run(nil, nil); code == 0 {
		t.Fatal("Run succeeded with an unknown database address")
	}
	if !strings.Contains(logs.String(), "schema check:") {
		t.Fatalf("Run did not reach the database: %s", logs.String())
	}
}

func TestBuildRejectsTwoExtensions(t *testing.T) {
	app, err := Build(config.Config{}, []*extension.Extension{{Name: "first"}, {Name: "second"}})
	if app != nil || err == nil || !strings.Contains(err.Error(), "at most one extension") {
		t.Fatalf("Build with two extensions = %v, %v", app, err)
	}
}
