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

// People's page views are counted per site-relative path and per referring
// domain; bots, the site's own address as referrer, and old seven-field lines
// add nothing to the referrers. Needs DB_DSN.
func TestIngestPagesAndReferrers(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	var userID, siteID string
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ('pr', 'pr') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, custom_domain) VALUES ($1, 'shop', 'shop.example') RETURNING id`, userID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	const ff = "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"
	lines := []string{
		"2026-09-20T10:00:01+00:00\tshop.example\t200\tGET\t/\t203.0.113.1\t" + ff, // old format
		"2026-09-20T10:00:02+00:00\tshop.example\t200\tGET\t/index.html\t203.0.113.2\t" + ff + "\tnews.ycombinator.com",
		"2026-09-20T10:00:03+00:00\tshop.example\t200\tGET\t/menu/\t203.0.113.3\t" + ff + "\tnews.ycombinator.com",
		"2026-09-20T10:00:04+00:00\tshop.example\t200\tGET\t/menu/\t203.0.113.4\t" + ff + "\tshop.example", // own address
		"2026-09-20T10:00:05+00:00\tshop.example\t200\tGET\t/menu/\t203.0.113.5\tcurl/8.0\tt.co",           // not a person
		"2026-09-20T10:00:06+00:00\tshop.example\t200\tGET\t/app.css\t203.0.113.6\t" + ff + "\tt.co",       // not a page
	}
	ing := NewIngester(db, "unused", "salt", "sites.example", "example")
	if err := ing.commitLines(ctx, lines, false, 0, 0); err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	rows, err := db.Query(`SELECT 'p:' || path, views FROM site_page_daily WHERE site_id = $1
		UNION ALL SELECT 'r:' || domain, views FROM site_referrer_daily WHERE site_id = $1`, siteID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var n int64
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		got[k] = n
	}
	want := map[string]int64{"p:/": 2, "p:/menu/": 2, "r:news.ycombinator.com": 2}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("pages and referrers = %v, want %v", got, want)
	}
}

// Random paths and referrer spam cannot add a row per request: past the
// per-site daily cap, new pages and domains go to (other), across runs too.
// Needs DB_DSN.
func TestIngestCapsPagesAndReferrersPerDay(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	var userID, siteID string
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ('cap', 'cap') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, custom_domain) VALUES ($1, 'shop', 'shop.example') RETURNING id`, userID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	const ff = "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"
	line := func(i int, p, ref string) string {
		return fmt.Sprintf("2026-09-20T10:00:%02d+00:00\tshop.example\t200\tGET\t%s\t203.0.113.%d\t%s\t%s", i%60, p, i%250+1, ff, ref)
	}
	ing := NewIngester(db, "unused", "salt", "sites.example", "example").WithItemCaps(3, 2)
	var run1, run2 []string
	for i := 0; i < 6; i++ {
		run1 = append(run1, line(i, fmt.Sprintf("/p%d", i), fmt.Sprintf("spam%d.example", i)))
	}
	run1 = append(run1, line(10, "/p0", "spam0.example"), line(11, "//p0?x=1", "spam0.example"))
	for i := 0; i < 3; i++ {
		run2 = append(run2, line(20+i, fmt.Sprintf("/q%d", i), fmt.Sprintf("more%d.example", i)))
	}
	run2 = append(run2, line(30, "/%70%30", "spam0.example"))
	for _, lines := range [][]string{run1, run2} {
		if err := ing.commitLines(ctx, lines, false, 0, 0); err != nil {
			t.Fatal(err)
		}
	}
	count := func(q string) (rows int, views int64, other int64) {
		r, err := db.Query(q, siteID)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		for r.Next() {
			var item string
			var n int64
			if err := r.Scan(&item, &n); err != nil {
				t.Fatal(err)
			}
			if item == otherItem {
				other = n
			} else {
				rows++
			}
			views += n
		}
		return
	}
	pr, pv, po := count(`SELECT path, views FROM site_page_daily WHERE site_id = $1`)
	if pr != 3 || pv != 12 || po != 6 {
		t.Errorf("pages: %d rows, %d views, %d other; want 3 rows, 12 views, 6 other", pr, pv, po)
	}
	var p0 int64
	db.QueryRow(`SELECT views FROM site_page_daily WHERE site_id = $1 AND path = '/p0'`, siteID).Scan(&p0)
	if p0 != 4 {
		t.Errorf("/p0 spelled four ways counted %d, want 4", p0)
	}
	rr, rv, _ := count(`SELECT domain, views FROM site_referrer_daily WHERE site_id = $1`)
	if rr != 2 || rv != 12 {
		t.Errorf("referrers: %d rows, %d views; want 2 rows, 12 views", rr, rv)
	}
}
