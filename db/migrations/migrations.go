// Package migrations holds every schema change file and applies the pending
// ones: `simple-host migrate`.
//
// # The rule for a new migration file
//
// Every schema change edits db/schema.sql AND adds one file here. The file:
//
//   - is plain SQL (no psql meta-commands such as \set or \echo);
//   - is idempotent: ADD COLUMN IF NOT EXISTS, CREATE TABLE/INDEX IF NOT EXISTS,
//     DROP ... IF EXISTS before a re-ADD. A database built from schema.sql
//     already has its effect, and the tool still runs it there once, so it must
//     be a no-op on a database that already has the change;
//   - is safe on a live database: additive, with defaults, no long locks, no
//     table rewrites of big tables, no CREATE INDEX CONCURRENTLY (it runs inside
//     a transaction). Old binaries keep running against it until they restart;
//   - is named so that lexical order is apply order. Hosted batches use
//     <batch>-<what>.sql (e.g. cp-ops-suspend.sql).
//
// Files are applied in lexical order, each once, each in its own transaction
// together with the row that records it in schema_migrations, under a Postgres
// advisory lock so two runs never race. A file may keep its own BEGIN/COMMIT:
// the row is written first, so it commits with the file either way.
//
// # Historical files
//
// The files listed in [Baseline] were applied by hand before this tool existed,
// are already folded into schema.sql, and are not all idempotent. The tool never
// runs them and never records them; `migrate -status` shows them as baseline.
// Do not add names to that list: a file added from now on is applied.
//
// # Where it runs
//
// A small box runs `simple-host migrate` from install.sh on every run, before
// the new app version starts. The server never migrates on start, so hosted
// production, where SQL is applied by hand, is never surprised: there, apply the
// file by hand and record it with `simple-host migrate -mark <file>` (or run
// `simple-host migrate`, which is what the idempotency rule above makes safe).
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed *.sql
var embedded embed.FS

// files is where migrations are read from; tests point it elsewhere.
var files fs.ReadFileFS = embedded

// Baseline is the fixed list of historical, hand-applied files. Never extend it.
var Baseline = map[string]bool{
	"0b-legacy-hostnames.sql":          true,
	"0c1-handles.sql":                  true,
	"0e-per-user-names.sql":            true,
	"1a-custom-domain-cols.sql":        true,
	"analytics-geo.sql":                true,
	"analytics-v2.sql":                 true,
	"analytics.sql":                    true,
	"api-analytics.sql":                true,
	"auth-token-purpose.sql":           true,
	"display-name.sql":                 true,
	"domain-bound-at.sql":              true,
	"event-domains.sql":                true,
	"handle-aliases.sql":               true,
	"hash-api-keys-drop-plaintext.sql": true,
	"hash-api-keys.sql":                true,
	"instance-config.sql":              true,
	"local-geo.sql":                    true,
	"oauth-connector.sql":              true,
	"private-collections.sql":          true,
	"prod-0b-legacy-hostnames.sql":     true,
	"prod-0e-per-user-names.sql":       true,
	"truncate-api-ip.sql":              true,
	"unify-identities.sql":             true,
	"visitor-grants.sql":               true,
	"visitor-oauth.sql":                true,
	"visitor-signin-nonce.sql":         true,
}

// lockKey is the advisory lock every migrate run takes. Any constant works as
// long as nothing else in this database uses it.
const lockKey = 7461_2026_0927

const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
  name       TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// Tracked returns the files the tool manages (everything but the baseline), in
