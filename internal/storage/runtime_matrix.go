package storage

import (
	"context"
	"fmt"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// PrepareMatrixDatabase applies the SQLite changes needed for a resource matrix.
func PrepareMatrixDatabase(ctx context.Context, path string, maxMemory, limit, used, fileSize int64, requireEmpty, clearIDs bool, ownTables, holdTables func(string) bool, checkpoint func(error), markChanged func()) (map[string]bool, error) {
	existing := map[string]bool{}
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, maxMemory), path)
	if err != nil {
		return existing, err
	}
	defer conn.Close()
	conn.SetInterrupt(ctx)
	if err = conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return existing, err
	}
	page, _, err := conn.Prepare("PRAGMA page_size")
	if err != nil {
		return existing, err
	}
	if !page.Step() {
		page.Close()
		return existing, fmt.Errorf("page size unavailable")
	}
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
			cols, e := TableColumns(conn, t)
			if e != nil {
				return existing, e
			}
			if _, has := cols["visitor_id"]; has {
				if e = conn.Exec("UPDATE " + Quote(t) + " SET visitor_id = NULL"); e != nil {
					return existing, e
				}
			}
		}
	}
	// Every table counts as new here, so one that needs visitor_id and has
	// none gets it.
	if err = EnsureOwnTables(conn, map[string]bool{}, ownTables); err != nil {
		return existing, err
	}
	if err = EnsureHoldTables(conn, holdTables); err != nil {
		return existing, err
	}
	if err = conn.Exec("COMMIT"); err != nil {
		return existing, err
	}
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		checkpoint(err)
	}
	markChanged()
	return existing, nil
}
