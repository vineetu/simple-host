package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

type recoveryConnHookKey struct{}

func init() {
	sqlite3.AutoExtension(func(conn *sqlite3.Conn) error {
		if hook, ok := conn.GetInterrupt().Value(recoveryConnHookKey{}).(func(*sqlite3.Conn) error); ok {
			return hook(conn)
		}
		return nil
	})
}

func TestOpenTableConnPageStepPanicClosesConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite")
	seed, err := sqlite3.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	var conn *sqlite3.Conn
	closes := 0
	ctx := context.WithValue(context.Background(), recoveryConnHookKey{}, func(c *sqlite3.Conn) error {
		conn = c
		return c.Trace(sqlite3.TRACE_STMT|sqlite3.TRACE_CLOSE, func(event sqlite3.TraceEvent, _, query any) error {
			if event == sqlite3.TRACE_CLOSE {
				closes++
			}
			if event == sqlite3.TRACE_STMT && query == "PRAGMA page_size" {
				panic("page step")
			}
			return nil
		})
	})
	func() {
		defer func() {
			if p := recover(); p != "page step" {
				t.Errorf("panic = %v, want page step", p)
			}
		}()
		_, done, err := OpenTableConn(ctx, path, true, time.Second, 256<<20, func() (int64, error) { return 1 << 20, nil })
		if done != nil {
			done()
		}
		t.Errorf("open returned instead of panicking: %v", err)
	}()
	if conn == nil {
		t.Fatal("connection never opened")
	}
	for stmt := range conn.Stmts() {
		t.Errorf("statement leaked: %s", stmt.SQL())
		_ = stmt.Close()
	}
	// TRACE_CLOSE fires even when Close returns BUSY. A second Close must
	// be a no-op, proving the first one actually cleared the connection.
	if closes != 1 {
		t.Errorf("close attempts = %d, want one setup cleanup", closes)
	}
	before := closes
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if closes != before {
		t.Fatal("setup cleanup left the connection open")
	}
}

func TestRowMetadataStepPanicClosesStatements(t *testing.T) {
	for _, query := range []string{
		`PRAGMA foreign_key_list("child")`,
		`PRAGMA table_info("parent")`,
		`SELECT (c."parent_id" IS NULL), (c."parent_id" IS NULL), p."id" IS NOT NULL, '' FROM "child" c LEFT JOIN "parent" p ON p."id" = c."parent_id" WHERE c._rowid_ = ?`,
		`PRAGMA table_info("child")`,
	} {
		t.Run(query, func(t *testing.T) {
			conn, err := sqlite3.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				for stmt := range conn.Stmts() {
					_ = stmt.Close()
				}
				_ = conn.Close()
			})
			if err := conn.Exec(`CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER PRIMARY KEY, parent_id REFERENCES parent); INSERT INTO parent VALUES(1); INSERT INTO child VALUES(1, 1)`); err != nil {
				t.Fatal(err)
			}
			seen := 0
			if err := conn.Trace(sqlite3.TRACE_STMT, func(event sqlite3.TraceEvent, _, sql any) error {
				if sql == query {
					seen++
					// The first parent table_info belongs to TableColumns;
					// the second resolves the implicit foreign-key target.
					if query != `PRAGMA table_info("parent")` || seen == 2 {
						panic("metadata step")
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			func() {
				defer func() {
					if p := recover(); p != "metadata step" {
						t.Errorf("panic = %v, want metadata step", p)
					}
				}()
				if query == `PRAGMA table_info("child")` {
					_, err = PrimaryKeyColumns(conn, "child")
				} else {
					_, err = CheckReferences(conn, "child", "_rowid_", 1, func(string) (string, bool, bool) { return "", true, false })
				}
				t.Errorf("helper returned instead of panicking: %v", err)
			}()
			// Unlike a synthetic closed hook, this fails with SQLITE_BUSY
			// if the panic left any prepared statement outstanding.
			if err := conn.Close(); err != nil {
				t.Fatalf("connection not closed after panic: %v", err)
			}
		})
	}
}

func TestPrimaryKeyColumnsStepError(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE notes(id INTEGER PRIMARY KEY, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn.SetInterrupt(ctx)
	if err := conn.SetAuthorizer(func(sqlite3.AuthorizerActionCode, string, string, string, string) sqlite3.AuthorizerReturnCode {
		// Preparation succeeds, but Step observes cancellation.
		cancel()
		return sqlite3.AUTH_OK
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := PrimaryKeyColumns(conn, "notes"); !errors.Is(err, sqlite3.INTERRUPT) {
		t.Fatalf("statement error lost: %v", err)
	}
}

func TestTableColumnsPreservesMetadataError(t *testing.T) {
	conn, err := sqlite3.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.Exec(`CREATE TABLE notes(id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetAuthorizer(func(sqlite3.AuthorizerActionCode, string, string, string, string) sqlite3.AuthorizerReturnCode {
		return sqlite3.AUTH_DENY
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := TableColumns(conn, "notes"); !errors.Is(err, sqlite3.AUTH) {
		t.Fatalf("metadata error replaced: %v", err)
	}
}
