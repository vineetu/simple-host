package server

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vsriram/simple-host/host/buildinfo"
	schema "github.com/vsriram/simple-host/host/db"
	"github.com/vsriram/simple-host/host/db/migrations"
	"github.com/vsriram/simple-host/host/extension"
	operationrepo "github.com/vsriram/simple-host/host/repository/operations"
)

const migrateUsage = `usage: simple-host migrate [-init] [-record-product] | -status | -mark FILE

  (no flag)   Apply every pending file in host/db/migrations/, in order, each once,
              each in its own transaction, under a lock so two runs cannot race.
              Then checks the database has everything this build reads.
  -init       First build an EMPTY database from the embedded host/db/schema.sql
              (a database that already has the users table is left alone),
              then apply as above. For a container with no psql; safe to run
              on every start, and safe when two starts race.
  -record-product  Explicitly adopt an existing database after checking product evidence.
  -status     List every migration file as applied, pending or baseline
              (historical files applied by hand before this tool; never run).
  -mark FILE  Record FILE as applied without running it, for a database where
              the SQL was applied by hand.

Reads DB_DSN from the environment. The server itself never migrates on start
unless MIGRATE_ON_START=true, which runs the -init form before it listens; on a
small box, re-running the install command runs this.`

func runMigrateCommand(args []string, exts []*extension.Extension) int {
	set := migrationSet(exts)
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, migrateUsage) }
	status := fs.Bool("status", false, "")
	mark := fs.String("mark", "", "")
	initSchema := fs.Bool("init", false, "")
	record := fs.Bool("record-product", false, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || (*status && *mark != "") || ((*initSchema || *record) && (*status || *mark != "")) {
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

	// A database container that has only just started may refuse connections.
	if err := waitForDB(database, 2*time.Minute); err != nil {
		fmt.Fprintln(os.Stderr, "database not reachable:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	if *status {
		entries, err := set.Status(ctx, database)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		writeMigrationStatus(os.Stdout, entries)
		return 0
	}
	if *mark != "" {
		var added bool
		err := migrations.WithLock(ctx, database, func(conn *sql.Conn) error {
			if err := checkProduct(ctx, conn, exts, false); err != nil {
				return err
			}
			var err error
			added, err = set.MarkConn(ctx, conn, *mark)
			return err
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return productExitCode(err)
		}
		if added {
			fmt.Printf("recorded %s as applied\n", *mark)
		} else {
			fmt.Printf("%s was already recorded\n", *mark)
		}
		return 0
	}

	fmt.Println(buildinfo.String())
	if err := migrateDatabase(ctx, database, exts, *initSchema, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }, *record); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return productExitCode(err)
	}
	return 0
}

// migrateDatabase is `simple-host migrate` (with -init when initSchema is set)
// on an open database: build an empty one from the embedded schema.sql when
// asked, apply every pending migration file, then check the database has
// everything this build reads. The server runs it before listening when
// MIGRATE_ON_START=true.
func migrateDatabase(ctx context.Context, database *sql.DB, exts []*extension.Extension, initSchema bool, logf func(string, ...any), explicit bool) error {
	set := migrationSet(exts)
	err := migrations.WithLock(ctx, database, func(conn *sql.Conn) error {
		if err := checkProduct(ctx, conn, exts, explicit); err != nil {
			return err
		}
		if initSchema {
			if built, err := set.InitConn(ctx, conn, programProduct(exts), logf); err != nil {
				return fmt.Errorf("init failed: %w", err)
			} else if built && logf != nil {
				logf("built the schema from host/db/schema.sql")
			}
		}
		if explicit {
			return recordProduct(ctx, conn, set, exts, logf)
		}
		ran, err := set.ApplyConn(ctx, conn, logf)
		if err != nil {
			return fmt.Errorf("migrate failed: %w", err)
		}
		if logf != nil {
			if len(ran) == 0 {
				logf("database is up to date; nothing to apply")
			} else {
				logf("applied %d migration(s)", len(ran))
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	if err := operationrepo.VerifySchema(ctx, database); err != nil {
		return err
	}
	return checkProductSchema(ctx, database, exts)
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
func runVersionCommand(set migrations.Set) int {
	fmt.Println(buildinfo.String())
	tracked := set.Tracked()
	latest := "none"
	if len(tracked) > 0 {
		latest = tracked[len(tracked)-1]
	}
	fmt.Printf("migrations in this build: %d (last: %s); `simple-host migrate -status` compares them with the database\n", len(tracked), latest)
	return 0
}

func migrationSet(exts []*extension.Extension) migrations.Set {
	set := migrations.Set{Platform: schema.Catalog()}
	if len(exts) == 1 && exts[0] != nil {
		set.Product = exts[0].Migrations
		set.ProductOrder = exts[0].MigrationOrder
	}
	return set
}

func writeMigrationStatus(w io.Writer, entries []migrations.Entry) {
	pending, unknown := 0, 0
	for _, e := range entries {
		if e.Unknown {
			unknown++
			continue
		}
		when := ""
		if e.State == "applied" {
			when = e.AppliedAt.UTC().Format(time.RFC3339)
		}
		if e.State == "pending" {
			pending++
		}
		fmt.Fprintf(w, "%-9s %-45s %s\n", e.State, e.Name, when)
	}
	fmt.Fprintf(w, "%d pending\n", pending)
	if unknown > 0 {
		fmt.Fprintf(w, "%d recorded files are not part of this program\n", unknown)
	}
}

// checkProductSchema follows platform verification at startup and after migrations.
func checkProductSchema(ctx context.Context, database *sql.DB, exts []*extension.Extension) error {
	if len(exts) == 1 && exts[0] != nil && exts[0].CheckSchema != nil {
		return exts[0].CheckSchema(ctx, database)
	}
	return nil
}
