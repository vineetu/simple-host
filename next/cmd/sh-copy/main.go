// Command sh-copy copies the old Simple Host's people, sites and saved data
// into the new service, checks the copy, and lists the owners affected by
// what is left behind. It reads the old Postgres in one read-only snapshot
// and the old data folder, and writes only the new database and data folder.
//
//	sh-copy copy     copy, or copy again: keeps ids, upserts, removes what is gone
//	sh-copy check    compare old and new; prints each difference (none = pass)
//	sh-copy report   the owners affected by something dropped
//
// Environment: OLD_DATABASE_URL (a read-only role) and OLD_DATA_DIR for the
// old service; DATABASE_URL, SH_DATA_DIR, SH_APP_DOMAIN, SH_SITE_DOMAIN and
// PASSCODE_ENC_KEY as the new service reads them; TURNSTILE_SITE_KEY and
// TURNSTILE_SECRET (the old service's) for saved data whose rules use the
// spam check.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/lib/pq"

	access "simplehost/access/core"
	"simplehost/app"
	"simplehost/core/db"
	data "simplehost/data/core"
	sites "simplehost/sites/core"
)

type config struct {
	New                           app.Config
	OldDB, OldDir                 string
	TurnstileKey, TurnstileSecret string
}

func main() {
	if len(os.Args) != 2 || !strings.Contains(" copy check report ", " "+os.Args[1]+" ") {
		fmt.Fprintln(os.Stderr, "usage: sh-copy copy|check|report")
		os.Exit(2)
	}
	cfg := config{New: app.FromEnv(), OldDB: os.Getenv("OLD_DATABASE_URL"), OldDir: os.Getenv("OLD_DATA_DIR"),
		TurnstileKey: os.Getenv("TURNSTILE_SITE_KEY"), TurnstileSecret: os.Getenv("TURNSTILE_SECRET")}
	n, err := run(context.Background(), cfg, os.Args[1], os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sh-copy:", err)
		os.Exit(1)
	}
	if os.Args[1] == "check" && n > 0 {
		os.Exit(1)
	}
}

type runner struct {
	cfg     config
	old     *sql.Tx // one read-only snapshot of the old database
	new     *sql.DB
	sites   *sites.Sites
	tmp     string          // scratch folder inside the new data folder
	keep    map[string]bool // new folders and files the copy wants (the rest is pruned)
	changes int             // copy: rows and files changed
	diffs   []string        // check: differences found
	notes   []note          // report: things not carried, by site
}

type note struct{ what, siteID, ownerID, detail string }

// run does one command and returns its count: changes made (copy),
// differences (check) or affected lines (report).
func run(ctx context.Context, cfg config, cmd string, out io.Writer) (int, error) {
	if cfg.OldDB == "" || cfg.OldDir == "" || cfg.New.DatabaseURL == "" {
		return 0, fmt.Errorf("set OLD_DATABASE_URL, OLD_DATA_DIR and DATABASE_URL")
	}
	oldURL, err := withParams(cfg.OldDB, "default_transaction_read_only", "on")
	if err != nil {
		return 0, err
	}
	oldDB, err := db.Open(ctx, oldURL)
	if err != nil {
		return 0, fmt.Errorf("old database: %w", err)
	}
	defer oldDB.Close()
	tx, err := oldDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	newURL, err := withParams(cfg.New.DatabaseURL, "timezone", "UTC")
	if err != nil {
		return 0, err
	}
	newDB, err := db.Open(ctx, newURL)
	if err != nil {
		return 0, fmt.Errorf("new database: %w", err)
	}
	defer newDB.Close()
	if err := db.Apply(ctx, newDB, access.Schema, sites.Schema, data.Schema, db.Schema); err != nil {
		return 0, err
	}
	nc := cfg.New
	r := &runner{cfg: cfg, old: tx, new: newDB, keep: map[string]bool{}, tmp: filepath.Join(nc.DataDir, ".sh-copy")}
	if r.sites, err = sites.New(newDB, &access.People{DB: newDB}, nc.DataDir, nc.AppDomain, nc.SiteDomain, nc.PasscodeKey); err != nil {
		return 0, err
	}
	os.RemoveAll(r.tmp)
	if err := os.MkdirAll(r.tmp, 0o755); err != nil {
		return 0, err
	}
	defer os.RemoveAll(r.tmp)
	switch cmd {
	case "copy":
		if err := r.copy(ctx); err != nil {
			return r.changes, err
		}
		fmt.Fprintf(out, "%d changes\n", r.changes)
		return r.changes, nil
	case "check":
		if err := r.check(ctx); err != nil {
			return len(r.diffs), err
		}
		for _, d := range r.diffs {
			fmt.Fprintln(out, d)
		}
		fmt.Fprintf(out, "%d differences\n", len(r.diffs))
		return len(r.diffs), nil
	}
	return r.report(ctx, out)
}

// withParams adds run-time settings to a postgres:// address. Old reads are
// read-only at the server, and both sides speak UTC so values compare as text.
func withParams(dsn string, kv ...string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil || u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("database addresses must be postgres://... URLs")
	}
	q := u.Query()
	q.Set("timezone", "UTC")
	for i := 0; i+1 < len(kv); i += 2 {
		q.Set(kv[i], kv[i+1])
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// sql fills the shared fragments of an old-side query.
func (r *runner) sql(q string) string {
	return strings.NewReplacer("{copied}", copiedSites, "{app}", pq.QuoteLiteral(r.cfg.New.AppDomain),
		"{dir}", siteDir, "{never}", "'2025-01-01 00:00:00+00'::timestamptz").Replace(q)
}

func (r *runner) diff(format string, a ...any) { r.diffs = append(r.diffs, fmt.Sprintf(format, a...)) }

func (r *runner) note(what, siteID, format string, a ...any) {
	r.notes = append(r.notes, note{what, siteID, "", fmt.Sprintf(format, a...)})
}
