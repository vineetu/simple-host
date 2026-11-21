package server

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/db/migrations"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/testdb"
)

func TestFirstInstallReleaseResumesAfterSchemaFailure(t *testing.T) {
	database, _ := productDB(t, true)
	ctx := context.Background()
	wantErr := errors.New("interrupted after schema creation")
	exts := []*extension.Extension{{CheckSchema: func(context.Context, *sql.DB) error { return wantErr }}}
	if err := migrateDatabase(ctx, database, exts, true, t.Logf, true); !errors.Is(err, wantErr) {
		t.Fatalf("first install failure: %v", err)
	}
	var release, pending string
	if err := database.QueryRow(`SELECT release FROM install_product`).Scan(&release); err != nil || release != "" {
		t.Fatalf("incomplete first install release=%q err=%v", release, err)
	}
	if err := database.QueryRow(`SELECT value FROM instance_config WHERE key='first_install_pending_release'`).Scan(&pending); err != nil || pending != "simple-host" {
		t.Fatalf("missing durable first-install marker: %q %v", pending, err)
	}
	if err := migrateDatabase(ctx, database, nil, true, t.Logf, true); err != nil {
		t.Fatal(err)
	}
	var dated bool
	if err := database.QueryRow(`SELECT release, release_at IS NOT NULL FROM install_product`).Scan(&release, &dated); err != nil || release != buildinfo.Version || !dated {
		t.Fatalf("resumed release=%q dated=%v err=%v", release, dated, err)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM instance_config WHERE key='first_install_pending_release'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("marker not cleared: %d %v", count, err)
	}
}

func TestAdoptionDoesNotStampFirstInstallRelease(t *testing.T) {
	database, _ := productDB(t, false)
	if err := migrateDatabase(context.Background(), database, nil, true, t.Logf, true); err != nil {
		t.Fatal(err)
	}
	var release string
	var dated bool
	if err := database.QueryRow(`SELECT release, release_at IS NOT NULL FROM install_product`).Scan(&release, &dated); err != nil || release != "" || dated {
		t.Fatalf("adoption stamped release=%q dated=%v err=%v", release, dated, err)
	}
}

func TestMigrateCommandUsesProductCatalog(t *testing.T) {
	if os.Getenv("DB_DSN") == "" {
		t.Skip("DB_DSN not set")
	}
	dsn := testdb.Fresh(t)
	t.Setenv("DB_DSN", dsn)
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	const name = "catalog-product.sql"
	product := &migrations.Catalog{FS: fstest.MapFS{name: {Data: []byte(`INSERT INTO instance_config(key,value) VALUES ('catalog-product','ran')`)}}, Legacy: []string{name}}
	exts := []*extension.Extension{{Migrations: product, MigrationOrder: append(append([]string{}, migrations.Legacy...), name)}}
	if err := migrateDatabase(context.Background(), database, exts, false, t.Logf, true); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM schema_migrations WHERE name=$1`, name); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"migrate", "-mark", name}, exts, nil); code != 0 {
		t.Fatalf("mark product exit = %d", code)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM schema_migrations WHERE name=$1`, name).Scan(&count); err != nil || count != 1 {
		t.Fatalf("product was not marked: %d, %v", count, err)
	}
	if _, err := database.Exec(`DELETE FROM schema_migrations WHERE name=$1`, name); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM instance_config WHERE key='catalog-product'`); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"migrate"}, exts, nil); code != 0 {
		t.Fatalf("migrate product exit = %d", code)
	}
	var value string
	if err := database.QueryRow(`SELECT value FROM instance_config WHERE key='catalog-product'`).Scan(&value); err != nil || value != "ran" {
		t.Fatalf("product was not applied: %q, %v", value, err)
	}
	// Startup uses this same composed set and must tolerate recorded product
	// files when it is subsequently run with only the platform catalogue.
	if err := migrateDatabase(context.Background(), database, nil, true, t.Logf, false); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"migrate", "-mark", name}, nil, nil); code != 1 {
		t.Fatalf("platform accepted absent product file: exit %d", code)
	}
	product.FS.(fstest.MapFS)["catalog-failure.sql"] = &fstest.MapFile{Data: []byte("SELECT no_such_catalog_function()")}
	product.Legacy = append(product.Legacy, "catalog-failure.sql")
	exts[0].MigrationOrder = append(exts[0].MigrationOrder, "catalog-failure.sql")
	if err := migrateDatabase(context.Background(), database, exts, false, t.Logf, false); err == nil || !strings.Contains(err.Error(), "catalog-failure.sql") {
		t.Fatalf("product failure = %v", err)
	}
}