// apply order.
func Tracked() []string {
	entries, _ := fs.ReadDir(files, ".")
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && !Baseline[e.Name()] {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// Entry is one line of `migrate -status`.
type Entry struct {
	Name      string
	State     string // "applied", "pending" or "baseline"
	AppliedAt time.Time
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// applied reads the recorded names. A database without the table has none.
func applied(ctx context.Context, q querier) (map[string]time.Time, error) {
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, fmt.Errorf("look for schema_migrations: %w", err)
	}
	done := map[string]time.Time{}
	if !exists {
		return done, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT name, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var n string
		var at time.Time
		if err := rows.Scan(&n, &at); err != nil {
			return nil, err
		}
		done[n] = at
	}
	return done, rows.Err()
}

// Status lists every embedded file with its state. It writes nothing.
func Status(ctx context.Context, db *sql.DB) ([]Entry, error) {
	done, err := applied(ctx, db)
	if err != nil {
		return nil, err
	}
	entries, _ := fs.ReadDir(files, ".")
	var out []Entry
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".sql") {
			continue
		}
		switch at, ok := done[n]; {
		case Baseline[n]:
			out = append(out, Entry{Name: n, State: "baseline"})
		case ok:
			out = append(out, Entry{Name: n, State: "applied", AppliedAt: at})
		default:
			out = append(out, Entry{Name: n, State: "pending"})
		}
		delete(done, n)
	}
	// Recorded by a newer build (this one was rolled back to): still applied.
	for n, at := range done {
		out = append(out, Entry{Name: n, State: "applied", AppliedAt: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// locked runs fn on one connection holding the migrate advisory lock, with the
// tracking table in place.
func locked(ctx context.Context, db *sql.DB, fn func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("take the migrate lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey)
	if _, err := conn.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return fn(conn)
}

// Apply runs every pending file, in order, each once. It returns the names it
// applied; on failure it stops at the failing file, which is rolled back and
// left pending, and reports the files applied before it.
func Apply(ctx context.Context, db *sql.DB, logf func(string, ...any)) ([]string, error) {
	var ran []string
	err := locked(ctx, db, func(conn *sql.Conn) error {
		// Read under the lock: a run that waited on another sees its work.
		done, err := applied(ctx, conn)
		if err != nil {
			return err
		}
		for _, name := range Tracked() {
			if _, ok := done[name]; ok {
				continue
			}
			body, err := files.ReadFile(name)
			if err != nil {
				return err
			}
			if logf != nil {
				logf("applying %s", name)
			}
			if err := applyOne(ctx, conn, name, string(body)); err != nil {
				return fmt.Errorf("%s: %w (rolled back; nothing after it was run)", name, err)
			}
			ran = append(ran, name)
		}
		return nil
	})
	return ran, err
}

// applyOne runs one file and records it in a single transaction. The
// transaction is driven with plain BEGIN/COMMIT rather than database/sql's Tx
// because some files carry their own BEGIN; ... COMMIT;. Inside ours, their
// BEGIN is a warning and their COMMIT commits everything so far, including the
// row, which is why the row is written before the file runs.
func applyOne(ctx context.Context, conn *sql.Conn, name, body string) error {
	if _, err := conn.ExecContext(ctx, `BEGIN`); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES ($1)`, name); err != nil {
		conn.ExecContext(context.Background(), `ROLLBACK`)
		return err
	}
	if _, err := conn.ExecContext(ctx, body); err != nil {
		conn.ExecContext(context.Background(), `ROLLBACK`)
		return err
	}
	_, err := conn.ExecContext(ctx, `COMMIT`)
	return err
}

// Mark records a file as applied without running it: for a database where the
// SQL was applied by hand (hosted production).
func Mark(ctx context.Context, db *sql.DB, name string) (bool, error) {
	known := false
	for _, n := range Tracked() {
		if n == name {
			known = true
		}
	}
	if !known {
		if Baseline[name] {
			return false, fmt.Errorf("%s is a historical file; it is never tracked", name)
		}
		return false, fmt.Errorf("%s is not a migration file in this build", name)
	}
	var added bool
	err := locked(ctx, db, func(conn *sql.Conn) error {
		res, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations (name) VALUES ($1) ON CONFLICT (name) DO NOTHING`, name)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		added = n == 1
		return nil
	})
	return added, err
}
