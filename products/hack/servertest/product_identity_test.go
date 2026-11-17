package servertest

import (
	"database/sql"
	"os/exec"
	"strings"
	"testing"

	"github.com/vsriram/simple-host/host/extension"
	"github.com/vsriram/simple-host/host/server"
	platformtest "github.com/vsriram/simple-host/host/server/servertest"
	"github.com/vsriram/simple-host/products/hack"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func identitySnapshot(t *testing.T, dsn string) string {
	t.Helper()
	b, err := exec.Command("pg_dump", "--dbname", dsn, "--no-owner", "--no-privileges").Output()
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

func TestProductIdentityRefusesOtherDatabase(t *testing.T) {
	for _, recorded := range []string{"simple-host", "simple-hack"} {
		t.Run(recorded, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			db, err := sql.Open("postgres", cfg.DBDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`UPDATE install_product SET product=$1`, recorded); err != nil {
				t.Fatal(err)
			}
			var exts []*extension.Extension
			if recorded == "simple-host" {
				exts = []*extension.Extension{hack.Extension()}
			}
			before := identitySnapshot(t, cfg.DBDSN)
			for _, args := range [][]string{{"migrate"}, {"migrate", "-init"}, {"migrate", "-record-product"}, {"migrate", "-mark", "0023-install-product.sql"}} {
				if code := server.Run(args, exts); code != 3 {
					t.Fatalf("%v exit=%d", args, code)
				}
			}
			for _, onStart := range []bool{false, true} {
				cfg.MigrateOnStart = onStart
				app, err := server.Build(cfg, exts)
				if app != nil {
					app.Close()
					t.Fatal("wrong program started")
				}
				if err == nil || !strings.Contains(err.Error(), "this install was set up as "+recorded) {
					t.Fatalf("startup: %v", err)
				}
				t.Setenv("MIGRATE_ON_START", map[bool]string{false: "false", true: "true"}[onStart])
				if code := server.Run(nil, exts); code != 3 {
					t.Fatalf("start exit=%d", code)
				}
			}
			if after := identitySnapshot(t, cfg.DBDSN); after != before {
				t.Fatal("refusal changed schema, ledger or data rows")
			}
		})
	}
}

func TestRecordProductContradictingEvidence(t *testing.T) {
	for _, product := range []string{"simple-host", "simple-hack"} {
		t.Run(product, func(t *testing.T) {
			cfg := platformtest.Config(t, testdb.Fresh(t))
			db, err := sql.Open("postgres", cfg.DBDSN)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec(`DROP TABLE install_product`); err != nil {
				t.Fatal(err)
			}
			var exts []*extension.Extension
			if product == "simple-host" {
				// A minimal owned fixture supplies the one fact the probe reads.
				if _, err := db.Exec(`DROP TABLE events CASCADE; CREATE TABLE events(id int); INSERT INTO events VALUES (1)`); err != nil {
					t.Fatal(err)
				}
			} else {
				exts = []*extension.Extension{hack.Extension()}
				if _, err := db.Exec(`DROP TABLE events CASCADE`); err != nil {
					t.Fatal(err)
				}
			}
			before := identitySnapshot(t, cfg.DBDSN)
			for _, args := range [][]string{{"migrate", "-record-product"}, {"migrate", "-init", "-record-product"}} {
				if code := server.Run(args, exts); code != 3 {
					t.Fatalf("%v exit=%d", args, code)
				}
			}
			if after := identitySnapshot(t, cfg.DBDSN); after != before {
				t.Fatal("contradictory evidence refusal wrote to database")
			}
		})
	}
}

func TestHackExplicitProductRecording(t *testing.T) {
	cfg := platformtest.Config(t, testdb.Fresh(t))
	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE install_product`); err != nil {
		t.Fatal(err)
	}
	exts := []*extension.Extension{hack.Extension()}
	for i := 0; i < 2; i++ {
		if code := server.Run([]string{"migrate", "-init", "-record-product"}, exts); code != 0 {
			t.Fatalf("record/repeat exit=%d", code)
		}
	}
	var count int
	var product string
	if err := db.QueryRow(`SELECT count(*), min(product) FROM install_product`).Scan(&count, &product); err != nil || count != 1 || product != "simple-hack" {
		t.Fatalf("%d %q %v", count, product, err)
	}
}
