package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStaticConfig(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://unreachable.invalid/test")
	t.Setenv("ADMIN_API_KEY", "")
	t.Setenv("DATA_DIR", filepath.Join(t.TempDir(), "sites"))
	for _, key := range []string{"STATIC_BIND", "STATIC_PORT", "STATIC_API_URL", "STATIC_STATE_DIR", "STATIC_REFRESH_SECONDS"} {
		t.Setenv(key, "")
	}
	cfg, err := LoadStatic()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "127.0.0.1" || cfg.Port != "8091" || cfg.RefreshInterval != 10*time.Second || cfg.StateDir != filepath.Join(filepath.Dir(cfg.DataDir), "static-state") {
		t.Fatalf("wrong defaults: bind %s port %s refresh %s state %s", cfg.Bind, cfg.Port, cfg.RefreshInterval, cfg.StateDir)
	}
	for _, tc := range []struct{ key, value string }{{"STATIC_PORT", "0"}, {"STATIC_PORT", "65536"}, {"STATIC_REFRESH_SECONDS", "0"}, {"STATIC_REFRESH_SECONDS", "invalid"}, {"STATIC_API_URL", "file:///tmp/api"}} {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := LoadStatic(); err == nil {
				t.Fatal("invalid setting accepted")
			}
		})
	}
}
