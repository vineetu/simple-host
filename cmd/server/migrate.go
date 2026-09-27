package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/vsriram/simple-host/db/migrations"
	"github.com/vsriram/simple-host/internal/buildinfo"
	dbpkg "github.com/vsriram/simple-host/internal/db"
)

const migrateUsage = `usage: simple-host migrate [-status | -mark FILE]

  (no flag)   Apply every pending file in db/migrations/, in order, each once,
              each in its own transaction, under a lock so two runs cannot race.
              Then checks the database has everything this build reads.
  -status     List every migration file as applied, pending or baseline
              (historical files applied by hand before this tool; never run).
  -mark FILE  Record FILE as applied without running it, for a database where
              the SQL was applied by hand.

Reads DB_DSN from the environment. The server itself never migrates on start;
on a small box, re-running the install command runs this.`

func runMigrateCommand(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, migrateUsage) }
	status := fs.Bool("status", false, "")
	mark := fs.String("mark", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || (*status && *mark != "") {
		fs.Usage()
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

	// A database container that has only just started (or is still loading
	// schema.sql into a new volume) refuses connections for a while.
	if err := waitForDB(database, 2*time.Minute); err != nil {
		fmt.Fprintln(os.Stderr, "database not reachable:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	switch {
	case *status:
		entries, err := migrations.Status(ctx, database)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		pending := 0
		for _, e := range entries {
			when := ""
			if e.State == "applied" {
				when = e.AppliedAt.UTC().Format(time.RFC3339)
			}
			if e.State == "pending" {
				pending++
			}
			fmt.Printf("%-9s %-45s %s\n", e.State, e.Name, when)
		}
		fmt.Printf("%d pending\n", pending)
		return 0
	case *mark != "":
		added, err := migrations.Mark(ctx, database, *mark)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		if added {
			fmt.Printf("recorded %s as applied\n", *mark)
		} else {
			fmt.Printf("%s was already recorded\n", *mark)
		}
		return 0
	}

	fmt.Println(buildinfo.String())
	ran, err := migrations.Apply(ctx, database, func(f string, a ...any) { fmt.Printf(f+"\n", a...) })
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate failed:", err)
		return 1
	}
	if len(ran) == 0 {
		fmt.Println("database is up to date; nothing to apply")
	} else {
		fmt.Printf("applied %d migration(s)\n", len(ran))
	}
	if err := dbpkg.VerifySchema(ctx, database); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func waitForDB(database *sql.DB, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := database.PingContext(ctx)
		cancel()
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(2 * time.Second)
	}
}

// runVersionCommand prints the release and commit. It needs no configuration
// and no database, so it works on a box whose database is down.
func runVersionCommand() int {
	fmt.Println(buildinfo.String())
	tracked := migrations.Tracked()
	latest := "none"
	if len(tracked) > 0 {
		latest = tracked[len(tracked)-1]
	}
	fmt.Printf("migrations in this build: %d (last: %s); `simple-host migrate -status` compares them with the database\n", len(tracked), latest)
	return 0
}
