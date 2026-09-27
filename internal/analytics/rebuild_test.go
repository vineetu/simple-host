package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// isolatedDB applies db/schema.sql to a throwaway Postgres schema, so a
// rebuild (which empties every aggregate table) cannot touch data other tests
// share. Needs DB_DSN.
func isolatedDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN not set")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := fmt.Sprintf("analytics_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) })
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("postgres", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ddl, err := os.ReadFile("../../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// A rebuild run while the live log is absent (just rotated away, not yet
// recreated) must leave the position past the newest archive. Before, it left
// no position, so the first ingest after the log reappeared read the newest
// archive from 0 and counted it a second time.
func TestRebuildWithoutLiveLogDoesNotRecount(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	var userID, siteID string
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ('b8', 'b8') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, custom_domain) VALUES ($1, 'shop', 'shop.example') RETURNING id`, userID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	live := filepath.Join(dir, "analytics.log")
	line := func(n int) string {
		return fmt.Sprintf("2026-09-20T10:00:%02d+00:00\tshop.example\t200\tGET\t/\t203.0.113.%d\tMozilla/5.0 (X11; Linux x86_64) Firefox/130.0\n", n, n)
	}
	writeGz(t, live+".2.gz", line(1)+line(2))
	writePlain(t, live+".1", line(3)+line(4)+line(5))

	ing := NewIngester(db, live, "salt-secret", "sites.example", "example")
	views := func() int64 {
		var n sql.NullInt64
		if err := db.QueryRow(`SELECT sum(views) FROM site_view_hourly WHERE site_id = $1`, siteID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n.Int64
	}

	if err := ing.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	if got := views(); got != 5 {
		t.Fatalf("after rebuild: %d views, want 5", got)
	}

	// The log comes back with one new line; the next pass counts only that.
	writePlain(t, live, line(6))
	if err := ing.runOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := views(); got != 6 {
		t.Errorf("after first ingest: %d views, want 6 (the newest archive was counted again)", got)
	}
}

// The retired per-name host (<name>.<siteDomain>) goes to the oldest site of
// that name, but never to one that has been deleted.
func TestNameToOldestSkipsDeleted(t *testing.T) {
	db := isolatedDB(t)
	var a, b, oldID, newID string
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ('na', 'na') RETURNING id`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ('nb', 'nb') RETURNING id`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, created_at, deleted_at) VALUES ($1, 'blog', now() - interval '1 day', now()) RETURNING id`, a).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites (user_id, name) VALUES ($1, 'blog') RETURNING id`, b).Scan(&newID); err != nil {
		t.Fatal(err)
	}
	maps, err := NewIngester(db, "unused", "s", "sites.example", "example").buildAttrMaps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := maps.nameToOldest["blog"]; got != newID {
		t.Errorf("nameToOldest[blog] = %s, want the live site %s (deleted one is %s)", got, newID, oldID)
	}
}
