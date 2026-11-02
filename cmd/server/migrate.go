package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	schema "github.com/vsriram/simple-host/db"
	"github.com/vsriram/simple-host/db/migrations"
	"github.com/vsriram/simple-host/internal/buildinfo"
	dbpkg "github.com/vsriram/simple-host/internal/db"
)

const migrateUsage = `usage: simple-host migrate [-init | -status | -mark FILE]

  (no flag)   Apply every pending file in db/migrations/, in order, each once,
              each in its own transaction, under a lock so two runs cannot race.
              Then checks the database has everything this build reads.
  -init       First build an EMPTY database from the embedded db/schema.sql
              (a database that already has the users table is left alone),
              then apply as above. For a container with no psql; safe to run
              on every start, and safe when two starts race.
  -status     List every migration file as applied, pending or baseline
              (historical files applied by hand before this tool; never run).
  -mark FILE  Record FILE as applied without running it, for a database where
              the SQL was applied by hand.

Reads DB_DSN from the environment. The server itself never migrates on start
unless MIGRATE_ON_START=true, which runs the -init form before it listens; on a
small box, re-running the install command runs this.`

func runMigrateCommand(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, migrateUsage) }
	status := fs.Bool("status", false, "")
	mark := fs.String("mark", "", "")
	initSchema := fs.Bool("init", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || (*status && *mark != "") || (*initSchema && (*status || *mark != "")) {
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
	if err := migrateDatabase(ctx, database, *initSchema, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// migrateDatabase is `simple-host migrate` (with -init when initSchema is set)
// on an open database: build an empty one from the embedded schema.sql when
// asked, apply every pending migration file, then check the database has
// everything this build reads. The server runs it before listening when
// MIGRATE_ON_START=true.
func migrateDatabase(ctx context.Context, database *sql.DB, initSchema bool, logf func(string, ...any)) error {
	if initSchema {
		built, err := migrations.Init(ctx, database, schema.SQL, logf)
		if err != nil {
			return fmt.Errorf("init failed: %w", err)
		}
		if built {
			logf("built the schema from db/schema.sql")
		}
	}
	ran, err := migrations.Apply(ctx, database, logf)
	if err != nil {
		return fmt.Errorf("migrate failed: %w", err)
	}
	if len(ran) == 0 {
		logf("database is up to date; nothing to apply")
	} else {
		logf("applied %d migration(s)", len(ran))
	}
	return dbpkg.VerifySchema(ctx, database)
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
