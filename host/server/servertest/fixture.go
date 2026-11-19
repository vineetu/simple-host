// Package servertest provides product-independent fixtures for the composed server.
package servertest

import (
	"database/sql"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
)

// Build constructs the program without starting workers and closes it after the test.
// Callers supply the extensions; this package imports no product.
func Build(t *testing.T, cfg config.Config, exts []*extension.Extension) *server.App {
	t.Helper()
	RecordProduct(t, cfg, exts)
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
	for _, key := range []string{"RESEND_API_KEY", "PHOTODNA_API_KEY", "PHOTODNA_API_KEY_FILE", "EVENT_DNS_TOKEN", "EVENT_NAME_PEER", "ANALYTICS_LOG", "ASSISTANT_BASE_URL", "ASSISTANT_MODEL", "HACK_DEMO_EVENT_SLUG"} {
		t.Setenv(key, "")
	}
	t.Setenv("PAPER_METRICS", "on")
	t.Setenv("MIGRATE_ON_START", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	RecordProduct(t, cfg, nil)
	return cfg
}

// RecordProduct explicitly sets the identity on a disposable fixture. Some hook
// tests intentionally reuse a schema with and without a product; that is no
// longer a supported operator transition, so those tests set their record here.
func RecordProduct(t *testing.T, cfg config.Config, exts []*extension.Extension) {
	t.Helper()
	product := "simple-host"
	if len(exts) == 1 && exts[0] != nil && exts[0].Name != "" {
		product = exts[0].Name
	}
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO install_product (product) VALUES ($1) ON CONFLICT ("only") DO UPDATE SET product=EXCLUDED.product`, product); err != nil {
		t.Fatal(err)
	}
}
