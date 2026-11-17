package storage

import (
	"context"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

// CheckpointExportSQLite folds the WAL into a SQLite file before it is exported.
func CheckpointExportSQLite(ctx context.Context, path string, maxMemory int64) (err error) {
	// A checkpoint grows no logical data; size it for the existing DB and WAL.
	size, e := sqliteReadSize(path)
	if e != nil {
		return e
	}
	ctx, cache := sqliteMemoryContext(ctx, size, maxMemory)
	conn, e := sqlite3.OpenContext(ctx, path)
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
	if e = configureSQLiteMemory(conn, cache); e != nil {
		return e
	}
	sqliteStage(ctx, "before_checkpoint")
	_, _, e = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
	return e
}
