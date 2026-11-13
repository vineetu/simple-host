package server

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vsriram/simple-host/db/migrations"
	"github.com/vsriram/simple-host/internal/extension"
	"github.com/vsriram/simple-host/internal/testdb"
)

func TestMigrationStatusOutput(t *testing.T) {
	at := time.Date(2026, 11, 13, 1, 2, 3, 0, time.UTC)
	entries := []migrations.Entry{
		{Name: "historical.sql", State: "baseline"},
		{Name: "legacy.sql", State: "applied", AppliedAt: at},
		{Name: "0002-next.sql", State: "pending"},
	}
	// Keep the old formatting byte for byte when all recorded names are embedded.
	want := fmt.Sprintf("%-9s %-45s %s\n%-9s %-45s %s\n%-9s %-45s %s\n1 pending\n",
		"baseline", "historical.sql", "", "applied", "legacy.sql", "2026-11-13T01:02:03Z", "pending", "0002-next.sql", "")
	var out bytes.Buffer
	writeMigrationStatus(&out, entries)
	if out.String() != want {
		t.Fatalf("status = %q; want %q", out.String(), want)
	}
	entries = append(entries, migrations.Entry{Name: "external.sql", State: "applied", AppliedAt: at, Unknown: true}, migrations.Entry{Name: "0099-external.sql", State: "applied", AppliedAt: at, Unknown: true})
	out.Reset()
	writeMigrationStatus(&out, entries)
	want += "2 recorded files are not part of this program\n"
	if out.String() != want {
		t.Fatalf("unknown status = %q; want %q", out.String(), want)
	}
}

func TestMigrationSet(t *testing.T) {
	platform := migrationSet(nil)
	if platform.Product != nil || platform.Platform.Schema == "" || len(platform.Tracked()) == 0 {
		t.Fatal("missing platform catalogue")
	}
	product := &migrations.Catalog{FS: fstest.MapFS{}, Schema: "product schema"}
	order := []string{"platform.sql", "product.sql"}
	set := migrationSet([]*extension.Extension{{Migrations: product, MigrationOrder: order}})
	if set.Product != product || !reflect.DeepEqual(set.ProductOrder, order) {
		t.Fatal("extension catalogue was not composed")
	}
	for _, exts := range [][]*extension.Extension{nil, {nil}, {{Name: "no catalogue"}}} {
		if got := migrationSet(exts).Tracked(); !reflect.DeepEqual(got, platform.Tracked()) {
			t.Fatalf("nil catalogue changed platform order: %v", got)
		}
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
	if code := Run([]string{"migrate", "-mark", name}, exts); code != 0 {
		t.Fatalf("mark product exit = %d", code)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM schema_migrations WHERE name=$1`, name).Scan(&count); err != nil || count != 1 {
		t.Fatalf("product was not marked: %d, %v", count, err)
	}
	if _, err := database.Exec(`DELETE FROM schema_migrations WHERE name=$1`, name); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"migrate"}, exts); code != 0 {
		t.Fatalf("migrate product exit = %d", code)
	}
	var value string
	if err := database.QueryRow(`SELECT value FROM instance_config WHERE key='catalog-product'`).Scan(&value); err != nil || value != "ran" {
		t.Fatalf("product was not applied: %q, %v", value, err)
	}
	// Startup uses this same composed set and must tolerate recorded product
	// files when it is subsequently run with only the platform catalogue.
	if err := migrateDatabase(context.Background(), database, migrationSet(nil), true, t.Logf); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"migrate", "-mark", name}, nil); code != 1 {
		t.Fatalf("platform accepted absent product file: exit %d", code)
	}
	product.FS.(fstest.MapFS)["catalog-failure.sql"] = &fstest.MapFile{Data: []byte("SELECT no_such_catalog_function()")}
	product.Legacy = append(product.Legacy, "catalog-failure.sql")
	exts[0].MigrationOrder = append(exts[0].MigrationOrder, "catalog-failure.sql")
	if err := migrateDatabase(context.Background(), database, migrationSet(exts), false, t.Logf); err == nil || !strings.Contains(err.Error(), "catalog-failure.sql") {
		t.Fatalf("product failure = %v", err)
	}
}
