package storage

import (
	"context"
	"fmt"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// PrepareMatrixDatabase applies the SQLite changes needed for a resource matrix.
// Only rules saves resolve system columns case-insensitively; preset row paths
// require their lowercase spelling for stamping and redaction.
func PrepareMatrixDatabase(ctx context.Context, path string, maxMemory, limit, used, fileSize int64, requireEmpty, clearIDs, rulesSave bool, ownTables, holdTables func(string) bool, checkpoint func(error), markChanged func(), committed func(map[string]bool)) (map[string]bool, error) {
	existing := map[string]bool{}
	ctx, cache := sqliteMemoryContext(ctx, min(limit, fileSize+limit-used), maxMemory)
	conn, err := sqlite3.OpenContext(ctx, path)
	if err != nil {
		return existing, err
	}
	defer closeTableConn(func() { defer conn.Close(); sqliteStage(ctx, "close") })
	sqliteStage(ctx, "after_open")
	conn.SetInterrupt(ctx)
	if err = configureSQLiteMemory(conn, cache); err != nil {
		return existing, err
	}
	if err = conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return existing, err
	}
	page, _, err := conn.Prepare("PRAGMA page_size")
	if err != nil {
		return existing, err
	}
	defer page.Close()
	sqliteStage(ctx, "after_prepare")
	if !page.Step() {
		page.Close()
		return existing, fmt.Errorf("page size unavailable")
	}
	sqliteStage(ctx, "after_step")
	size := page.ColumnInt64(0)
	page.Close()
	maxPages := (limit - (used - fileSize)) / size
	if maxPages < 1 || fileSize > maxPages*size {
		return existing, sqlite3.FULL
	}
	if err = conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
		return existing, err
	}
	tables, err := SQLTables(conn)
	if err != nil {
		return existing, err
	}
	for t := range tables {
		existing[strings.ToLower(t)] = true
	}
	if requireEmpty {
		for t := range tables {
			check, _, e := conn.Prepare("SELECT 1 FROM " + Quote(t) + " LIMIT 1")
			if e != nil {
				return existing, e
			}
			defer check.Close()
			hasRows := check.Step()
			e = check.Err()
			check.Close()
			if e != nil {
				return existing, e
			}
			if hasRows {
				return existing, fmt.Errorf("database must be empty before own applies")
			}
		}
	}
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return existing, err
	}
	defer conn.Exec("ROLLBACK")
	if clearIDs {
		for t := range tables {
			cols, e := tableColumns(conn, t, rulesSave)
			if e != nil {
				return existing, e
			}
			column := "visitor_id"
			if rulesSave {
				column = schemaColumn(cols, column)
			}
			if _, found := cols[column]; found {
				if e = conn.Exec("UPDATE " + Quote(t) + " SET " + Quote(column) + " = NULL"); e != nil {
					return existing, e
				}
			}
		}
	}
	// Every table counts as new here, so one that needs visitor_id and has
	// none gets it.
	if err = ensureOwnTables(conn, map[string]bool{}, ownTables, rulesSave); err != nil {
		return existing, err
	}
	if err = ensureHoldTables(conn, holdTables, rulesSave); err != nil {
		return existing, err
	}
	sqliteStage(ctx, "before_commit")
	if err = conn.Exec("COMMIT"); err != nil {
		return existing, err
	}
	committed(existing)
	defer markChanged()
	sqliteStage(ctx, "after_commit")
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		checkpoint(err)
	}
	return existing, nil
}
