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
)

func storageQuote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func storageSQLTables(conn *sqlite3.Conn) (map[string]bool, error) {
	stmt, _, err := conn.Prepare(`SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	tables := map[string]bool{}
	for stmt.Step() {
		tables[stmt.ColumnText(0)] = true
	}
	return tables, stmt.Err()
}
func storageTableColumns(conn *sqlite3.Conn, table string) (map[string]string, error) {
	tables, err := storageSQLTables(conn)
	if err != nil || !tables[table] {
		return nil, fmt.Errorf("unknown table")
	}
	stmt, _, err := conn.Prepare("PRAGMA table_info(" + storageQuote(table) + ")")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	cols := map[string]string{}
	for stmt.Step() {
		cols[stmt.ColumnText(1)] = strings.ToUpper(stmt.ColumnText(2))
	}
	return cols, stmt.Err()
}

// Owner schema changes are transactional. Add the identity column only to newly
// created tables, and refuse a change that removes it from an existing table.
// need picks the tables that must carry it (nil: every table).
func storageEnsureOwnTables(conn *sqlite3.Conn, before map[string]bool, need func(string) bool) error {
	tables, err := storageSQLTables(conn)
	if err != nil {
		return err
	}
	for table := range tables {
		if need != nil && !need(table) {
			continue
		}
		cols, e := storageTableColumns(conn, table)
		if e != nil {
			return e
		}
		if _, ok := cols["visitor_id"]; !ok && !before[table] {
			if e = conn.Exec("ALTER TABLE " + storageQuote(table) + " ADD COLUMN visitor_id TEXT"); e != nil {
				return e
			}
			cols["visitor_id"] = "TEXT"
		}
		if cols["visitor_id"] != "TEXT" {
			return fmt.Errorf("visitor_id TEXT required")
		}
		if e = conn.Exec("CREATE INDEX IF NOT EXISTS " + storageQuote("sh_visitor_"+table) + " ON " + storageQuote(table) + " (visitor_id)"); e != nil {
			return e
		}
		// IF NOT EXISTS must not accept a same-named index on another column.
		index, _, e := conn.Prepare("SELECT tbl_name FROM sqlite_schema WHERE type='index' AND name=?")
		if e != nil {
			return e
		}
		_ = index.BindText(1, "sh_visitor_"+table)
		correct := index.Step() && index.ColumnText(0) == table
		index.Close()
		info, _, e := conn.Prepare("PRAGMA index_info(" + storageQuote("sh_visitor_"+table) + ")")
		if e != nil {
			return e
		}
		correct = correct && info.Step() && info.ColumnText(2) == "visitor_id"
		info.Close()
		if !correct {
			return fmt.Errorf("visitor_id index required")
		}

	}
	return nil
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
	release, err := acquireStorageSQLite(ctx)
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
	maxPages := (storageSiteLimitBytes() - (h.storageBudgetUsed(usage) - fi.Size())) / size
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

// storageCheckReferences reads the table's declared foreign keys, then
// checks the added or edited row against each parent: the parent row must
// exist and be readable by this caller under the parent table's read value
// (own: the caller added it). It runs inside the write's transaction, so a
// refused reference leaves no change. Reading the row back also covers
// foreign-key values supplied by SQLite defaults.
func storageCheckReferences(conn *sqlite3.Conn, c storageCall, table, rowid string, id int64) (int, error) {
	stmt, _, err := conn.Prepare("PRAGMA foreign_key_list(" + storageQuote(table) + ")")
	if err != nil {
		return 400, err
	}
	type reference struct {
		table    string
		from, to []string
	}
	refs := map[int]*reference{}
	for stmt.Step() {
		n := stmt.ColumnInt(0)
		ref := refs[n]
		if ref == nil {
			ref = &reference{table: stmt.ColumnText(2)}
			refs[n] = ref
		}
		ref.from = append(ref.from, stmt.ColumnText(3))
		ref.to = append(ref.to, stmt.ColumnText(4))
	}
	err = stmt.Err()
	stmt.Close()
	if err != nil {
		return 400, err
	}
	for _, ref := range refs {
		parent, err := storageTableColumns(conn, ref.table)
		if err != nil {
			return 404, fmt.Errorf("referenced row not found")
		}
		filter, ok := c.storageMayRead(c.resource.tableMatrix(ref.table).Read)
		if !ok || filter != "" && parent["visitor_id"] != "TEXT" {
			return 404, fmt.Errorf("referenced row not found")
		}
		if ref.to[0] == "" {
			info, _, err := conn.Prepare("PRAGMA table_info(" + storageQuote(ref.table) + ")")
			if err != nil {
				return 400, err
			}
			primary := map[int]string{}
			for info.Step() {
				if pos := info.ColumnInt(5); pos > 0 {
					primary[pos] = info.ColumnText(1)
				}
			}
			err = info.Err()
			info.Close()
			if err != nil {
				return 400, err
			}
			if len(primary) != len(ref.to) {
				return 400, fmt.Errorf("foreign key must reference a primary key")
			}
			for i := range ref.to {
				ref.to[i] = primary[i+1]
			}
		}
		joins, nulls := []string{}, []string{}
		for i, from := range ref.from {
			joins = append(joins, "p."+storageQuote(ref.to[i])+" = c."+storageQuote(from))
			nulls = append(nulls, "c."+storageQuote(from)+" IS NULL")
		}
		writer := "''"
		if parent["visitor_id"] != "" {
			writer = "COALESCE(p.visitor_id,'')"
		}
		// Optional links are all NULL. Partial composite NULLs are refused.
		query := "SELECT (" + strings.Join(nulls, " OR ") + "), (" + strings.Join(nulls, " AND ") + "), p." + storageQuote(ref.to[0]) + " IS NOT NULL, " + writer + " FROM " + storageQuote(table) +
			" c LEFT JOIN " + storageQuote(ref.table) + " p ON " + strings.Join(joins, " AND ") + " WHERE c." + rowid + " = ?"
		check, _, err := conn.Prepare(query)
		if err != nil {
			return 404, err
		}
		if err = check.BindInt64(1, id); err != nil {
			check.Close()
			return 404, err
		}
		found := check.Step()
		anyNull, allNull, exists := found && check.ColumnBool(0), found && check.ColumnBool(1), found && check.ColumnBool(2)
		author := check.ColumnText(3)
		err = check.Err()
		check.Close()
		if err != nil {
			return 404, err
		}
		if allNull {
			continue
		}
		if !found || anyNull || !exists || filter != "" && author != filter {
			return 404, fmt.Errorf("referenced row not found")
		}
	}
	return 0, nil
}

// Test rowid accessibility instead of consulting connection-global insert state.
func storageRowID(conn *sqlite3.Conn, table string, cols map[string]string) (string, error) {
	for _, name := range []string{"_rowid_", "rowid", "oid"} {
		shadowed := false
		for col := range cols {
			if strings.EqualFold(col, name) {
				shadowed = true
			}
		}
		if shadowed {
			continue
		}
		stmt, _, err := conn.Prepare("SELECT " + name + " FROM " + storageQuote(table) + " LIMIT 0")
		if err != nil {
			return "", err
		}
		stmt.Close()
		return name, nil
	}
	return "", fmt.Errorf("accessible rowid required")
}
