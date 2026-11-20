package server

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/vsriram/simple-host/host/buildinfo"
	"github.com/vsriram/simple-host/host/db/migrations"
	"github.com/vsriram/simple-host/host/extension"
	operationrepo "github.com/vsriram/simple-host/host/ops/repo"
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

func TestMigrateSchemaCheck(t *testing.T) {
	database, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	for _, exts := range [][]*extension.Extension{nil, {nil}, {{}}} {
		if err := migrateDatabase(ctx, database, exts, true, t.Logf, true); err != nil {
			t.Fatalf("nil hook: %v", err)
		}
	}
	wantErr := errors.New("product schema missing")
	calls := 0
	exts := []*extension.Extension{{CheckSchema: func(ctx context.Context, db *sql.DB) error {
		calls++
		if db != database {
			t.Fatal("product check received a different database")
		}
		if err := operationrepo.VerifySchema(ctx, db); err != nil {
			t.Fatalf("product check ran before platform schema was ready: %v", err)
		}
		var migrationsRecorded int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&migrationsRecorded); err != nil || migrationsRecorded == 0 {
			t.Fatalf("product check ran before migrations: %d, %v", migrationsRecorded, err)
		}
		return wantErr
	}}}
	if err := migrateDatabase(ctx, database, exts, false, t.Logf, false); !errors.Is(err, wantErr) || calls != 1 {
		t.Fatalf("product schema failure: %v, calls=%d", err, calls)
	}
	if _, err := database.Exec(`ALTER TABLE account_passwords DROP COLUMN login`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(ctx, database, exts, false, t.Logf, false); err == nil || !strings.Contains(err.Error(), "account_passwords.login") || calls != 1 {
		t.Fatalf("platform check must fail before product check: err=%v, calls=%d", err, calls)
	}
}
