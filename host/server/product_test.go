package server

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/config"
	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/testdb"
)

func productDB(t *testing.T, empty bool) (*sql.DB, string) {
	t.Helper()
	dsn := testdb.Fresh(t)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if empty {
		productExec(t, db, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	}
	return db, dsn
}
func productExec(t *testing.T, db *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := db.Exec(statement, args...); err != nil {
		t.Fatal(err)
	}
}
func productConfig(t *testing.T, dsn string) config.Config {
	t.Helper()
	t.Setenv("DB_DSN", dsn)
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("GEOIP_DIR", t.TempDir())
	t.Setenv("ADMIN_API_KEY", "product-test-only")
	t.Setenv("SITE_DOMAIN", "product.example.test")
	t.Setenv("MIGRATE_ON_START", "false")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func productSnapshot(t *testing.T, dsn string) string {
	t.Helper()
	// Include every data row (including the ledger) and every schema object.
	// pg_dump's random psql restriction token is the only variable output.
	cmd := exec.Command("pg_dump", "--dbname", dsn, "--no-owner", "--no-privileges")
	b, err := cmd.Output()
	if err != nil {
		t.Fatal("pg_dump failed")
	}
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, `\restrict `) || strings.HasPrefix(line, `\unrestrict `) || strings.HasPrefix(line, "-- Dumped") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func TestProductRecordStates(t *testing.T) {
	for _, state := range []string{"matching", "other", "missing-table"} {
		t.Run(state, func(t *testing.T) {
			db, dsn := productDB(t, false)
			cfg := productConfig(t, dsn)
			switch state {
			case "matching":
				productExec(t, db, `INSERT INTO install_product(product, release) VALUES ('simple-host', 'kept-release')`)
			case "other":
				productExec(t, db, `INSERT INTO install_product(product) VALUES ('another-product')`)
			case "missing-table":
				productExec(t, db, `DROP TABLE install_product`)
			}
			if state == "matching" {
				if err := migrateDatabase(context.Background(), db, nil, true, t.Logf, false); err != nil {
					t.Fatal(err)
				}
				var release string
				if err := db.QueryRow(`SELECT release FROM install_product`).Scan(&release); err != nil || release != "kept-release" {
					t.Fatalf("ordinary migrate changed release: %q err=%v", release, err)
				}
				return
			}
			before := productSnapshot(t, dsn)
			for _, args := range [][]string{{"migrate"}, {"migrate", "-init"}, {"migrate", "-mark", "0023-install-product.sql"}} {
				if code := Run(args, nil, nil); code != 3 {
					t.Fatalf("%v exit %d", args, code)
				}
			}
			for _, onStart := range []bool{false, true} {
				cfg.MigrateOnStart = onStart
				app, err := Build(cfg, nil, nil)
				if app != nil {
					app.Close()
					t.Fatal("unexpected startup")
				}
				if err == nil || productExitCode(err) != 3 {
					t.Fatalf("migrateOnStart=%v: %v", onStart, err)
				}
				t.Setenv("MIGRATE_ON_START", fmt.Sprint(onStart))
				if code := Run(nil, nil, nil); code != 3 {
					t.Fatalf("start exit %d", code)
				}
			}
			if after := productSnapshot(t, dsn); after != before {
				t.Fatal("refusal changed data, ledger or schema")
			}
			if code := Run([]string{"migrate", "-status"}, nil, nil); code != 0 {
				t.Fatalf("status exit %d", code)
			}
			if state == "other" {
				if code := Run([]string{"migrate", "-record-product"}, nil, nil); code != 3 {
					t.Fatalf("adopting another product exit %d", code)
				}
			} else {
				if code := Run([]string{"migrate", "-init", "-record-product"}, nil, nil); code != 0 {
					t.Fatalf("explicit adoption exit %d", code)
				}
				if code := Run([]string{"migrate", "-record-product"}, nil, nil); code != 0 {
					t.Fatalf("repeat adoption exit %d", code)
				}
			}
		})
	}
}

// Two different products starting at once on an empty database: exactly one
// claims it, the other is refused, and one product record exists.
func TestConcurrentFirstStarts(t *testing.T) {
	db, dsn := productDB(t, true)
	cfg := productConfig(t, dsn)
	cfg.MigrateOnStart = true
	cfg.SiteDomainSet = false // setup mode: no workers or provider traffic
	type result struct {
		app *App
		err error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, exts := range [][]*extension.Extension{nil, {{Name: "another-product"}}} {
		go func(exts []*extension.Extension) {
			<-start
			app, err := Build(cfg, exts, nil)
			results <- result{app, err}
		}(exts)
	}
	close(start)
	running, refused := 0, 0
	for i := 0; i < 2; i++ {
		r := <-results
		if r.app != nil {
			running++
			r.app.Close()
		} else if r.err != nil && productExitCode(r.err) == 3 {
			refused++
		} else {
			t.Errorf("first start: %v", r.err)
		}
	}
	if running != 1 || refused != 1 {
		t.Fatalf("running=%d refused=%d", running, refused)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM install_product`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("record count=%d: %v", count, err)
	}
}
