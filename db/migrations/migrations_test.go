package migrations

import (
	"context"
	"database/sql"
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
			t.Errorf("baseline names %s, which is not in db/migrations", name)
		}
	}
	tracked := Tracked()
	for _, n := range tracked {
		if Baseline[n] {
			t.Errorf("historical %s would be run", n)
		}
	}
	if len(tracked)+len(Baseline) != len(all) {
		t.Errorf("tracked %d + baseline %d != %d files", len(tracked), len(Baseline), len(all))
	}
	for i := 1; i < len(tracked); i++ {
		if tracked[i-1] >= tracked[i] {
			t.Errorf("not in lexical order: %v", tracked)
		}
	}
}

// Every tracked file must be safe to run on a database built from schema.sql,
// which already has its effect. This is a cheap text check; the database test
// below runs them for real.
func TestTrackedFilesArePlainSQL(t *testing.T) {
	for _, n := range Tracked() {
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

func pending(t *testing.T, db *sql.DB) []string {
	t.Helper()
	entries, err := Status(context.Background(), db)
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
	db := openTestDB(t)
	ctx := context.Background()
	schema, err := os.ReadFile("../schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("schema.sql: %v", err)
	}

	// A fresh database from schema.sql: every tracked file runs once as a
	// no-op (they are idempotent), then nothing is pending.
	ran, err := Apply(ctx, db, nil)
	if err != nil {
		t.Fatalf("fresh database: %v", err)
	}
	if len(ran) != len(Tracked()) {
		t.Errorf("fresh database ran %v, want every tracked file", ran)
	}
	if p := pending(t, db); len(p) != 0 {
		t.Errorf("pending after migrate: %v", p)
	}
	if ran, err := Apply(ctx, db, nil); err != nil || len(ran) != 0 {
		t.Errorf("second run: ran %v, err %v; want a no-op", ran, err)
	}

	// A box installed before tracking existed: no table at all.
	if _, err := db.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if p := pending(t, db); len(p) != len(Tracked()) {
		t.Errorf("untracked database: pending %v, want every tracked file", p)
	}
	if ran, err := Apply(ctx, db, nil); err != nil || len(ran) != len(Tracked()) {
		t.Errorf("untracked database: ran %v, err %v", ran, err)
	}

	// An older release's database missing a column that a newer file adds:
	// applied once, even with two runs racing, then a no-op. One file wraps
	// itself in BEGIN/COMMIT, as some hand-written files do.
	saved := files
	t.Cleanup(func() { files = saved })
	mapped := fstest.MapFS{}
	for _, n := range Tracked() {
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
	files = mapped
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
			results[i], errs[i] = Apply(ctx, db, nil)
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
	if ran, err := Apply(ctx, db, nil); err != nil || len(ran) != 0 {
		t.Errorf("third run: ran %v, err %v; want a no-op", ran, err)
	}

	// A failing file is rolled back, stays pending, and stops the run.
	files = fstest.MapFS{
		"zz-test-c.sql": {Data: []byte("ALTER TABLE users ADD COLUMN zz_test_c TEXT;\nSELECT no_such_function();")},
	}
	if _, err := Apply(ctx, db, nil); err == nil || !strings.Contains(err.Error(), "zz-test-c.sql") {
		t.Errorf("failing file: err %v", err)
	}
	var ok bool
	db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'zz_test_c')`).Scan(&ok)
	if ok {
		t.Error("failed file's column survived; it was not rolled back")
	}
	if p := pending(t, db); len(p) != 1 || p[0] != "zz-test-c.sql" {
		t.Errorf("after a failure, pending = %v", p)
	}

	// -mark records without running.
	if added, err := Mark(ctx, db, "zz-test-c.sql"); err != nil || !added {
		t.Errorf("mark: added %v, err %v", added, err)
	}
	if p := pending(t, db); len(p) != 0 {
		t.Errorf("after mark, pending = %v", p)
	}
	if _, err := Mark(ctx, db, "no-such.sql"); err == nil {
		t.Error("marking an unknown file succeeded")
	}
}

// TestZPHackApplicationsMigratesPreExistingRows is Codex 6: a migration test
// against genuinely pre-migration schema state (not just a missing
// schema_migrations row, as the "untracked database" case above covers),
// reusing TestMigrateAgainstPostgres's fresh-schema.sql + Apply() pattern.
// zp-hack-applications.sql's own effect is undone first -- CASCADE drops its
// CHECK constraints and partial indexes along with the columns -- so running
// it here is not a no-op: it must backfill exactly what an existing,
// unmigrated install has. An event defaults applications_open to true, and
// an existing (join-link) participant gets joined_via='link' with blank
// profile fields, never NULL (every column is NOT NULL).
//
// Runs against whatever MIGRATE_TEST_DSN already holds: standalone (an
// empty throwaway database), schema.sql has not run yet, so it is applied
// here. Run after TestMigrateAgainstPostgres in the same process (that is
// also how scripts/check-fresh-install.sh exercises this package: one `go
// test` against one throwaway database), schema.sql is already there --
// CREATE TABLE users has no IF NOT EXISTS, so re-running it verbatim would
// fail on a database it already built, and that failure would say nothing
// about this test.
func TestZPHackApplicationsMigratesPreExistingRows(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	var schemaApplied bool
	if err := db.QueryRow(`SELECT to_regclass('public.users') IS NOT NULL`).Scan(&schemaApplied); err != nil {
		t.Fatal(err)
	}
	if !schemaApplied {
		schema, err := os.ReadFile("../schema.sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(schema)); err != nil {
			t.Fatalf("schema.sql: %v", err)
		}
	}

	// Undo the migration's own effect, simulating an install that has not
	// run it yet.
	if _, err := db.Exec(`ALTER TABLE event_members
		DROP COLUMN joined_via CASCADE, DROP COLUMN city CASCADE,
		DROP COLUMN country CASCADE, DROP COLUMN age_range CASCADE, DROP COLUMN gender CASCADE`); err != nil {
		t.Fatalf("undo event_members columns: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE events DROP COLUMN applications_open CASCADE`); err != nil {
		t.Fatalf("undo events column: %v", err)
	}
	// Whatever a schema_migrations row from an earlier run in this process
	// says, the file must be pending again now that its columns are gone.
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE name = 'zp-hack-applications.sql'`); err != nil {
		t.Fatalf("clear schema_migrations row: %v", err)
	}

	// A leftover row from an earlier failed run of this same test.
	db.Exec(`DELETE FROM events WHERE slug = 'zp-premigration-event'`)
	db.Exec(`DELETE FROM users WHERE username IN ('zp-premigration-account@example.com', 'zp-premigration-member@example.com')`)

	// Pre-existing rows, exactly as an unmigrated install would have them:
	// an account, an event and a join-link participant, with no reference to
	// the now-missing columns.
	var accountID, memberID, eventID string
	if err := db.QueryRow(`INSERT INTO users (username) VALUES ('zp-premigration-account@example.com') RETURNING id`).Scan(&accountID); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO users (username) VALUES ('zp-premigration-member@example.com') RETURNING id`).Scan(&memberID); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO events (slug, account_id, title, organiser_name, contact_email, join_code, judge_code)
		VALUES ('zp-premigration-event', $1, 'Pre-migration event', 'Org', 'org@example.com', 'zp-premig-join', 'zp-premig-judge')
		RETURNING id`, accountID).Scan(&eventID); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO event_members (event_id, user_id, role) VALUES ($1, $2, 'participant')`, eventID, memberID); err != nil {
		t.Fatalf("insert pre-existing member row: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DELETE FROM events WHERE id = $1`, eventID)
		db.Exec(`DELETE FROM users WHERE id IN ($1, $2)`, accountID, memberID)
	})

	if _, err := Apply(ctx, db, nil); err != nil {
		t.Fatalf("apply: %v", err)
	}

	var appsOpen bool
	if err := db.QueryRow(`SELECT applications_open FROM events WHERE id = $1`, eventID).Scan(&appsOpen); err != nil {
		t.Fatalf("read applications_open: %v", err)
	}
	if !appsOpen {
		t.Error("existing event did not default applications_open to true")
	}
	var joinedVia, city, country, ageRange, gender string
	if err := db.QueryRow(`SELECT joined_via, city, country, age_range, gender FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, memberID).
		Scan(&joinedVia, &city, &country, &ageRange, &gender); err != nil {
		t.Fatalf("read pre-existing member row: %v", err)
	}
	if joinedVia != "link" || city != "" || country != "" || ageRange != "" || gender != "" {
		t.Errorf("pre-existing member backfilled as joined_via=%q city=%q country=%q age_range=%q gender=%q, want link/blank/blank/blank/blank", joinedVia, city, country, ageRange, gender)
	}

	// The file is idempotent and now recorded: a second run is a no-op.
	if ran, err := Apply(ctx, db, nil); err != nil || len(ran) != 0 {
		t.Errorf("second run: ran %v, err %v; want a no-op", ran, err)
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
