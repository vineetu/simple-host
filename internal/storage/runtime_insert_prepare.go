package storage

import (
	"encoding/json"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

// PrepareInsert constructs the insert statement from validated values.
func PrepareInsert(table, rowid string, cols map[string]string, values map[string]json.RawMessage, abortOnConflict bool) (string, []json.RawMessage, error) {
	names, err := insertColumns(cols, values)
	if err != nil {
		return "", nil, err
	}
	query, params := insertRowStatement(table, rowid, abortOnConflict, names, values)
	return query, params, nil
}

// InsertSuppliesID reports whether values include a server-assigned ID.
func InsertSuppliesID(conn *sqlite3.Conn, table string, values map[string]json.RawMessage) (bool, error) {
	return insertSuppliesID(conn, table, values)
}
