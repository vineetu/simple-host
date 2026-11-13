package migrations

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func twoCatalogs() Set {
	platform := Catalog{
		FS:       fstest.MapFS{},
		Legacy:   []string{"platform-z.sql", "platform-a.sql"},
		Baseline: map[string]bool{"platform-history.sql": true},
		Schema: `CREATE TABLE users (id integer PRIMARY KEY);
   CREATE TABLE migration_trace (seq serial, name text NOT NULL);
   INSERT INTO users VALUES (1);`,
	}
	product := Catalog{
		FS:       fstest.MapFS{},
		Legacy:   []string{"product-z.sql", "product-a.sql"},
		Baseline: map[string]bool{"product-history.sql": true},
		Schema: `CREATE TABLE product_fixture (user_id integer REFERENCES users(id));
   INSERT INTO product_fixture SELECT id FROM users;`,
	}
	for _, catalog := range []*Catalog{&platform, &product} {
		files := catalog.FS.(fstest.MapFS)
		for _, name := range catalog.Legacy {
			files[name] = &fstest.MapFile{Data: []byte(`INSERT INTO migration_trace(name) VALUES ('` + name + `')`)}
		}
		for name := range catalog.Baseline {
			files[name] = &fstest.MapFile{Data: []byte("THIS MUST NEVER RUN")}
		}
	}
	for i, name := range []string{"0003-platform.sql", "0004-product.sql", "0005-platform.sql", "0006-product.sql"} {
		catalog := &platform
		if i%2 == 1 {
			catalog = &product
		}
		catalog.FS.(fstest.MapFS)[name] = &fstest.MapFile{Data: []byte(`INSERT INTO migration_trace(name) VALUES ('` + name + `')`)}
	}
	return Set{Platform: platform, Product: &product,
		ProductOrder: []string{"product-z.sql", "platform-z.sql", "product-a.sql", "platform-a.sql"}}
}

func assertCatalogOrder(t *testing.T, set Set) {
	t.Helper()
	var platformOrder []string
	for _, name := range set.ProductOrder {
		if slices.Contains(set.Platform.Legacy, name) {
			platformOrder = append(platformOrder, name)
		}
	}
	if !reflect.DeepEqual(platformOrder, set.Platform.Legacy) {
		t.Fatalf("platform legacy %v is not a subsequence of combined order %v", set.Platform.Legacy, set.ProductOrder)
	}
	numbers := map[int]string{}
	for _, catalog := range set.catalogs() {
		names, err := fs.ReadDir(catalog.FS, ".")
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range names {
			name := entry.Name()
			if n := number(name); n > 0 {
				if prev, ok := numbers[n]; ok {
					t.Fatalf("duplicate migration number: %s and %s", prev, name)
				}
				numbers[n] = name
			}
		}
	}
}

func TestCatalogOrder(t *testing.T) {
	set := twoCatalogs()
	assertCatalogOrder(t, set)
	want := append(slices.Clone(set.ProductOrder), "0003-platform.sql", "0004-product.sql", "0005-platform.sql", "0006-product.sql")
	if got := set.Tracked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tracked = %v; want %v", got, want)
	}
	set.Product = nil
	if got, want := set.Tracked(), []string{"platform-z.sql", "platform-a.sql", "0003-platform.sql", "0005-platform.sql"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("platform tracked = %v; want %v", got, want)
	}
}

// Partition the real catalogue without changing file names or bytes, proving
// composition preserves today's complete order as well as the synthetic case.
func TestEmbeddedCatalogComposition(t *testing.T) {
	original := Set{Platform: Embedded()}
	set := Set{Platform: Catalog{FS: fstest.MapFS{}, Baseline: Baseline},
		Product: &Catalog{FS: fstest.MapFS{}, Baseline: Baseline}, ProductOrder: slices.Clone(Legacy)}
	names, err := fs.Glob(embedded, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		catalog := &set.Platform
		if i%2 == 1 {
			catalog = set.Product
		}
		data, err := embedded.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		catalog.FS.(fstest.MapFS)[name] = &fstest.MapFile{Data: data}
	}
	for _, name := range Legacy {
		catalog := &set.Platform
		if _, ok := catalog.FS.(fstest.MapFS)[name]; !ok {
			catalog = set.Product
		}
		catalog.Legacy = append(catalog.Legacy, name)
	}
	assertCatalogOrder(t, set)
	if got, want := set.Tracked(), original.Tracked(); !reflect.DeepEqual(got, want) {
		t.Fatalf("composed = %v; want %v", got, want)
	}
}

