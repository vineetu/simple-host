package storage

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// InsertColumns validates insert column names and returns them for statement construction.
func InsertColumns(cols map[string]string, values map[string]json.RawMessage) ([]string, error) {
	names := make([]string, 0, len(values))
	for name := range values {
		if _, ok := cols[name]; !ok {
			return nil, errors.New("invalid column")
		}
		names = append(names, name)
	}
	return names, nil
}

// InsertRowStatement constructs a parameterized insert for validated columns.
func InsertRowStatement(table, rowid string, abortOnConflict bool, names []string, values map[string]json.RawMessage) (string, []json.RawMessage) {
	sort.Strings(names)
	// OR ABORT overrides ON CONFLICT REPLACE for visitor inserts.
	query := "INSERT INTO " + Quote(table)
	if abortOnConflict {
		query = "INSERT OR ABORT INTO " + Quote(table)
	}
	params := []json.RawMessage{}
	if len(names) == 0 {
		query += " DEFAULT VALUES"
	} else {
		quoted, marks := []string{}, []string{}
		for _, name := range names {
			quoted = append(quoted, Quote(name))
			marks = append(marks, "?")
			params = append(params, values[name])
		}
		query += " (" + strings.Join(quoted, ",") + ") VALUES (" + strings.Join(marks, ",") + ")"
	}
	return query + " RETURNING " + rowid, params
}

// InsertSuppliesID reports whether an insert supplies a primary key or rowid.
func InsertSuppliesID(conn *sqlite3.Conn, table string, values map[string]json.RawMessage) (bool, error) {
	info, _, err := conn.Prepare("PRAGMA table_info(" + Quote(table) + ")")
	if err != nil {
		return false, err
	}
	defer info.Close()
	for info.Step() {
		if _, supplied := values[info.ColumnText(1)]; supplied && info.ColumnInt(5) > 0 {
			return true, nil
		}
	}
	for name := range values {
		if l := strings.ToLower(name); l == "rowid" || l == "_rowid_" || l == "oid" {
			return true, nil
		}
	}
	return false, nil
}

// SetRowAuthorizer confines writes to the target table.
func SetRowAuthorizer(conn *sqlite3.Conn, table string, write sqlite3.AuthorizerActionCode) error {
	return conn.SetAuthorizer(func(a sqlite3.AuthorizerActionCode, n3, n4, schema, inner string) sqlite3.AuthorizerReturnCode {
		if schema != "" && schema != "main" {
			return sqlite3.AUTH_DENY
		}
		switch a {
		case sqlite3.AUTH_SELECT, sqlite3.AUTH_READ, sqlite3.AUTH_RECURSIVE:
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_INSERT, sqlite3.AUTH_UPDATE, sqlite3.AUTH_DELETE:
			if a != write || n3 != table || inner != "" {
				return sqlite3.AUTH_DENY
			}
			return sqlite3.AUTH_OK
		case sqlite3.AUTH_FUNCTION:
			fn := strings.ToLower(n4)
			if fn == "load_extension" || fn == "readfile" || fn == "writefile" {
				return sqlite3.AUTH_DENY
			}
			return sqlite3.AUTH_OK
		}
		return sqlite3.AUTH_DENY
	})
}
