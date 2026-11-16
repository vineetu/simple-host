package migrations_test

import (
	"context"
	"database/sql"
	_ "github.com/lib/pq"
	schema "github.com/vsriram/simple-host/db"
	"github.com/vsriram/simple-host/db/migrations"
	hackdb "github.com/vsriram/simple-host/products/hack/db"
	hackmigrations "github.com/vsriram/simple-host/products/hack/db/migrations"
	"github.com/vsriram/simple-host/products/hack/testdb"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMain(m *testing.M) { testdb.Main(m) }

func composed() migrations.Set {
	product := hackdb.Catalog()
	return migrations.Set{Platform: schema.Catalog(), Product: &product, ProductOrder: hackmigrations.ProductOrder}
}
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
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
func readMigration(t *testing.T, name string) []byte {
	t.Helper()
	for _, catalog := range []migrations.Catalog{schema.Catalog(), hackdb.Catalog()} {
		if b, err := catalog.FS.ReadFile(name); err == nil {
			return b
		}
	}
	t.Fatalf("migration not found: %s", name)
	return nil
}

// TestZPHackApplicationsMigratesPreExistingRows is Codex 6: a migration test
// against genuinely pre-migration schema state (not just a missing
// schema_migrations row, as the "untracked database" case above covers),
// reusing TestMigrateAgainstPostgres's fresh-schema.sql + Apply pattern.
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
	set := composed()
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := set.Init(ctx, db, nil); err != nil {
		t.Fatal(err)
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

	if _, err := set.Apply(ctx, db, nil); err != nil {
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
	if ran, err := set.Apply(ctx, db, nil); err != nil || len(ran) != 0 {
		t.Errorf("second run: ran %v, err %v; want a no-op", ran, err)
	}
}

func TestUpgradeFromHackV0813(t *testing.T) {
	set := composed()
	oldDSN, newDSN := os.Getenv("MIGRATE_UPGRADE_OLD_DSN"), os.Getenv("MIGRATE_UPGRADE_NEW_DSN")
	if oldDSN == "" || newDSN == "" {
		t.Skip("MIGRATE_UPGRADE_OLD_DSN and MIGRATE_UPGRADE_NEW_DSN not set")
	}
	old, err := sql.Open("postgres", oldDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	fresh, err := sql.Open("postgres", newDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	ctx := context.Background()
	oldSchema, err := os.ReadFile("testdata/hack-v0.8.13-schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	current := schema.SQL + "\n" + hackdb.SQL
	list, err := os.ReadFile("testdata/hack-v0.8.13-tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, setup := range []struct {
		db     *sql.DB
		schema string
	}{{old, string(oldSchema)}, {fresh, string(current)}} {
		catalog := set
		catalog.Platform.Schema = setup.schema
		catalog.Product = nil
		built, err := catalog.Init(ctx, setup.db, nil)
		if err != nil || !built {
			t.Fatalf("Init: built %v: %v", built, err)
		}
	}
	recorded := map[string]bool{}
	filtered := fstest.MapFS{}
	for _, n := range strings.Fields(string(list)) {
		recorded[n] = true
		data := readMigration(t, n)
		filtered[n] = &fstest.MapFile{Data: data}
	}
	saved := set
	set = migrations.Set{Platform: migrations.Catalog{FS: filtered, Legacy: hackmigrations.ProductOrder}}
	if ran, err := set.Apply(ctx, old, nil); err != nil || len(ran) != len(recorded) {
		t.Fatalf("release Apply ran %v: %v", ran, err)
	}
	set = saved
	var want []string
	for _, n := range set.Tracked() {
		if !recorded[n] {
			want = append(want, n)
		}
	}
	ran, err := set.Apply(ctx, old, nil)
	if err != nil {
		t.Fatalf("upgrade applied %v: %v", ran, err)
	}
	if !reflect.DeepEqual(ran, want) {
		t.Errorf("upgrade applied %v; want %v", ran, want)
	}
	if _, err := set.Apply(ctx, fresh, nil); err != nil {
		t.Fatalf("fresh Apply: %v", err)
	}
	queries := map[string]string{
		"tables":      `SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p')`,
		"columns":     `SELECT c.relname || '.' || a.attname || ' | ' || format_type(a.atttypid,a.atttypmod) || ' | not null=' || a.attnotnull || ' | default=' || coalesce(pg_get_expr(d.adbin,d.adrelid),'') FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE n.nspname='public' AND c.relkind IN ('r','p') AND a.attnum>0 AND NOT a.attisdropped`,
		"constraints": `SELECT c.relname || '.' || con.conname || ' | ' || pg_get_constraintdef(con.oid) FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public'`,
		"indexes":     `SELECT tablename || '.' || indexname || ' | ' || indexdef FROM pg_indexes WHERE schemaname='public'`,
		"triggers":    `SELECT c.relname || '.' || t.tgname || ' | ' || pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND NOT t.tgisinternal`,
		"functions":   `SELECT p.proname || '(' || pg_get_function_identity_arguments(p.oid) || ')' FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public'`,
	}
	var diffs []string
	for kind, query := range queries {
		sets := [2]map[string]bool{{}, {}}
		for i, db := range []*sql.DB{old, fresh} {
			rows, err := db.Query(query)
			if err != nil {
				t.Fatalf("%s: %v", kind, err)
			}
			for rows.Next() {
				var line string
				if err := rows.Scan(&line); err != nil {
					t.Fatal(err)
				}
				sets[i][line] = true
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
		for line := range sets[0] {
			if !sets[1][line] {
				diffs = append(diffs, kind+" only upgraded: "+line)
			}
		}
		for line := range sets[1] {
			if !sets[0][line] {
				diffs = append(diffs, kind+" only fresh: "+line)
			}
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Errorf("schema differences:\n%s", strings.Join(diffs, "\n"))
	}
}
