package server

import (
	"context"
	"flag"
	"fmt"
	"os"

	siterepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/storage"
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
	database, code := openCommandDB()
	if database == nil {
		return code
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
	list, err := siterepo.SitesWithoutSummary(ctx, database, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, "list sites:", err)
		return 1
	}
	n := 0
	for _, site := range list {
		// A read failure here (permissions, a missing version directory) must
		// not wedge every later site behind this one forever: treat it as "no
		// index.html found" and move on, same as a genuinely missing file.
		data, err := disk.ReadFile(ctx, storage.FileVersion{UserID: site.UserID, SiteName: site.Name, Version: site.ActiveVersion}, "index.html")
		if err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "read index.html for %s: %v (treating as empty)\n", site.Name, err)
		}
		summary := siteshttp.ExtractSiteSummary(map[string][]byte{"index.html": data})
		if err := siterepo.UpdateSiteSummaryIfUnset(ctx, database, site.ID, summary); err != nil {
			fmt.Fprintln(os.Stderr, "store summary:", err)
			return 1
		}
		n++
		fmt.Printf("site-summary-backfill: summarised %d sites\n", n)
	}
	fmt.Printf("site-summary-backfill: total %d sites\n", n)
	return 0
}
