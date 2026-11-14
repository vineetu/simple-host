package storage

import (
	"testing"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

func TestLegacyAuthorizerRulesSchemaAlter(t *testing.T) {
	for _, tc := range []struct {
		name, mode, sql string
		owner, rules    bool
		allowed         bool
	}{
		{"rules rename", "schema", "ALTER TABLE entries RENAME TO renamed", true, true, true},
		{"rules drop column", "schema", "ALTER TABLE entries DROP COLUMN body", true, true, true},
		{"rules rename column", "schema", "ALTER TABLE entries RENAME COLUMN body TO content", true, true, true},
		{"rules add column", "schema", "ALTER TABLE entries ADD COLUMN extra TEXT", true, true, true},
		{"preset rename unchanged", "schema", "ALTER TABLE entries RENAME TO renamed", true, false, false},
		{"visitor rename", "schema", "ALTER TABLE entries RENAME TO renamed", false, true, false},
		{"query rename", "query", "ALTER TABLE entries RENAME TO renamed", true, true, false},
		{"execute rename", "execute", "ALTER TABLE entries RENAME TO renamed", true, true, false},
		{"temp table", "schema", "CREATE TEMP TABLE scratch(x)", true, true, false},
		{"temp view", "schema", "CREATE TEMP VIEW scratch AS SELECT * FROM entries", true, true, false},
		{"temp schema read", "query", "SELECT * FROM sqlite_temp_master", true, true, false},
		{"temp schema write", "schema", "UPDATE sqlite_temp_master SET name='forged'", true, true, false},
		{"attach", "schema", "ATTACH ':memory:' AS other", true, true, false},
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
			err = LegacyAuthorizer(conn, tc.mode, "anyone", "caller", tc.owner, tc.rules, &state,
				func(sqlite3.AuthorizerActionCode) sqlite3.AuthorizerReturnCode { return sqlite3.AUTH_DENY },
				func(a sqlite3.AuthorizerActionCode, _, _ string) sqlite3.AuthorizerActionCode { return a },
				func(string) sqlite3.AuthorizerActionCode { return sqlite3.AUTH_READ })
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
