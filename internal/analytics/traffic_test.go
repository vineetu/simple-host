package analytics

import (
	"context"
	"testing"
)

// Every line counts toward traffic, whatever its method, status or path, and
// only a site's host counts toward that site; /v1 is API, the rest pages.
func TestCommitLinesTraffic(t *testing.T) {
	db := isolatedDB(t)
	ctx := context.Background()
	var userID, siteID string
	if err := db.QueryRow(`INSERT INTO users (username, handle) VALUES ('tr', 'tr') RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`INSERT INTO sites (user_id, name, custom_domain) VALUES ($1, 'shop', 'shop.example') RETURNING id`, userID).Scan(&siteID); err != nil {
		t.Fatal(err)
	}
	ua := "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"
	lines := []string{
		"2026-09-30T10:00:00+00:00\tshop.example\t200\tGET\t/\t203.0.113.1\t" + ua + "\t\t1000",
		"2026-09-30T10:00:01+00:00\tshop.example\t200\tGET\t/app.js\t203.0.113.1\t" + ua + "\t\t500",
		"2026-09-30T10:00:02+00:00\tshop.example:443\t404\tGET\t/nope\t203.0.113.1\t" + ua + "\t\t200",
		"2026-09-30T10:00:03+00:00\tshop.example\t201\tPOST\t/v1/sites/x/state?y=1\t203.0.113.1\t" + ua + "\t\t300",
		"2026-09-30T10:00:04+00:00\tunknown.example\t404\tGET\t/\t203.0.113.2\t" + ua + "\t\t50",
		"2026-10-01T00:00:00+00:00\tshop.example\t200\tGET\t/\t203.0.113.1\t" + ua + "\t\t7",
		"2026-09-30T10:00:05+00:00\tshop.example\t200\tGET\t/\t203.0.113.1\t" + ua, // before bytes were logged
		"not a log line",
	}
	ing := NewIngester(db, "", "salt", "sites.example", "example")
	if err := ing.commitLines(ctx, lines, false, 0, 0); err != nil {
		t.Fatal(err)
	}
	type row struct{ bytes, requests int64 }
	total := map[string]row{}
	rows, err := db.Query(`SELECT day::text || ' ' || kind, bytes, requests FROM traffic_daily`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		var r row
		rows.Scan(&k, &r.bytes, &r.requests)
		total[k] = r
	}
	rows.Close()
	want := map[string]row{
		"2026-09-30 pages": {1000 + 500 + 200 + 50, 5},
		"2026-09-30 api":   {300, 1},
		"2026-10-01 pages": {7, 1},
	}
	if len(total) != len(want) {
		t.Fatalf("traffic_daily = %v, want %v", total, want)
	}
	for k, w := range want {
		if total[k] != w {
			t.Errorf("traffic_daily %s = %+v, want %+v", k, total[k], w)
		}
	}
	var pages, api int64
	if err := db.QueryRow(`SELECT COALESCE(SUM(bytes) FILTER (WHERE kind = 'pages'), 0), COALESCE(SUM(bytes) FILTER (WHERE kind = 'api'), 0)
		FROM site_traffic_daily WHERE site_id = $1 AND day = '2026-09-30'`, siteID).Scan(&pages, &api); err != nil {
		t.Fatal(err)
	}
	if pages != 1700 || api != 300 {
		t.Errorf("site traffic 2026-09-30: pages %d api %d, want 1700 and 300", pages, api)
	}
	// A second pass adds, like the view counts.
	if err := ing.commitLines(ctx, lines[:1], false, 0, 0); err != nil {
		t.Fatal(err)
	}
	var b int64
	db.QueryRow(`SELECT bytes FROM site_traffic_daily WHERE site_id = $1 AND day = '2026-09-30' AND kind = 'pages'`, siteID).Scan(&b)
	if b != 2700 {
		t.Errorf("after a second pass: %d, want 2700", b)
	}
}
