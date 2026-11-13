package storage

import (
	"context"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

// CheckpointExportSQLite folds the WAL into a SQLite file before it is exported.
func CheckpointExportSQLite(ctx context.Context, path string, maxMemory int64) (err error) {
	conn, e := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, maxMemory), path)
	if e != nil {
		return e
	}
	defer closeTableConn(func() {
		defer func() {
			if ce := conn.Close(); err == nil {
				err = ce
			}
		}()
		sqliteStage(ctx, "close")
	})
	sqliteStage(ctx, "after_open")
	conn.SetInterrupt(ctx)
	sqliteStage(ctx, "before_checkpoint")
	_, _, e = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
	return e
}
