package servertest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestHackMissingColumnStopsStartup(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	database, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	exts := []*extension.Extension{hack.Extension()}
	t.Setenv("DB_DSN", cfg.DBDSN)
	platformtest.RecordProduct(t, cfg, exts)
	// Record the current migrations before damaging the schema so migrating
	// again cannot repair the missing column and hide a missing product check.
	if code := server.Run([]string{"migrate", "-record-product"}, exts, nil); code != 0 {
		t.Fatalf("migrate complete schema: exit %d", code)
	}
	if _, err := database.Exec(`ALTER TABLE events DROP COLUMN theme`); err != nil {
		t.Fatal(err)
	}
	const want = "schema check (EVENTS=hosted): database is behind this build; missing: events.theme\n" +
		"Run `simple-host migrate` against this database (on a small box, re-running the install\n" +
		"command does it), or apply the files in host/db/migrations/ by hand and record each with\n" +
		"`simple-host migrate -mark FILE`. A new database takes host/db/schema.sql"
	if err := exts[0].CheckSchema(context.Background(), database); err == nil || err.Error() != want {
		t.Fatalf("product schema error = %v; want %s", err, want)
	}
	platformtest.RecordProduct(t, cfg, exts)
	app, err := server.Build(cfg, exts, nil)
	if app != nil {
		app.Close()
		t.Fatal("Hack started with a missing column")
	}
	if err == nil || err.Error() != want {
		t.Fatalf("startup error = %v; want %s", err, want)
	}
	for _, args := range [][]string{{"migrate"}, {"migrate", "-init"}} {
		if code := server.Run(args, exts, nil); code != 1 {
			t.Fatalf("%v with missing Hack column: exit %d; want 1", args, code)
		}
	}
	cfg.MigrateOnStart = true
	app, err = server.Build(cfg, exts, nil)
	if app != nil {
		app.Close()
		t.Fatal("Hack migrated and started with a missing column")
	}
	if err == nil || err.Error() != "migrate on start: "+want {
		t.Fatalf("migrate-on-start error = %v", err)
	}
	// The same database is still sufficient for the platform alone.
	cfg.MigrateOnStart = false
	app = platformtest.Build(t, cfg, nil)
	if app == nil {
		t.Fatal("platform did not start without a product hook")
	}
}
