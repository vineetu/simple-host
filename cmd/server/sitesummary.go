package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/storage"
)

const siteSummaryUsage = `usage: simple-host site-summary-backfill [-limit N]

Extracts summaries from existing sites' active index.html files, then exits.
Safe to re-run: only touches sites never summarised.
Reads DB_DSN and DATA_DIR (default ./data/sites) from the environment.
-limit bounds the sites processed in this run; 0 means no limit.`

func runSiteSummaryBackfill(args []string) int {
	fs := flag.NewFlagSet("site-summary-backfill", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, siteSummaryUsage) }
	limit := fs.Int("limit", 0, "maximum sites to process (0 = no limit)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *limit < 0 {
		fmt.Fprintln(os.Stderr, "limit must be non-negative")
		return 2
	}
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DB_DSN is not set")
		return 2
	}
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open postgres:", err)
		return 1
	}
	defer database.Close()
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data/sites"
	}
	disk, err := storage.NewDiskStorage(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open storage:", err)
		return 1
	}
	ctx := context.Background()
	rows, err := database.QueryContext(ctx, `SELECT id, user_id, name, active_version FROM sites WHERE deleted_at IS NULL AND summary_title IS NULL ORDER BY created_at, id LIMIT NULLIF($1, 0)`, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list sites:", err)
		return 1
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id, userID, name string
		var version int
		if err := rows.Scan(&id, &userID, &name, &version); err != nil {
			fmt.Fprintln(os.Stderr, "read site:", err)
			return 1
		}
		// A read failure here (permissions, a missing version directory) must
		// not wedge every later site behind this one forever: treat it as "no
		// index.html found" and move on, same as a genuinely missing file.
		data, err := os.ReadFile(filepath.Join(disk.VersionDir(userID, name, version), "index.html"))
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "read index.html for %s: %v (treating as empty)\n", name, err)
		}
		summary := handler.ExtractSiteSummary(map[string][]byte{"index.html": data})
		if err := db.UpdateSiteSummaryIfUnset(ctx, database, id, summary); err != nil {
			fmt.Fprintln(os.Stderr, "store summary:", err)
			return 1
		}
		n++
		fmt.Printf("site-summary-backfill: summarised %d sites\n", n)
	}
	if err := rows.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "list sites:", err)
		return 1
	}
	fmt.Printf("site-summary-backfill: total %d sites\n", n)
	return 0
}
