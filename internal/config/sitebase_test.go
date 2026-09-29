package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A split base with the move on must have its certificate hand-off, or every
// person would count as ready under the base; the service refuses to start.
func TestSiteBaseCertDirRequired(t *testing.T) {
	good := t.TempDir()
	for _, d := range []string{"ready", "requests"} {
		if err := os.Mkdir(filepath.Join(good, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	noRequests := t.TempDir()
	if err := os.Mkdir(filepath.Join(noRequests, "ready"), 0o755); err != nil {
		t.Fatal(err)
	}
	fileReady := t.TempDir()
	if err := os.Mkdir(filepath.Join(fileReady, "requests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fileReady, "ready"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		base, move, dir string
		wantErr         string
	}{
		{"", "", "", ""},
		{"", "serve", "", ""},                    // base defaults to SITE_DOMAIN
		{"simple-host.app", "permanent", "", ""}, // same domain
		{"simple-host.site", "off", "", ""},      // move off
		{"simple-host.site", "bogus", "", ""},    // unknown mode is off
		{"u.simple-host.app", "serve", "", ""},   // nested: the base is ignored
		{"simple-host.site", "serve", good, ""},
		{"simple-host.site", "serve", "", "SITE_BASE_CERT_DIR is required"},
		{"simple-host.site", "canonical", "", "SITE_BASE_CERT_DIR is required"},
		{"Simple-Host.Site.", "permanent", "  ", "SITE_BASE_CERT_DIR is required"},
		{"simple-host.site", "serve", noRequests, "requests/ is missing"},
		{"simple-host.site", "serve", filepath.Join(good, "nope"), "ready/ is missing"},
		{"simple-host.site", "serve", fileReady, "is not a directory"},
	} {
		t.Setenv("DB_DSN", "postgres://test")
		t.Setenv("ADMIN_API_KEY", "test-key")
		t.Setenv("SITE_DOMAIN", "simple-host.app")
		t.Setenv("SITE_BASE_DOMAIN", c.base)
		t.Setenv("SITE_BASE_MOVE", c.move)
		t.Setenv("SITE_BASE_CERT_DIR", c.dir)
		_, err := Load()
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("base=%q move=%q dir=%q: unexpected error %v", c.base, c.move, c.dir, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("base=%q move=%q dir=%q: error %v, want one containing %q", c.base, c.move, c.dir, err, c.wantErr)
		}
	}
}
