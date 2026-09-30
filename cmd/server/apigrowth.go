package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vsriram/simple-host/internal/geoip"
	"github.com/vsriram/simple-host/internal/handler"
)

const apiGrowthUsage = `usage: simple-host api-growth-backfill [NGINX_LOG...]

Fills the admin page's API growth counts (api_growth_daily) from what is
already recorded, and exits:

  1. every day the API traffic tables still hold (api_request_daily for the
     kind of call, api_ip_daily for the caller's country). Adds only what is
     missing, so it is safe to run again; the server does the same at start.
  2. /mcp calls from the given nginx access logs ("combined" format; .gz is
     read too), for days that have no connector calls counted yet. Before
     this release /mcp was not counted anywhere.

Countries are looked up on this box (GEOIP_DIR, or "geoip" beside DATA_DIR);
no address is stored or sent anywhere. Reads DB_DSN from the environment.`

func runAPIGrowthBackfill(args []string) int {
	fs := flag.NewFlagSet("api-growth-backfill", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, apiGrowthUsage) }
	if err := fs.Parse(args); err != nil {
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
	geoDir := os.Getenv("GEOIP_DIR")
	if geoDir == "" {
		dataDir := os.Getenv("DATA_DIR")
		if dataDir == "" {
			dataDir = "./data/sites"
		}
		geoDir = filepath.Join(filepath.Dir(filepath.Clean(dataDir)), "geoip")
	}
	geo := geoip.Open(geoDir)
	defer geo.Close()
	if city, _ := geo.Loaded(); !city {
		fmt.Fprintf(os.Stderr, "note: no country database in %s; every caller counts as unknown\n", geoDir)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	m := handler.NewAPIGrowthBackfiller(database, geo)
	n, err := m.BackfillGrowth(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backfill from the API traffic tables:", err)
		return 1
	}
	fmt.Printf("from the API traffic tables: added %d calls\n", n)
	if fs.NArg() > 0 {
		n, err := m.BackfillGrowthFromLogs(ctx, fs.Args())
		if err != nil {
			fmt.Fprintln(os.Stderr, "backfill from logs:", err)
			return 1
		}
		fmt.Printf("from %d log files: added %d /mcp calls\n", fs.NArg(), n)
	}
	return 0
}
