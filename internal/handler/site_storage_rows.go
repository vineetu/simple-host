package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/internal/storage"
)

func storageQuote(s string) string                              { return storage.Quote(s) }
func storageSQLTables(c *sqlite3.Conn) (map[string]bool, error) { return storage.SQLTables(c) }
func storageTableColumns(c *sqlite3.Conn, t string) (map[string]string, error) {
	return storage.TableColumns(c, t)
}
func storageEnsureOwnTables(c *sqlite3.Conn, b map[string]bool, n func(string) bool) error {
	return storage.EnsureOwnTables(c, b, n)
}
func storageEnsureHoldTables(c *sqlite3.Conn, n func(string) bool) error {
	return storage.EnsureHoldTables(c, n)
}

// prepareStorageMatrixDatabase readies a database for c.resource's matrix:
// every table whose matrix uses own or lets visitors add gets the
// server-owned visitor_id TEXT column and its index. requireEmpty
// (switching to own from a database pages could write with SQL) refuses
// tables that already hold rows, whose visitor_id a page could have forged.
// clearIDs (the first preset on such a database) sets every existing
// visitor_id to NULL instead, so the rows become the owner's. It returns the
// database's tables, lower-cased.
func (h *SiteHandler) prepareStorageMatrixDatabase(ctx context.Context, c storageCall, requireEmpty, clearIDs bool) (map[string]bool, error) {
	existing := map[string]bool{}
	path := h.storageSQLPath(c)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return existing, nil
	} else if err != nil {
		return existing, err
	}
	usage, err := h.measureSiteStorage(ctx, c)
	if err != nil {
		return existing, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return existing, err
	}
	release, err := h.sqliteGateFor().Acquire(ctx)
	if err != nil {
		return existing, err
	}
	defer release()
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), path)
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
	maxPages := (storageSiteLimitBytes(h.ed.Storage().CombinedPool) - (h.storageBudgetUsed(usage) - fi.Size())) / size
	if maxPages < 1 || fi.Size() > maxPages*size {
		return existing, sqlite3.FULL
	}
	if err = conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
		return existing, err
	}
	tables, err := storageSQLTables(conn)
	if err != nil {
		return existing, err
	}
	for t := range tables {
		existing[strings.ToLower(t)] = true
	}
	if requireEmpty {
		for t := range tables {
			check, _, e := conn.Prepare("SELECT 1 FROM " + storageQuote(t) + " LIMIT 1")
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
			cols, e := storageTableColumns(conn, t)
			if e != nil {
				return existing, e
			}
			if _, has := cols["visitor_id"]; has {
				if e = conn.Exec("UPDATE " + storageQuote(t) + " SET visitor_id = NULL"); e != nil {
					return existing, e
				}
			}
		}
	}
	// Every table counts as new here, so one that needs visitor_id and has
	// none gets it.
	if err = storageEnsureOwnTables(conn, map[string]bool{}, c.resource.ownTables()); err != nil {
		return existing, err
	}
	if err = storageEnsureHoldTables(conn, c.resource.holds); err != nil {
		return existing, err
	}
	if err = conn.Exec("COMMIT"); err != nil {
		return existing, err
	}
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		log.Printf("storage: site %s %s: checkpoint after preset: %v", c.siteID, c.resourceName, err)
	}
	h.disk.MarkChanged()
	return existing, nil
}

type storageRowPage struct {
	limit      int
	order      string
	desc       bool
	orderIndex int
}
type storageRowCursor struct {
	Order string          `json:"order"`
	Desc  bool            `json:"desc"`
	Value json.RawMessage `json:"value"`
	ID    int64           `json:"id"`
}

func (p *storageRowPage) cursor(row []any, id int64) string {
	value := any(id)
	if p.orderIndex >= 0 {
		value = row[p.orderIndex]
	}
	raw, _ := json.Marshal(value)
	b, _ := json.Marshal(storageRowCursor{p.order, p.desc, raw, id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func storageCheckReferences(conn *sqlite3.Conn, c storageCall, table, rowid string, id int64) (int, error) {
	return storage.CheckReferences(conn, table, rowid, id, func(parent string) (string, bool, bool) {
		filter, ok := c.storageMayRead(c.resource.tableMatrix(parent).Read)
		return filter, ok, c.resource.holds(parent) && !c.ownerOnSite
	})
}

// Test rowid accessibility instead of consulting connection-global insert state.
func storageRowID(conn *sqlite3.Conn, table string, cols map[string]string) (string, error) {
	return storage.RowID(conn, table, cols)
}
