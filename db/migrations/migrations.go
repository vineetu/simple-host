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
//   - is named NNNN-what.sql with the next free number (0001, 0002, ...).
//     The no-DB test names the next number when a file breaks this.
//
// Files are applied in apply order: the frozen [Legacy] list first, then the
// numbered files by number. Digits sort before letters, so this is not lexical
// order; `migrate -status` lists them in apply order. Each file runs once, in
// its own transaction together with the row that records it in
// schema_migrations, under a Postgres advisory lock so two runs never race.
// Apply refuses to run a pending numbered file at or below the newest applied one,
// even one this build does not embed (two branches took the same slot): renumber it.
// A file may keep its own BEGIN/COMMIT: the row is written first, so it commits
// with the file either way.
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
// the new app version starts. A startup data-only sweep seals legacy notification
// secrets when PASSCODE_ENC_KEY is set, only after the new binary starts.
// The server migrates schema on start only with MIGRATE_ON_START=true (off by
// default; Kubernetes, see cmd/server/main.go and docs/kubernetes.md). Hosted
// production leaves it off, so SQL applied by hand is never a surprise: there,
// apply the file by hand and record it with `simple-host migrate -mark <file>`
// (or run `simple-host migrate`, which is what the idempotency rule above makes safe).
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
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

// Legacy is the frozen list of tracked files predating numbered migrations. Never extend it.
var Legacy = []string{
	"ask-daily-count.sql",
	"cp-domains-custom-domain-certs.sql",
	"cp-gdpr-retired-handles.sql",
	"cp-int-previous-domain-checks.sql",
	"cp-keys-key-names.sql",
	"cp-ops-schema-migrations.sql",
	"cp-ops-suspend.sql",
	"cp-proof-domain-ownership.sql",
	"cp-recover-recently-deleted.sql",
	"hack1-events.sql",
	"hack10-event-icons.sql",
	"hack11-account-preferences.sql",
	"hack2-team-sites.sql",
	"hack3-judging.sql",
	"hack4-administration.sql",
	"hack5-content.sql",
	"hack6-registration.sql",
	"hack7-mail-retries.sql",
	"hack7-voting-directory.sql",
	"hack8-judging-options.sql",
	"hack9-event-websites.sql",
	"knobs-promised-dates.sql",
	"sd1-saved-data-safety.sql",
	"sd1-saved-data-safety2-limits.sql",
	"sd1-saved-data-safety3-history-bytes.sql",
	"sd2-saved-data-kinds.sql",
	"sd3-saved-data-personal-board.sql",
	"sd3-saved-data-personal-private.sql",
	"v061-setup-check-daily.sql",
	"v071-event-account-flag.sql",
	"v072-notify-last-id.sql",
	"v073-setup-assist-daily.sql",
	"v075-signup-source.sql",
	"v075-site-keep-versions.sql",
	"v076-site-passcode.sql",
	"v077-address-families.sql",
	"v078-api-growth.sql",
	"v079-api-self-calls.sql",
	"w2-account-signin-email.sql",
	"w2-addr-idle-cleanup.sql",
	"w2-signin-email-undo.sql",
	"w2-sites-offline.sql",
	"w2-sites-old-names.sql",
	"w3-analytics-pages-referrers.sql",
	"w3-connection-device.sql",
	"w3-keys-scope-expiry.sql",
	"w3-network-usage.sql",
	"z6-hack-connector-selected-team.sql",
	"z7-site-storage.sql",
	"z8-hack-ink-color.sql",
	"z9-home-page.sql",
	"za-showcase-curation.sql",
	"zb-storage-story.sql",
	"zc-site-named-viewers.sql",
	"zd-storage-access-presets.sql",
	"ze-spam-checked-posts.sql",
	"zf-signup-detail.sql",
	"zg-oidc-signin.sql",
	"zh-site-notifications.sql",
	"zi-site-search-summary.sql",
	"zj-hack-notifications.sql",
	"zk-hack-directory-opt-in.sql",
	"zk-hack-team-removal-notice.sql",
	"zl-hack-team-finding.sql",
	"zn-sso-hop.sql",
	"zn-visitor-ai.sql",
	"zo-sso-signout-confirm.sql",
	"zp-hack-applications.sql",
	"zq-hack-leave-event.sql",
	"zr-hack-team-kept-after-leave.sql",
	"zr-paper-metrics.sql",
	"zs-hack-paper-metrics.sql",
	"zt-hack-teams-redesign.sql",
	"zv-hack-entry-summary.sql",
	"zw-hack-wait-notices.sql",
	"zw-sim-152-version-author.sql",
	"zx-hack-entry-reminders.sql",
	"zx-sim-151-owner-visitor-marks.sql",
	"zz-sim-200-image-scan.sql",
}

