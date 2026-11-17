package storage

import (
	"errors"
	"fmt"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// SetLegacySQLiteLimit applies and verifies the page cap used by legacy SQL writes.
func SetLegacySQLiteLimit(conn *sqlite3.Conn, available int64) error {
	page, _, err := conn.Prepare("PRAGMA page_size")
	if err != nil {
		return err
	}
	defer page.Close()
	if !page.Step() {
		if page != nil {
			page.Close()
		}
		if err := page.Err(); err != nil {
			return err
		}
		return errors.New("page size unavailable")
	}
	size := page.ColumnInt64(0)
	page.Close()
	if size <= 0 {
		return errors.New("invalid page size")
	}
	maxPages := available / size
	if maxPages < 1 {
		return ErrTableFull
	}
	if err := conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
		return err
	}
	capStmt, _, err := conn.Prepare("PRAGMA max_page_count")
	if err != nil {
		return err
	}
	defer capStmt.Close()
	if !capStmt.Step() {
		if capStmt != nil {
			capStmt.Close()
		}
		if err := capStmt.Err(); err != nil {
			return err
		}
		return errors.New("page cap unavailable")
	}
	actualPages := capStmt.ColumnInt64(0)
	capStmt.Close()
	if actualPages > maxPages {
		return ErrTableFull
	}
	return nil
}
