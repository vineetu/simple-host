// Package testdb gives each DB-backed test process its own disposable
// Postgres database, cloned from the platform-only schema template.
// Product fixtures supply their composed template through MainWithSchema and
// FreshWithSchema. The integration runner assigns DB_DSN by package directory.
//
// DB-backed test packages call Main from their TestMain:
//
//	func TestMain(m *testing.M) { testdb.Main(m) }
//
// A package with its own TestMain may instead call Fresh for each DB test.
// DB hands every test the package database's one pool.
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
	"sync"
	"testing"

	"github.com/lib/pq"
)

// templateEnv holds the original DB_DSN once Main has repointed DB_DSN at
// the per-process clone; Fresh clones from it.
const templateEnv = "TESTDB_TEMPLATE_DSN"

func isolating() bool { return os.Getenv("TESTDB_ISOLATE") == "1" }

// shared is the pool on the package database, the DB_DSN Main ran tests with.
var shared struct {
	dsn  string
	once sync.Once
	db   *sql.DB
	err  error
}

// DB returns a pool on the test's DB_DSN, skipping when there is none or it
// does not answer. Tests on the package database share one pool, opened on
// first use; a test that moved DB_DSN to its own clone gets its own pool,
// closed when the test ends.
func DB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN unset")
	}
	if dsn == shared.dsn {
		shared.once.Do(func() { shared.db, shared.err = open(dsn) })
		if shared.err != nil {
			t.Skipf("postgres not reachable: %v", shared.err)
		}
		return shared.db
	}
	database, err := open(dsn)
	if err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func open(dsn string) (*sql.DB, error) {
	database, err := sql.Open("postgres", dsn)
	if err == nil {
		err = database.Ping()
	}
	return database, err
}

// run runs the tests on the package database in DB_DSN, then closes its pool.
func run(m *testing.M) int {
	shared.dsn = os.Getenv("DB_DSN")
	code := m.Run()
	if shared.db != nil {
		shared.db.Close()
	}
	return code
}

// Main runs the package's tests, inside a per-process clone of the DB_DSN
// database when TESTDB_ISOLATE=1. It never returns.
func Main(m *testing.M) {
	MainWithSchema(m, os.Getenv("TESTDB_PLATFORM_DSN"))
}

// MainWithSchema runs tests with the caller's schema set, represented by an
// already-built template DSN. An empty template uses DB_DSN for ad-hoc runs.
// The integration runner selects DB_DSN by package independently of this helper.
func MainWithSchema(m *testing.M, template string) {
	dsn := template
	if dsn == "" {
		dsn = os.Getenv("DB_DSN")
	}
	if !isolating() || os.Getenv("DB_DSN") == "" {
		os.Exit(run(m))
	}
	cloneDSN, drop, err := clone(dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb: %v\n", err)
		os.Exit(1)
	}
	os.Setenv(templateEnv, dsn)
	os.Setenv("DB_DSN", cloneDSN)
	code := run(m)
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
// skips when there is no database or isolation is off. Packages with only
// one DB-backed test may call Fresh without installing Main.
func Fresh(t *testing.T) string {
	t.Helper()
	return FreshWithSchema(t, os.Getenv("TESTDB_PLATFORM_DSN"))
}

// FreshWithSchema clones the caller's schema set. With no explicit template it
// uses the process's original template, or DB_DSN outside MainWithSchema.
func FreshWithSchema(t *testing.T, template string) string {
	t.Helper()
	tmpl := template
	if tmpl == "" {
		tmpl = os.Getenv(templateEnv)
	}
	if tmpl == "" {
		tmpl = os.Getenv("DB_DSN")
	}
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
