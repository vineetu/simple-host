package storage

import (
	"encoding/json"
	"os"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// PrimaryKeyColumns reads primary key columns for a table.
func PrimaryKeyColumns(conn *sqlite3.Conn, table string) (map[string]bool, error) {
	pk := map[string]bool{}
	info, _, err := conn.Prepare("PRAGMA table_info(" + Quote(table) + ")")
	if err != nil {
		return nil, err
	}
	for info.Step() {
		if info.ColumnInt(5) > 0 {
			pk[info.ColumnText(1)] = true
		}
	}
	info.Close()
	return pk, nil
}

// CheckpointRows truncates the SQLite write-ahead log after a commit.
func CheckpointRows(conn *sqlite3.Conn) error {
	_, _, err := conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
	return err
}

// RemoveStagedRuntime removes an already staged runtime directory.
func RemoveStagedRuntime(path string) error { return os.RemoveAll(path) }

// RowUpdateStatement builds the update from validated columns and decisions.
func RowUpdateStatement(table, rowid string, names []string, values map[string]json.RawMessage, id json.RawMessage, filter string, owner, hideHeld bool) (string, []json.RawMessage) {
	sets := []string{}
	params := []json.RawMessage{}
	for _, name := range names {
		sets = append(sets, Quote(name)+" = ?")
		params = append(params, values[name])
	}
	query := "UPDATE OR ABORT " + Quote(table) + " SET " + strings.Join(sets, ", ") + " WHERE " + rowid + " = ?"
	if owner {
		query = "UPDATE " + Quote(table) + " SET " + strings.Join(sets, ", ") + " WHERE " + rowid + " = ?"
	}
	params = append(params, id)
	if filter != "" {
		query += " AND visitor_id = ?"
		b, _ := json.Marshal(filter)
		params = append(params, b)
	}
	if hideHeld {
		query += " AND approved_at IS NOT NULL"
	}
	return query, params
}

// RowDeleteStatement builds the delete from validated decisions.
func RowDeleteStatement(table, rowid string, id json.RawMessage, filter string, hideHeld bool) (string, []json.RawMessage) {
	query := "DELETE FROM " + Quote(table) + " WHERE " + rowid + " = ?"
	params := []json.RawMessage{id}
	if filter != "" {
		query += " AND visitor_id = ?"
		b, _ := json.Marshal(filter)
		params = append(params, b)
	}
	if hideHeld {
		query += " AND approved_at IS NOT NULL"
	}
	return query, params
}
