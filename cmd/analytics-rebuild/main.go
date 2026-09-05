// Command analytics-rebuild replays the nginx analytics log from the beginning
// and rewrites every hourly aggregate with the current traffic classifier.
//
// Run it once after deploying the classifier, so historical charts show the same
// person/bot/infra split as new traffic instead of the pre-classifier totals
// (which counted the loopback monitoring probe as thousands of daily pageviews).
//
// It reads the same environment as the server: DB_DSN, ADMIN_API_KEY (the hash
// salt -- it must match the server's or visitor counts will not line up),
// ANALYTICS_LOG, SITE_DOMAIN, CONTENT_HOST.
//
// This is also how per-country history is backfilled: the log still carries
// remote_addr, so replaying it resolves a country for every retained line. Run
// ip-country-load first, or every visitor comes back as 'XX'.
//
//	sudo systemctl stop simple-host
//	sudo -u simplehost env $(cat /etc/simple-host.env | xargs) analytics-rebuild
//	sudo systemctl start simple-host
//
// Stopping the server first is not strictly required, but it keeps the log
// output readable and avoids the ingest loop racing the replay.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/analytics"
	"github.com/vsriram/simple-host/internal/config"
)

func main() {
	timeout := flag.Duration("timeout", 30*time.Minute, "overall deadline for the rebuild")
	dryRun := flag.Bool("dry-run", false, "report what would be replayed, then exit without writing")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.AnalyticsLog == "" {
		log.Fatal("ANALYTICS_LOG is not set; nothing to replay")
	}

	info, err := os.Stat(cfg.AnalyticsLog)
	if err != nil {
		log.Fatalf("stat %s: %v", cfg.AnalyticsLog, err)
	}

	db, err := sql.Open("postgres", cfg.DBDSN)
	if err != nil {
		log.Fatalf("open postgres: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping postgres: %v", err)
	}

	before, err := countRows(ctx, db)
	if err != nil {
		log.Fatalf("count existing rows: %v", err)
	}

	fmt.Printf("log:       %s (%.1f MB)\n", cfg.AnalyticsLog, float64(info.Size())/(1<<20))
	fmt.Printf("existing:  %d view rows, %d visitor rows, %d geo rows\n", before.views, before.visitors, before.geo)

	if *dryRun {
		fmt.Println("dry run: no changes made")
		return
	}

	started := time.Now()
	ing := analytics.NewIngester(db, cfg.AnalyticsLog, cfg.AdminAPIKey, cfg.ContentHost, cfg.SiteDomain)
	if err := ing.Rebuild(ctx); err != nil {
		log.Fatalf("rebuild: %v", err)
	}

	after, err := countRows(ctx, db)
	if err != nil {
		log.Fatalf("count rebuilt rows: %v", err)
	}

	fmt.Printf("rebuilt:   %d view rows, %d visitor rows, %d geo rows in %s\n",
		after.views, after.visitors, after.geo, time.Since(started).Round(time.Second))

	if err := printSplit(ctx, db); err != nil {
		log.Fatalf("summarise: %v", err)
	}
}

type rowCounts struct{ views, visitors, geo int64 }

func countRows(ctx context.Context, db *sql.DB) (rowCounts, error) {
	var c rowCounts
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM site_view_hourly`).Scan(&c.views); err != nil {
		return c, err
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM site_visitor_hourly`).Scan(&c.visitors); err != nil {
		return c, err
	}
	err := db.QueryRowContext(ctx, `SELECT count(*) FROM site_geo_daily`).Scan(&c.geo)
	return c, err
}

// printSplit shows the rebuilt totals so the operator can sanity-check the
// classifier against what they expected before trusting the dashboard.
func printSplit(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT class, SUM(views) AS views, MIN(hour)::date::text, MAX(hour)::date::text
		FROM site_view_hourly
		GROUP BY class
		ORDER BY views DESC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println("\nclass    views      covering")
	for rows.Next() {
		var class, from, to string
		var views int64
		if err := rows.Scan(&class, &views, &from, &to); err != nil {
			return err
		}
		fmt.Printf("%-8s %-10d %s → %s\n", class, views, from, to)
	}
	return rows.Err()
}
