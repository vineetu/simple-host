package servertest

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vsriram/simple-host/products/hack/testdb"
)

// Exercise the real entry points: neither environment selection nor a lost
// Run return code may turn the wrong program into a successful start.
func TestProgramsRefuseOtherProduct(t *testing.T) {
	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", dir+"/", "../../../cmd/simple-host", "../../../cmd/simple-hack")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	for _, product := range []string{"simple-host", "simple-hack"} {
		t.Run(product, func(t *testing.T) {
			dsn := testdb.Fresh(t)
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			other := "simple-host"
			if product == other {
				other = "simple-hack"
			}
			if _, err := db.Exec(`INSERT INTO install_product(product) VALUES ($1)`, other); err != nil {
				t.Fatal(err)
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "DB_DSN=" + dsn,
				"ADMIN_API_KEY=program-fixture-only", "SITE_DOMAIN=program.example.test",
				"DATA_DIR=" + t.TempDir(), "GEOIP_DIR=" + t.TempDir()}
			eventsValues := []string{""}
			if product == "simple-hack" {
				eventsValues = append(eventsValues, "hosted")
			}
			for _, events := range eventsValues {
				for _, migrate := range []string{"false", "true"} {
					for _, args := range [][]string{nil, {"migrate"}, {"migrate", "-init"}, {"migrate", "-record-product"}} {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
						cmd := exec.CommandContext(ctx, filepath.Join(dir, product), args...)
						cmd.Env = append(append([]string{}, env...), "EVENTS="+events, "MIGRATE_ON_START="+migrate)
						out, err := cmd.CombinedOutput()
						cancel()
						exit, ok := err.(*exec.ExitError)
						want := fmt.Sprintf("this install was set up as %s; this program is %s", other, product)
						if !ok || exit.ExitCode() != 3 || !strings.Contains(string(out), want) {
							t.Fatalf("EVENTS=%q MIGRATE_ON_START=%s args=%v: exit=%v output=%s", events, migrate, args, err, out)
						}
					}
				}
			}
		})
	}
}
