package db

import (
	"context"
	"database/sql"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	schema "github.com/vsriram/simple-host/db"
	"github.com/vsriram/simple-host/db/migrations"
	operationrepo "github.com/vsriram/simple-host/internal/repository/operations"
	productmigrations "github.com/vsriram/simple-host/products/hack/db/migrations"
	"github.com/vsriram/simple-host/products/hack/testdb"
)

func TestProductCatalogOrder(t *testing.T) {
	product := Catalog()
	set := migrations.Set{Platform: schema.Catalog(), Product: &product, ProductOrder: productmigrations.ProductOrder}
	var platform, hack []string
	for _, name := range set.ProductOrder {
		if _, err := set.Platform.FS.ReadFile(name); err == nil {
			platform = append(platform, name)
		} else {
			hack = append(hack, name)
		}
	}
	if !reflect.DeepEqual(platform, set.Platform.Legacy) || !reflect.DeepEqual(hack, product.Legacy) {
		t.Fatal("catalogue legacy lists are not subsequences of the frozen combined order")
	}
	frozen, err := os.ReadFile("migrations/testdata/combined-legacy.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(strings.Fields(string(frozen)), set.ProductOrder) {
		t.Fatal("combined legacy order changed")
	}
	numbers := map[int]string{}
	for _, catalog := range []migrations.Catalog{set.Platform, product} {
		entries, err := fs.ReadDir(catalog.FS, ".")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, ".sql") {
				continue
			}
			if n, err := strconv.Atoi(strings.SplitN(name, "-", 2)[0]); err == nil {
				if previous := numbers[n]; previous != "" {
					t.Fatalf("duplicate migration number: %s, %s", previous, name)
				}
				numbers[n] = name
			}
		}
	}
}

// The standalone platform schema must load without any product object, even
// before migrations run. Every authoritative table/function/trigger is checked.
func TestPlatformSchemaAlone(t *testing.T) {
	db := emptyDatabase(t)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatal(err)
	}
	if err := operationrepo.VerifySchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	objects, err := os.ReadFile("../../../scripts/split/hack-objects.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(objects), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		kind, name := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		var found bool
		var query string
		switch kind {
		case "table":
			query = `SELECT to_regclass('public.' || $1) IS NOT NULL`
		case "function":
			query = `SELECT EXISTS (SELECT 1 FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname=$1)`
		case "trigger":
			query = `SELECT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname=$1 AND NOT tgisinternal)`
		case "column":
			query = `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='public' AND table_name=split_part($1,'.',1) AND column_name=split_part($1,'.',2))`
		default:
			t.Fatalf("unknown object kind %s", kind)
		}
		if err := db.QueryRow(query, name).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found {
			t.Errorf("platform schema contains %s %s", kind, name)
		}
	}
}

func emptyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", testdb.Fresh(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	return db
}

// Hosted Host keeps its existing product objects and ledger rows. Both Apply
// calls and the platform schema check must leave that complete state intact.
func TestPlatformOnExistingCombinedDatabase(t *testing.T) {
	db := emptyDatabase(t)
	ctx := context.Background()
	product := Catalog()
	combined := migrations.Set{Platform: schema.Catalog(), Product: &product, ProductOrder: productmigrations.ProductOrder}
	if _, err := combined.Init(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := combined.Apply(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var state string
		if err := db.QueryRow(`SELECT string_agg(name || ':' || applied_at::text, ',' ORDER BY name) FROM schema_migrations`).Scan(&state); err != nil {
			t.Fatal(err)
		}
		var columns string
		if err := db.QueryRow(`SELECT string_agg(table_name || '.' || column_name || ':' || data_type, ',' ORDER BY table_name,column_name) FROM information_schema.columns WHERE table_schema='public'`).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		return state + columns
	}
	before := snapshot()
	platform := migrations.Set{Platform: schema.Catalog()}
	for range 2 {
		if ran, err := platform.Apply(ctx, db, nil); err != nil || len(ran) > 0 {
			t.Fatalf("platform Apply: %v, %v", ran, err)
		}
		if err := operationrepo.VerifySchema(ctx, db); err != nil {
			t.Fatal(err)
		}
		if snapshot() != before {
			t.Fatal("platform changed existing composed schema or ledger")
		}
	}
	entries, err := platform.Status(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	unknown := 0
	for _, entry := range entries {
		if entry.Unknown {
			unknown++
		}
	}
	if unknown != len((migrations.Set{Platform: product}).Tracked()) {
		t.Fatalf("unknown product history: %d", unknown)
	}
}

// The copy is the exact platform statement from the unchanged mixed file.
func TestPlatformPartsCopy(t *testing.T) {
	original, err := Catalog().FS.ReadFile("zs-hack-paper-metrics.sql")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := schema.Catalog().FS.ReadFile("0022-platform-parts.sql")
	if err != nil {
		t.Fatal(err)
	}
	statement := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS paper_run \(.*?\n\);`)
	if !reflect.DeepEqual(statement.Find(original), statement.Find(copy)) || statement.Find(copy) == nil {
		t.Fatal("platform paper_run statement changed")
	}
}
