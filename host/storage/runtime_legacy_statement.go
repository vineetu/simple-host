package storage

import (
	"encoding/json"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// BindLegacyStatement binds each parameter and returns the failing index, if any.
func BindLegacyStatement(stmt *sqlite3.Stmt, params []json.RawMessage) int {
	for i, raw := range params {
		if BindStorageParam(stmt, i+1, raw) != nil {
			return i
		}
	}
	return -1
}

// CheckpointLegacySQLite checkpoints a committed legacy write.
func CheckpointLegacySQLite(conn *sqlite3.Conn) error {
	_, _, err := conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
	return err
}
