package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dbpkg "github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/handler"
)

const moveSiteBaseUsage = `usage: simple-host move-site-base --from DOMAIN --to DOMAIN [--apply]

  Rewrites the stored free names and retired names under --from
  (<name>.<from>) to the same name under --to, in one transaction, and links
  each moved name on disk next to the old one (DATA_DIR/domains/). Custom
  domains, reserved names and names with more than one label are never
  touched. Without --apply nothing changes and only the counts are printed.
  Running it again does nothing. Safe to run while serving: lookups find
  either form (docs/designs/site-base-domain-move.md).

  Reads DB_DSN and DATA_DIR from the environment (e.g. set -a; . /etc/simple-host.env).`

func runMoveSiteBaseCommand(args []string) int {
	fs := flag.NewFlagSet("move-site-base", flag.ContinueOnError)
	from := fs.String("from", "", "the domain names are stored under now")
	to := fs.String("to", "", "the domain to store them under")
	apply := fs.Bool("apply", false, "write the changes (default: dry run, counts only)")
	fs.Usage = func() { fmt.Fprintln(os.Stderr, moveSiteBaseUsage) }
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *from == "" || *to == "" {
		fmt.Fprintln(os.Stderr, moveSiteBaseUsage)
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := dbpkg.VerifySchema(ctx, database); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	rep, err := dbpkg.MoveSiteBase(ctx, database, *from, *to, handler.LabelReserved, *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	mode := "dry run (nothing changed)"
	if *apply {
		mode = "applied"
	}
	fmt.Printf("move-site-base %s -> %s: %s\n", *from, *to, mode)
	fmt.Printf("  free names (current):  %d\n", rep.CustomDomains)
	fmt.Printf("  free names (earlier):  %d\n", rep.PreviousDomains)
	fmt.Printf("  retired names:         %d\n", rep.RetiredNames)
	fmt.Printf("  left as they are (the new form is already held): %d\n", rep.Conflicts)
	if !*apply {
		return 0
	}
	// Disk: domains/<new> points where domains/<old> does. The old link stays.
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "./data/sites"
	}
	linked, failed := 0, 0
	for _, m := range rep.Links {
		oldLink := filepath.Join(dataDir, "domains", m.From)
		target, err := os.Readlink(oldLink)
		if err != nil {
			continue // not linked on disk (served by the app from the database)
		}
		newLink := filepath.Join(dataDir, "domains", m.To)
		if cur, err := os.Readlink(newLink); err == nil {
			if cur == target {
				linked++
			} else {
				failed++
				fmt.Fprintf(os.Stderr, "  %s already links elsewhere; left as it is\n", m.To)
			}
			continue
		}
		if err := os.Symlink(target, newLink); err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "  link %s: %v\n", m.To, err)
			continue
		}
		linked++
	}
	fmt.Printf("  disk links:            %d (failed %d)\n", linked, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