// lockKey is the advisory lock every migrate run takes. Any constant works as
// long as nothing else in this database uses it.
const lockKey = 7461_2026_0927

const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
  name       TEXT PRIMARY KEY,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`

// numberedName is the only shape a new migration file may have.
var numberedName = regexp.MustCompile(`^[0-9]{4}-[a-z0-9]+(-[a-z0-9]+)*\.sql$`)

// number is a numbered file's number, or 0 for any other name.
func number(name string) int {
	if !numberedName.MatchString(name) {
		return 0
	}
	n, _ := strconv.Atoi(name[:4])
	return n
}

// Tracked returns the files the tool manages (everything but the baseline), in
// apply order.
func Tracked() []string {
	entries, _ := fs.ReadDir(files, ".")
	present := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") && !Baseline[e.Name()] {
			present[e.Name()] = true
		}
	}
	var names, numbered, other []string
	for _, n := range Legacy {
		if present[n] {
			names = append(names, n)
			delete(present, n)
		}
	}
	for n := range present {
		if number(n) > 0 {
			numbered = append(numbered, n)
		} else {
			other = append(other, n)
		}
	}
	sort.Strings(numbered)
	sort.Strings(other)
	return append(append(names, numbered...), other...)
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
		if !e.IsDir() && Baseline[e.Name()] {
			out = append(out, Entry{Name: e.Name(), State: "baseline"})
			delete(done, e.Name())
		}
	}
	for _, n := range Tracked() {
		if at, ok := done[n]; ok {
			out = append(out, Entry{Name: n, State: "applied", AppliedAt: at})
		} else {
			out = append(out, Entry{Name: n, State: "pending"})
		}
		delete(done, n)
	}
	var unknown []string
	for n := range done {
		unknown = append(unknown, n)
	}
	sort.Strings(unknown)
	for _, n := range unknown {
		out = append(out, Entry{Name: n, State: "applied", AppliedAt: done[n]})
	}
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
		// Every recorded name counts, including ones this build does not
		// embed (applied by another branch or a newer build), and a pending
		// file in the same slot is refused too.
		tracked := Tracked()
		var newest string
		for name := range done {
			if number(name) > number(newest) {
				newest = name
			}
		}
		for _, name := range tracked {
			if _, ok := done[name]; !ok && number(name) > 0 && number(name) <= number(newest) {
				return fmt.Errorf("pending %s is not above applied %s; renumber the new file above the newest applied one", name, newest)
			}
		}
		for _, name := range tracked {
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

// Init builds an empty database from schema (db/schema.sql, embedded by the
// db package) and reports whether it did. A database that already has the
// users table is left alone, so running it on every start is safe; it takes
// the same advisory lock as Apply, so two pods starting at once never both
// build the schema (the second waits, then sees the table and does nothing).
// Call Apply afterwards: schema.sql is current, so Apply only records each
// tracked file as applied.
func Init(ctx context.Context, db *sql.DB, schema string, logf func(string, ...any)) (bool, error) {
	built := false
	err := locked(ctx, db, func(conn *sql.Conn) error {
		var empty bool
		if err := conn.QueryRowContext(ctx, `SELECT to_regclass('public.users') IS NULL`).Scan(&empty); err != nil {
			return fmt.Errorf("look for the users table: %w", err)
		}
		if !empty {
			return nil
		}
		if logf != nil {
			logf("empty database: applying schema.sql")
		}
		if _, err := conn.ExecContext(ctx, `BEGIN`); err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, schema); err != nil {
			conn.ExecContext(context.Background(), `ROLLBACK`)
			return fmt.Errorf("schema.sql: %w (rolled back)", err)
		}
		if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
			return err
		}
		built = true
		return nil
	})
	return built, err
}
