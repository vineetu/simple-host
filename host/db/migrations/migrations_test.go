package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	_ "github.com/lib/pq"
)

// Every file that is not historical must be picked up with no code change, and
// no historical file may ever be.
func TestTrackedSkipsBaseline(t *testing.T) {
	set := Set{Platform: Embedded()}
	all, err := fs.Glob(embedded, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	for name := range Baseline {
		found := false
		for _, n := range all {
			found = found || n == name
		}
		if !found {
			t.Errorf("baseline names %s, which is not in host/db/migrations", name)
		}
	}
	tracked := set.Tracked()
	for _, n := range tracked {
		if Baseline[n] {
			t.Errorf("historical %s would be run", n)
		}
	}
	if len(tracked)+len(Baseline) != len(all) {
		t.Errorf("tracked %d + baseline %d != %d files", len(tracked), len(Baseline), len(all))
	}

}

// Every tracked file must be safe to run on a database built from schema.sql,
// which already has its effect. This is a cheap text check; the database test
// below runs them for real.
func TestTrackedFilesArePlainSQL(t *testing.T) {
	set := Set{Platform: Embedded()}
	for _, n := range set.Tracked() {
		body, _ := embedded.ReadFile(n)
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), `\`) {
				t.Errorf("%s: psql meta-command %q; migrate runs plain SQL", n, line)
			}
			if strings.Contains(strings.ToUpper(line), "CONCURRENTLY") && !strings.HasPrefix(strings.TrimSpace(line), "--") {
				t.Errorf("%s: CONCURRENTLY cannot run inside migrate's transaction", n)
			}
		}
	}
}

// openTestDB needs MIGRATE_TEST_DSN: an EMPTY throwaway database the test may
// fill (scripts/check-fresh-install.sh makes one). Never point it at real data.
func openTestDB(t *testing.T) *sql.DB {
	dsn := os.Getenv("MIGRATE_TEST_DSN")
	if dsn == "" {
		t.Skip("MIGRATE_TEST_DSN not set (scripts/check-fresh-install.sh sets it)")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func pending(t *testing.T, set Set, db *sql.DB) []string {
	t.Helper()
	entries, err := set.Status(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.State == "pending" {
			out = append(out, e.Name)
		}
	}
	return out
}

func TestMigrateAgainstPostgres(t *testing.T) {
	set := Set{Platform: Embedded()}
	db := openTestDB(t)
	t.Run("catalogues", func(t *testing.T) { testCatalogsAgainstPostgres(t, db) })
	ctx := context.Background()
	schema, err := os.ReadFile("../schema.sql")
	if err != nil {
		t.Fatal(err)
	}

	set.Platform.Schema = string(schema)

	// An empty database is built by Init, which is what a container with no
	// psql runs (`migrate -init`, MIGRATE_ON_START). Two pods starting at once
	// both call it: exactly one builds the schema, the other waits on the lock
	// and then finds the users table.
	var initWG sync.WaitGroup
	built := make([]bool, 2)
	initErrs := make([]error, 2)
	for i := range built {
		initWG.Add(1)
		go func(i int) {
			defer initWG.Done()
			built[i], initErrs[i] = set.Init(ctx, db, nil)
		}(i)
	}
	initWG.Wait()
	if initErrs[0] != nil || initErrs[1] != nil {
		t.Fatalf("racing Init: %v / %v", initErrs[0], initErrs[1])
	}
	if built[0] == built[1] {
		t.Fatalf("racing Init built %v and %v; want the schema built exactly once", built[0], built[1])
	}
	alreadyBuilt := set
	alreadyBuilt.Platform.Schema = "THIS IS NEVER RUN"
	if again, err := alreadyBuilt.Init(ctx, db, nil); err != nil || again {
		t.Errorf("Init on a built database: built %v, err %v; want a no-op", again, err)
	}

	// A fresh database from schema.sql: every tracked file runs once as a
	// no-op (they are idempotent), then nothing is pending.
	ran, err := set.Apply(ctx, db, nil)
	if err != nil {
		t.Fatalf("fresh database: %v", err)
	}
	if len(ran) != len(set.Tracked()) {
		t.Errorf("fresh database ran %v, want every tracked file", ran)
	}
	if p := pending(t, set, db); len(p) != 0 {
		t.Errorf("pending after migrate: %v", p)
	}
	if ran, err := set.Apply(ctx, db, nil); err != nil || len(ran) != 0 {
		t.Errorf("second run: ran %v, err %v; want a no-op", ran, err)
	}

	// A box installed before tracking existed: no table at all, and the
	// columns 0026 drops from site_storage_tables are still there (ze-spam-
	// checked-posts.sql alters them).
	if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`ALTER TABLE site_storage_tables
		ADD COLUMN IF NOT EXISTS acc_read TEXT NOT NULL DEFAULT 'nobody',
		ADD COLUMN IF NOT EXISTS acc_add TEXT NOT NULL DEFAULT 'nobody',
		ADD COLUMN IF NOT EXISTS acc_edit TEXT NOT NULL DEFAULT 'nobody',
		ADD COLUMN IF NOT EXISTS acc_delete TEXT NOT NULL DEFAULT 'nobody',
		ADD COLUMN IF NOT EXISTS preset_base TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatal(err)
	}
	if p := pending(t, set, db); len(p) != len(set.Tracked()) {
		t.Errorf("untracked database: pending %v, want every tracked file", p)
	}
	if ran, err := set.Apply(ctx, db, nil); err != nil || len(ran) != len(set.Tracked()) {
		t.Errorf("untracked database: ran %v, err %v", ran, err)
	}

	// An older release's database missing a column that a newer file adds:
	// applied once, even with two runs racing, then a no-op. One file wraps
	// itself in BEGIN/COMMIT, as some hand-written files do.
	saved := set.Platform.FS
	mapped := fstest.MapFS{}
	for _, n := range set.Tracked() {
		body, _ := saved.ReadFile(n)
		mapped[n] = &fstest.MapFile{Data: body}
	}
	extra := fstest.MapFS{
		"0c1-handles.sql": {Data: []byte("THIS IS NEVER RUN")},
		"zz-test-a.sql":   {Data: []byte("ALTER TABLE users ADD COLUMN IF NOT EXISTS zz_test_a TEXT;\nINSERT INTO instance_config (key, value) VALUES ('zz-test-a', '1');")},
		"zz-test-b.sql":   {Data: []byte("BEGIN;\nALTER TABLE users ADD COLUMN IF NOT EXISTS zz_test_b TEXT;\nCOMMIT;")},
	}
	for n, f := range extra {
		mapped[n] = f
	}
	set.Platform.FS = mapped
	t.Cleanup(func() {
		db.Exec(`ALTER TABLE users DROP COLUMN IF EXISTS zz_test_a, DROP COLUMN IF EXISTS zz_test_b`)
		db.Exec(`DELETE FROM instance_config WHERE key = 'zz-test-a'`)
		db.Exec(`DELETE FROM schema_migrations WHERE name LIKE 'zz-test-%'`)
	})
	var wg sync.WaitGroup
	results := make([][]string, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = set.Apply(ctx, db, nil)
		}(i)
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("racing runs: %v / %v", errs[0], errs[1])
	}
	if got := len(results[0]) + len(results[1]); got != 2 {
		t.Errorf("racing runs applied %v and %v; want the two new files once in total", results[0], results[1])
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM instance_config WHERE key = 'zz-test-a'`).Scan(&n)
	if n != 1 {
		t.Errorf("zz-test-a ran %d times", n)
	}
	for _, col := range []string{"zz_test_a", "zz_test_b"} {
		var ok bool
		db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = $1)`, col).Scan(&ok)
		if !ok {
			t.Errorf("column %s was not added", col)
		}
	}
	if ran, err := set.Apply(ctx, db, nil); err != nil || len(ran) != 0 {
		t.Errorf("third run: ran %v, err %v; want a no-op", ran, err)
	}

	// A failing file is rolled back, stays pending, and stops the run.
	set.Platform.FS = fstest.MapFS{
		"zz-test-c.sql": {Data: []byte("ALTER TABLE users ADD COLUMN zz_test_c TEXT;\nSELECT no_such_function();")},
	}
	if _, err := set.Apply(ctx, db, nil); err == nil || !strings.Contains(err.Error(), "zz-test-c.sql") {
		t.Errorf("failing file: err %v", err)
	}
	var ok bool
	db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'zz_test_c')`).Scan(&ok)
	if ok {
		t.Error("failed file's column survived; it was not rolled back")
	}
	if p := pending(t, set, db); len(p) != 1 || p[0] != "zz-test-c.sql" {
		t.Errorf("after a failure, pending = %v", p)
	}

	// -mark records without running.
	if added, err := set.Mark(ctx, db, "zz-test-c.sql"); err != nil || !added {
		t.Errorf("mark: added %v, err %v", added, err)
	}
	if p := pending(t, set, db); len(p) != 0 {
		t.Errorf("after mark, pending = %v", p)
	}
	if _, err := set.Mark(ctx, db, "no-such.sql"); err == nil {
		t.Error("marking an unknown file succeeded")
	}
}

// The saved-data files touch busy tables: each runs as one transaction with a
// short lock timeout, so a backfill never races a write and a long lock wait
// gives up instead of queueing every request behind it.
func TestSavedDataMigrationsAreOneTransaction(t *testing.T) {
	for _, n := range []string{"sd1-saved-data-safety.sql", "sd1-saved-data-safety2-limits.sql", "sd1-saved-data-safety3-history-bytes.sql"} {
		body, err := embedded.ReadFile(n)
		if err != nil {
			t.Fatal(err)
		}
		s := string(body)
		b, l, c := strings.Index(s, "\nBEGIN;\n"), strings.Index(s, "SET LOCAL lock_timeout"), strings.LastIndex(s, "\nCOMMIT;\n")
		if b < 0 || l < b || c < l {
			t.Errorf("%s: not wrapped in BEGIN; SET LOCAL lock_timeout ...; COMMIT;", n)
		}
	}
}

func TestMigrationNames(t *testing.T) {
	all, err := fs.Glob(embedded, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	present, legacy, numbers := map[string]bool{}, map[string]bool{}, map[int]string{}
	next := 1
	for _, n := range all {
		present[n] = true
		if v := number(n); v >= next {
			next = v + 1
		}
	}
	advice := fmt.Sprintf("name new migrations NNNN-what.sql with the next number (currently %04d)", next)
	for i, n := range Legacy {
		if !present[n] || Baseline[n] || legacy[n] || (i > 0 && Legacy[i-1] >= n) {
			t.Errorf("invalid Legacy entry %s; %s", n, advice)
		}
		legacy[n] = true
	}
	for _, n := range all {
		if Baseline[n] || legacy[n] {
			continue
		}
		v := number(n)
		if v == 0 {
			t.Errorf("invalid migration %s; %s", n, advice)
			continue
		}
		if prev := numbers[v]; prev != "" {
			t.Errorf("duplicate number: %s and %s; %s", prev, n, advice)
		}
		numbers[v] = n
	}
	if _, err := embedded.ReadFile("testdata/schema.sql"); err == nil {
		t.Error("testdata must not be embedded")
	}
}

func TestNumberedGuard(t *testing.T) {
	set := Set{Platform: Embedded()}
	db := openTestDB(t)
	ctx := context.Background()
	t.Cleanup(func() {
		db.Exec(`DELETE FROM schema_migrations WHERE name IN ('0001-test.sql','0002-test.sql','0002-elsewhere.sql')`)
	})
	set.Platform.FS = fstest.MapFS{
		"0001-test.sql":     {Data: []byte("SELECT 1")},
		"0002-test.sql":     {Data: []byte("SELECT 1")},
		"zz-test-guard.sql": {Data: []byte("SELECT 1")},
	}
	// Other tests apply real migrations to this database. Give this fixture
	// its own tracking table on the one connection used by Apply.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(strings.Replace(createTable, "CREATE TABLE", "CREATE TEMP TABLE", 1)); err != nil {
		t.Fatal(err)
	}
	// Recorded by another branch's build: not in this build's files, and it
	// holds slot 0002, so both pending numbered files are refused.
	if _, err := db.Exec(`INSERT INTO schema_migrations (name) VALUES ('0002-elsewhere.sql') ON CONFLICT DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	ran, err := set.Apply(ctx, db, nil)
	if err == nil || !strings.Contains(err.Error(), "0001-test.sql") || !strings.Contains(err.Error(), "0002-elsewhere.sql") || len(ran) != 0 {
		t.Fatalf("guard: ran %v, error %v", ran, err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE name IN ('0001-test.sql','0002-test.sql','zz-test-guard.sql')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("guard ran files: %d, %v", count, err)
	}
}
