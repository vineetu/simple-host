package storage

import (
	"context"
	"errors"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// SetLegacySQLiteLimit applies and verifies the page cap used by legacy SQL writes.
func SetLegacySQLiteLimit(conn *sqlite3.Conn, available int64) error {
	maxPages, _, err := capPages(context.Background(), conn, available)
	if err != nil {
		return err
	}
	capStmt, _, err := conn.Prepare("PRAGMA max_page_count")
	if err != nil {
		return err
	}
	defer capStmt.Close()
	if !capStmt.Step() {
		if err = capStmt.Err(); err == nil {
			err = errors.New("page cap unavailable")
		}
		return err
	}
	actualPages := capStmt.ColumnInt64(0)
	capStmt.Close()
	if actualPages > maxPages {
		return ErrTableFull
	}
	return nil
}
