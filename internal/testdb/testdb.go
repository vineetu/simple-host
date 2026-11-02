// Package testdb gives each DB-backed test process its own disposable
// Postgres database, cloned from the database named in DB_DSN.
//
// Every package with DB-backed tests calls Main from its TestMain:
//
//	func TestMain(m *testing.M) { testdb.Main(m) }
//
// With TESTDB_ISOLATE=1 (set by scripts/test-integration.sh) and DB_DSN
// set, Main runs `CREATE DATABASE <clone> TEMPLATE <db from DB_DSN>`,
// points DB_DSN at the clone for the whole process, and drops the clone
// afterwards. The template database is never connected to, so it stays
// usable as a template for the next package. Without TESTDB_ISOLATE=1,
// Main is a plain m.Run(): an ad-hoc DB_DSN (which might be a real
// database) is never used as a template and never dropped.
package testdb

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/lib/pq"
)

// templateEnv holds the original DB_DSN once Main has repointed DB_DSN at
// the per-process clone; Fresh clones from it.
const templateEnv = "TESTDB_TEMPLATE_DSN"

func isolating() bool { return os.Getenv("TESTDB_ISOLATE") == "1" }

// Main runs the package's tests, inside a per-process clone of the DB_DSN
// database when TESTDB_ISOLATE=1. It never returns.
func Main(m *testing.M) {
	dsn := os.Getenv("DB_DSN")
	if !isolating() || dsn == "" {
		os.Exit(m.Run())
	}
	cloneDSN, drop, err := clone(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb: %v\n", err)
		os.Exit(1)
	}
	os.Setenv(templateEnv, dsn)
	os.Setenv("DB_DSN", cloneDSN)
	code := m.Run()
	if err := drop(); err != nil {
		fmt.Fprintf(os.Stderr, "testdb: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// Fresh returns the DSN of a brand-new clone of the template database,
// dropped when the test ends. It is for tests whose code under test reads
// process-global queues that cannot be scoped to the test's own rows. It
// skips when there is no database or isolation is off.
func Fresh(t *testing.T) string {
	t.Helper()
	tmpl := os.Getenv(templateEnv)
	if !isolating() || tmpl == "" {
		t.Skip("DB_DSN unset or TESTDB_ISOLATE!=1: no template database to clone")
	}
	dsn, drop, err := clone(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := drop(); err != nil {
			t.Error(err)
		}
	})
	return dsn
}

// clone creates a uniquely named copy of the database dsn points at and
// returns its DSN plus a function that drops it. dsn must be a postgres://
// URL. The work is done over a connection to the server's "postgres"
// database, never to the template itself.
func clone(dsn string) (string, func() error, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		return "", nil, fmt.Errorf("DB_DSN must be a postgres:// URL to clone it")
	}
	tmpl := strings.TrimPrefix(u.Path, "/")
	if tmpl == "" {
		return "", nil, fmt.Errorf("DB_DSN has no database name")
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("%s_t%d_%s", tmpl, os.Getpid(), hex.EncodeToString(b[:]))
	if len(name) > 63 {
		name = name[len(name)-63:]
	}

	adminURL := *u
	adminURL.Path = "/postgres"
	admin, err := sql.Open("postgres", adminURL.String())
	if err != nil {
		return "", nil, err
	}
	defer admin.Close()
	if _, err := admin.Exec(`CREATE DATABASE ` + pq.QuoteIdentifier(name) + ` TEMPLATE ` + pq.QuoteIdentifier(tmpl)); err != nil {
		return "", nil, fmt.Errorf("clone %s from template %s: %w", name, tmpl, err)
	}

	cloneURL := *u
	cloneURL.Path = "/" + name
	drop := func() error {
		a, err := sql.Open("postgres", adminURL.String())
		if err != nil {
			return err
		}
		defer a.Close()
		if _, err := a.Exec(`DROP DATABASE IF EXISTS ` + pq.QuoteIdentifier(name) + ` WITH (FORCE)`); err != nil {
			return fmt.Errorf("drop clone %s: %w", name, err)
		}
		return nil
	}
	return cloneURL.String(), drop, nil
}
