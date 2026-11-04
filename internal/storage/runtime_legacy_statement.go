package storage

import (
	"encoding/json"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// PrepareLegacyStatement compiles a legacy SQL statement.
func PrepareLegacyStatement(conn *sqlite3.Conn, sql string) (*sqlite3.Stmt, string, error) {
	return conn.Prepare(sql)
}

// LegacyStatementInfo reports the parameter count and read-only flag.
func LegacyStatementInfo(stmt *sqlite3.Stmt) (int, bool) { return stmt.BindCount(), stmt.ReadOnly() }

// BindLegacyStatement binds each parameter and returns the failing index, if any.
func BindLegacyStatement(stmt *sqlite3.Stmt, params []json.RawMessage) int {
	for i, raw := range params {
		if BindStorageParam(stmt, i+1, raw) != nil {
			return i
		}
	}
	return -1
}

// ExecuteLegacyStatement executes a prepared statement.
func ExecuteLegacyStatement(stmt *sqlite3.Stmt) error { return stmt.Exec() }

// CloseLegacyStatement closes a prepared statement.
func CloseLegacyStatement(stmt *sqlite3.Stmt) error { return stmt.Close() }

// CheckpointLegacySQLite checkpoints a committed legacy write.
func CheckpointLegacySQLite(conn *sqlite3.Conn) error {
	_, _, err := conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
	return err
}
