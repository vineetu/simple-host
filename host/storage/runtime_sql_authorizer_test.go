package storage

import (
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestLegacyAuthorizerSchemaAlter(t *testing.T) {
	for _, tc := range []struct {
		name, mode, sql string
		allowed         bool
	}{
		{"rules rename", "schema", "ALTER TABLE entries RENAME TO renamed", true},
		{"rules drop column", "schema", "ALTER TABLE entries DROP COLUMN body", true},
		{"rules rename column", "schema", "ALTER TABLE entries RENAME COLUMN body TO content", true},
		{"rules add column", "schema", "ALTER TABLE entries ADD COLUMN extra TEXT", true},
		{"query rename", "query", "ALTER TABLE entries RENAME TO renamed", false},
		{"execute rename", "execute", "ALTER TABLE entries RENAME TO renamed", false},
		{"temp table", "schema", "CREATE TEMP TABLE scratch(x)", false},
		{"temp view", "schema", "CREATE TEMP VIEW scratch AS SELECT * FROM entries", false},
		{"temp schema read", "query", "SELECT * FROM sqlite_temp_master", false},
		{"temp schema write", "schema", "UPDATE sqlite_temp_master SET name='forged'", false},
		{"attach", "schema", "ATTACH ':memory:' AS other", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := sqlite3.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.Exec("CREATE TABLE entries(id INTEGER PRIMARY KEY, body TEXT)"); err != nil {
				t.Fatal(err)
			}
			var state LegacyAuthorizerState
			err = LegacyAuthorizer(conn, tc.mode, &state,
				func(sqlite3.AuthorizerActionCode) sqlite3.AuthorizerReturnCode { return sqlite3.AUTH_DENY },
				func(a sqlite3.AuthorizerActionCode, _, _ string) sqlite3.AuthorizerActionCode { return a })
			if err != nil {
				t.Fatal(err)
			}
			err = conn.Exec(tc.sql)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v, error=%v", tc.allowed, err)
			}
		})
	}
}
