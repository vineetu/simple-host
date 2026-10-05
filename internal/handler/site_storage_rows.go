package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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
func storageEnsureOwnTables(conn *sqlite3.Conn, before map[string]bool) error {
	tables, err := storageSQLTables(conn)
	if err != nil {
		return err
	}
	for table := range tables {
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
func (h *SiteHandler) validateStorageOwnDatabase(ctx context.Context, c storageCall) error {
	path := h.storageSQLPath(c)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	usage, err := h.measureSiteStorage(ctx, c)
	if err != nil {
		return err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	conn, err := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), path)
	if err != nil {
		return err
	}
	defer conn.Close()
	page, _, err := conn.Prepare("PRAGMA page_size")
	if err != nil {
		return err
	}
	if !page.Step() {
		page.Close()
		return fmt.Errorf("page size unavailable")
	}
	size := page.ColumnInt64(0)
	page.Close()
	maxPages := (storageSiteLimitBytes() - (h.storageBudgetUsed(usage) - fi.Size())) / size
	if maxPages < 1 || fi.Size() > maxPages*size {
		return sqlite3.FULL
	}
	if err = conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
		return err
	}

	tables, err := storageSQLTables(conn)
	if err != nil {
		return err
	}
	for t := range tables {
		cols, e := storageTableColumns(conn, t)
		if e != nil {
			return e
		}
		if cols["visitor_id"] != "TEXT" {
			return fmt.Errorf("visitor_id TEXT required")
		}
	}
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.Exec("ROLLBACK")
	if err = storageEnsureOwnTables(conn, tables); err != nil {
		return err
	}
	if err = conn.Exec("COMMIT"); err != nil {
		return err
	}
	if _, _, err = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE); err != nil {
		return err
	}
	h.disk.MarkChanged()
	return nil
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

// Visitors supply values, table/column selections and a cursor, never SQL. Every
// identifier comes from the actual schema and every value is bound.
func storageRowsStatement(w http.ResponseWriter, r *http.Request, c storageCall, conn *sqlite3.Conn) (string, []json.RawMessage, *storageRowPage, error) {
	table := r.PathValue("table")
	cols, err := storageTableColumns(conn, table)
	if err != nil {
		return "", nil, nil, err
	}
	if c.resource.Read == "own" && cols["visitor_id"] != "TEXT" {
		return "", nil, nil, fmt.Errorf("visitor_id TEXT required")
	}
	if r.Method == http.MethodPost {
		var values map[string]json.RawMessage
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		if dec.Decode(&values) != nil || values == nil || len(values) > 100 || dec.Decode(new(any)) != io.EOF {
			return "", nil, nil, fmt.Errorf("one JSON object of column values required")
		}
		if !c.owner {
			if _, ok := values["visitor_id"]; ok {
				return "", nil, nil, fmt.Errorf("visitor_id is set by the server")
			}
			if _, ok := cols["visitor_id"]; ok {
				values["visitor_id"], _ = json.Marshal(c.visitorID)
			}
		}
		names := make([]string, 0, len(values))
		for name := range values {
			if _, ok := cols[name]; !ok {
				return "", nil, nil, fmt.Errorf("unknown column")
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return "INSERT INTO " + storageQuote(table) + " DEFAULT VALUES", nil, nil, nil
		}
		quoted := []string{}
		marks := []string{}
		params := []json.RawMessage{}
		for _, name := range names {
			quoted = append(quoted, storageQuote(name))
			marks = append(marks, "?")
			params = append(params, values[name])
		}
		return "INSERT INTO " + storageQuote(table) + " (" + strings.Join(quoted, ",") + ") VALUES (" + strings.Join(marks, ",") + ")", params, nil, nil
	}
	_, after, limit, ok := storagePage(r)
	if !ok {
		return "", nil, nil, fmt.Errorf("invalid limit")
	}
	rowid := ""
	for _, name := range []string{"_rowid_", "rowid", "oid"} {
		shadowed := false
		for col := range cols {
			if strings.EqualFold(col, name) {
				shadowed = true
			}
		}
		if !shadowed {
			rowid = name
			break
		}
	}
	if rowid == "" {
		return "", nil, nil, fmt.Errorf("table needs an accessible rowid")
	}
	order := r.URL.Query().Get("order")
	if order == "" {
		order = rowid
	}
	if _, ok := cols[order]; !ok && order != rowid {
		return "", nil, nil, fmt.Errorf("unknown order column")
	}
	desc := r.URL.Query().Get("desc")
	if desc != "" && desc != "0" && desc != "1" {
		return "", nil, nil, fmt.Errorf("invalid desc")
	}
	page := &storageRowPage{limit: limit, order: order, desc: desc == "1", orderIndex: -1}
	// Column order for SELECT * comes from table_info, rather than map order.
	stmt, _, e := conn.Prepare("SELECT * FROM " + storageQuote(table) + " LIMIT 0")
	if e != nil {
		return "", nil, nil, e
	}
	for i := 0; i < stmt.ColumnCount(); i++ {
		if stmt.ColumnName(i) == order {
			page.orderIndex = i
		}
	}
	stmt.Close()
	sql := "SELECT *, " + rowid + " FROM " + storageQuote(table)
	where := []string{}
	params := []json.RawMessage{}
	bind := func(v any) { b, _ := json.Marshal(v); params = append(params, b) }
	if c.ownReader() != "" {
		where = append(where, "visitor_id = ?")
		bind(c.ownReader())
	}
	direction, op := " ASC", ">"
	if page.desc {
		direction, op = " DESC", "<"
	}
	if after != "" {
		b, e := base64.RawURLEncoding.DecodeString(after)
		var cur storageRowCursor
		if e != nil || json.Unmarshal(b, &cur) != nil || cur.Order != order || cur.Desc != page.desc || cur.Value == nil {
			return "", nil, nil, fmt.Errorf("invalid after")
		}
		// SQLite sorts NULL first in ascending order; include them when descending.
		q := storageQuote(order)
		tie := rowid
		if string(cur.Value) == "null" {
			if page.desc {
				where = append(where, "("+q+" IS NULL AND "+tie+op+"?)")
			} else {
				where = append(where, "("+q+" IS NOT NULL OR ("+q+" IS NULL AND "+tie+op+"?))")
			}
			bind(cur.ID)
		} else {
			nulls := ""
			if page.desc {
				nulls = q + " IS NULL OR "
			}
			where = append(where, "("+nulls+q+op+"? OR ("+q+" = ? AND "+tie+op+"?))")
			params = append(params, cur.Value, cur.Value)
			bind(cur.ID)
		}
	}
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	sql += " ORDER BY " + storageQuote(order) + direction + ", " + rowid + direction + " LIMIT ?"
	bind(limit + 1)
	return sql, params, page, nil
}
