package storage

import (
	"context"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

// CheckpointExportSQLite folds the WAL into a SQLite file before it is exported.
func CheckpointExportSQLite(ctx context.Context, path string, maxMemory int64) error {
	conn, e := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, maxMemory), path)
	if e != nil {
		return e
	}
	_, _, e = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
	if ce := conn.Close(); e == nil {
		e = ce
	}
	return e
}