// Called on the empty migration-test database before the existing full-schema
// tests. Cleanup removes only these fixture objects, leaving it empty again.
func testCatalogsAgainstPostgres(t *testing.T, db *sql.DB) {
	ctx := context.Background()
	set := twoCatalogs()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		exec(`DROP TABLE IF EXISTS product_fixture, migration_trace, users, schema_migrations`)
	})
	exec(createTable)
	exec(`INSERT INTO schema_migrations(name) VALUES ('external-legacy.sql'), ('0001-external.sql')`)
	t.Run("init rollback", func(t *testing.T) {
		broken := set
		product := *set.Product
		product.Schema += ` SELECT missing_catalog_function();`
		broken.Product = &product
		if built, err := broken.Init(ctx, db, nil); err == nil || built {
			t.Fatalf("Init = %v, %v", built, err)
		}
		var empty bool
		if err := db.QueryRow(`SELECT to_regclass('public.users') IS NULL AND to_regclass('public.product_fixture') IS NULL AND to_regclass('public.migration_trace') IS NULL`).Scan(&empty); err != nil || !empty {
			t.Fatalf("both schemas must roll back: empty=%v, err=%v", empty, err)
		}
	})
	t.Run("init order and concurrent starts", func(t *testing.T) {
		var wg sync.WaitGroup
		built, errs := make([]bool, 2), make([]error, 2)
		for i := range built {
			wg.Add(1)
			go func(i int) { defer wg.Done(); built[i], errs[i] = set.Init(ctx, db, nil) }(i)
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil || built[0] == built[1] {
			t.Fatalf("racing Init: %v, %v", built, errs)
		}
		var id int
		if err := db.QueryRow(`SELECT user_id FROM product_fixture`).Scan(&id); err != nil || id != 1 {
			t.Fatalf("product schema did not follow platform: %d, %v", id, err)
		}
		invalid := set
		invalid.Platform.Schema = "THIS MUST NEVER RUN"
		if built, err := invalid.Init(ctx, db, nil); err != nil || built {
			t.Fatalf("existing users: %v, %v", built, err)
		}
	})
	t.Run("apply order and unknown ledger", func(t *testing.T) {
		entries, err := set.Status(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		var unknown []string
		for _, e := range entries {
			if e.Unknown {
				unknown = append(unknown, e.Name)
			}
		}
		if !reflect.DeepEqual(unknown, []string{"0001-external.sql", "external-legacy.sql"}) {
			t.Fatalf("unknown = %v", unknown)
		}
		ran, err := set.Apply(ctx, db, nil)
		if err != nil || !reflect.DeepEqual(ran, set.Tracked()) {
			t.Fatalf("Apply = %v, %v", ran, err)
		}
		var trace string
		if err := db.QueryRow(`SELECT string_agg(name, ',' ORDER BY seq) FROM migration_trace`).Scan(&trace); err != nil || trace != strings.Join(ran, ",") {
			t.Fatalf("execution order = %s, %v", trace, err)
		}
		if ran, err := set.Apply(ctx, db, nil); err != nil || len(ran) != 0 {
			t.Fatalf("repeat = %v, %v", ran, err)
		}
		entries, err = set.Status(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if !e.Unknown && !set.baseline(e.Name) && (e.State != "applied" || e.AppliedAt.IsZero()) {
				t.Errorf("status = %+v", e)
			}
		}
		platform := set
		platform.Product = nil
		if ran, err := platform.Apply(ctx, db, nil); err != nil || len(ran) != 0 {
			t.Fatalf("platform ignores product ledger: %v, %v", ran, err)
		}
		if built, err := platform.Init(ctx, db, nil); err != nil || built {
			t.Fatalf("platform Init ignores product ledger: %v, %v", built, err)
		}
	})
	t.Run("high water includes unknown names", func(t *testing.T) {
		for _, pending := range []string{"0003-platform.sql", "0004-product.sql"} {
			for _, recorded := range []string{pending[:4] + "-elsewhere.sql", "0099-elsewhere.sql"} {
				exec(`TRUNCATE schema_migrations, migration_trace`)
				for _, name := range set.Tracked() {
					if number(name) < number(pending) {
						exec(`INSERT INTO schema_migrations(name) VALUES ($1)`, name)
					}
				}
				exec(`INSERT INTO schema_migrations(name) VALUES ($1)`, recorded)
				ran, err := set.Apply(ctx, db, nil)
				if err == nil || len(ran) != 0 || !strings.Contains(err.Error(), pending) || !strings.Contains(err.Error(), recorded) {
					t.Fatalf("guard %s: %v, %v", recorded, ran, err)
				}
				var count int
				if err := db.QueryRow(`SELECT count(*) FROM migration_trace`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("guard executed SQL: %d, %v", count, err)
				}
			}
		}
	})
	t.Run("mark either catalogue", func(t *testing.T) {
		exec(`TRUNCATE schema_migrations, migration_trace`)
		for _, name := range []string{"platform-z.sql", "product-z.sql", "0003-platform.sql", "0004-product.sql"} {
			if added, err := set.Mark(ctx, db, name); err != nil || !added {
				t.Fatalf("Mark %s: %v, %v", name, added, err)
			}
			if added, err := set.Mark(ctx, db, name); err != nil || added {
				t.Fatalf("repeat Mark %s: %v, %v", name, added, err)
			}
		}
		for _, name := range []string{"platform-history.sql", "product-history.sql", "missing.sql"} {
			if _, err := set.Mark(ctx, db, name); err == nil {
				t.Fatalf("Mark accepted %s", name)
			}
		}
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM migration_trace`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("Mark executed SQL: %d, %v", count, err)
		}
	})
}
