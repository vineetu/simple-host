package storage

import (
	"context"
	"fmt"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// PrepareRulesDatabase applies the SQLite changes a rules save needs: the
// author and approval columns, resolved case-insensitively.
func PrepareRulesDatabase(ctx context.Context, path string, maxMemory, limit, used, fileSize int64, requireEmpty, clearIDs bool, ownTables, holdTables func(string) bool, checkpoint func(error), markChanged func(), committed func(map[string]bool)) (map[string]bool, error) {
	existing := map[string]bool{}
	ctx, conn, cache, err := openSQLite(ctx, path, min(limit, fileSize+limit-used), maxMemory)
	if err != nil {
		return existing, err
	}
	defer closeTableConn(func() { defer conn.Close(); sqliteStage(ctx, "close") })
	if err = configureSQLiteMemory(conn, cache); err != nil {
		return existing, err
	}
	if err = conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return existing, err
	}
	pages, pageSize, err := capPages(ctx, conn, limit-(used-fileSize))
	if err == ErrTableFull || err == nil && fileSize > pages*pageSize {
		return existing, sqlite3.FULL
	}
	if err != nil {
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
		if err = requireEmptyTables(conn, tables); err != nil {
			return existing, err
		}
	}
	if err = addRuleColumns(ctx, conn, tables, clearIDs, ownTables, holdTables); err != nil {
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

// requireEmptyTables refuses a database that holds any row.
func requireEmptyTables(conn *sqlite3.Conn, tables map[string]bool) error {
	for t := range tables {
		check, _, err := conn.Prepare("SELECT 1 FROM " + Quote(t) + " LIMIT 1")
		if err != nil {
			return err
		}
		hasRows := check.Step()
		err = check.Err()
		check.Close()
		if err != nil {
			return err
		}
		if hasRows {
			return fmt.Errorf("database must be empty before own applies")
		}
	}
	return nil
}

// addRuleColumns commits, in one transaction, the optional clearing of
// visitor ids and the author and approval columns the rules need. Every table
// counts as new here, so one that needs visitor_id and has none gets it.
func addRuleColumns(ctx context.Context, conn *sqlite3.Conn, tables map[string]bool, clearIDs bool, own, hold func(string) bool) error {
	if err := conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.Exec("ROLLBACK")
	if clearIDs {
		if err := clearVisitorIDs(conn, tables); err != nil {
			return err
		}
	}
	if err := EnsureRuleColumns(conn, nil, own, hold); err != nil {
		return err
	}
	sqliteStage(ctx, "before_commit")
	return conn.Exec("COMMIT")
}

// clearVisitorIDs empties the visitor_id column of every table that has one.
func clearVisitorIDs(conn *sqlite3.Conn, tables map[string]bool) error {
	for t := range tables {
		cols, _, err := TableXInfo(conn, t)
		if err != nil {
			return err
		}
		if column := schemaColumn(cols, "visitor_id"); column != "" {
			if err = conn.Exec("UPDATE " + Quote(t) + " SET " + Quote(column) + " = NULL"); err != nil {
				return err
			}
		}
	}
	return nil
}
