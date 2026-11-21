package storage

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	ruleengine "github.com/vsriram/simple-host/host/storage/rules"
)

// Quote provides the existing runtime storage helper.
func Quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// SQLTables provides the existing runtime storage helper.
func SQLTables(conn *sqlite3.Conn) (map[string]bool, error) {
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

// TableColumns returns a table's columns as table_info lists them (without
// generated and hidden columns; TableXInfo includes those).
func TableColumns(conn *sqlite3.Conn, table string) (map[string]string, error) {
	cols, _, err := tableColumnInfo(conn, table, false)
	return cols, err
}

// TableXInfo returns all declared columns, including generated and hidden ones,
// with their types and names in storage order.
func TableXInfo(conn *sqlite3.Conn, table string) (map[string]string, []string, error) {
	return tableColumnInfo(conn, table, true)
}

func tableColumnInfo(conn *sqlite3.Conn, table string, complete bool) (map[string]string, []string, error) {
	tables, err := SQLTables(conn)
	if err != nil {
		return nil, nil, err
	}
	if !tables[table] {
		return nil, nil, fmt.Errorf("unknown table")
	}
	pragma := "table_info"
	if complete {
		pragma = "table_xinfo"
	}
	stmt, _, err := conn.Prepare("PRAGMA " + pragma + "(" + Quote(table) + ")")
	if err != nil {
		return nil, nil, err
	}
	defer stmt.Close()
	cols := map[string]string{}
	var order []string
	for stmt.Step() {
		name := stmt.ColumnText(1)
		cols[name] = strings.ToUpper(stmt.ColumnText(2))
		order = append(order, name)
	}
	return cols, order, stmt.Err()
}

// schemaColumn resolves a SQLite column while keeping its declared spelling.
func schemaColumn(cols map[string]string, name string) string {
	for column := range cols {
		if strings.EqualFold(column, name) {
			return column
		}
	}
	return ""
}

// Owner schema changes are transactional. Add the identity column only to newly
// created tables, and refuse a change that removes it from an existing table.
// need picks the tables that must carry it (nil: every table).
func ensureOwnTables(conn *sqlite3.Conn, before map[string]bool, need func(string) bool) error {
	tables, err := SQLTables(conn)
	if err != nil {
		return err
	}
	for table := range tables {
		if need != nil && !need(table) {
			continue
		}
		cols, _, e := TableXInfo(conn, table)
		if e != nil {
			return e
		}
		column := schemaColumn(cols, "visitor_id")
		if _, found := cols[column]; !found && !before[table] {
			if e = conn.Exec("ALTER TABLE " + Quote(table) + " ADD COLUMN visitor_id TEXT"); e != nil {
				return e
			}
			cols["visitor_id"] = "TEXT"
			column = "visitor_id"
		}
		if cols[column] != "TEXT" {
			return fmt.Errorf("visitor_id TEXT required")
		}
		if e = conn.Exec("CREATE INDEX IF NOT EXISTS " + Quote("sh_visitor_"+table) + " ON " + Quote(table) + " (" + Quote(column) + ")"); e != nil {
			return e
		}
		// IF NOT EXISTS must not accept a same-named index on another column.
		index, _, e := conn.Prepare("SELECT tbl_name FROM sqlite_schema WHERE type='index' AND name=? COLLATE NOCASE")
		if e != nil {
			return e
		}
		defer index.Close()
		_ = index.BindText(1, "sh_visitor_"+table)
		correct := index.Step() && strings.EqualFold(index.ColumnText(0), table)
		index.Close()
		info, _, e := conn.Prepare("PRAGMA index_info(" + Quote("sh_visitor_"+table) + ")")
		if e != nil {
			return e
		}
		defer info.Close()
		correct = correct && info.Step() && strings.EqualFold(info.ColumnText(2), column)
		info.Close()
		if !correct {
			return fmt.Errorf("visitor_id index required")
		}

	}
	return nil
}

// ErrWrittenBySchemaLocked is returned by EnsureWrittenByColumn when a
// trigger or view's stored SQL appears to depend on the table, so altering
// it could silently change what that trigger or view does (a SELECT * or
// an INSERT ... SELECT * sees one more column than before).
var ErrWrittenBySchemaLocked = fmt.Errorf("a trigger or view on this table may depend on its exact columns")

// EnsureWrittenByColumn adds the server-owned written_by TEXT NOT NULL
// DEFAULT ” column to table, lazily, the first time an owner add names
// someone with for: unlike visitor_id (ensureOwnTables, run at
// rules-save time, on every table the rules track an author on), this
// runs under the write lock, within the same locked window as the insert
// it is for, so an existing table works on its very first for use without
// the owner having to resave its rules.
//
// ” (the default, including every row before this column existed) means
// the visitor in visitor_id wrote the row themselves; "owner" means the
// owner added it on their behalf. The caller (ResolveOwnerFor's gate) only
// ever calls this for a table whose read is own, so the column only ever
// exists on an own-read table.
//
// Refuses (ErrWrittenBySchemaLocked) rather than risk a working trigger or
// view silently changing shape: a conservative, table-name text match
// against every stored trigger and view, not a true dependency analysis.
// A false positive only means the owner removes or recreates that trigger
// or view before using for here; it never applies the ALTER.
// SQLite column names are case-insensitive, and so is written_by.
func EnsureWrittenByColumn(conn *sqlite3.Conn, table string) error {
	cols, err := TableColumns(conn, table)
	if err != nil {
		return err
	}
	column := schemaColumn(cols, "written_by")
	if cols[column] == "TEXT" {
		return nil
	}
	if _, ok := cols[column]; ok {
		return fmt.Errorf("written_by TEXT required")
	}
	dependent, err := tableHasSchemaDependents(conn, table)
	if err != nil {
		return err
	}
	if dependent {
		return ErrWrittenBySchemaLocked
	}
	return conn.Exec("ALTER TABLE " + Quote(table) + " ADD COLUMN written_by TEXT NOT NULL DEFAULT ''")
}

// tableHasSchemaDependents reports whether any trigger or view's stored SQL
// text mentions table, a conservative stand-in for "depends on its current
// columns" cheap enough to run on every for write.
func tableHasSchemaDependents(conn *sqlite3.Conn, table string) (bool, error) {
	stmt, _, err := conn.Prepare(`SELECT sql FROM sqlite_schema WHERE type IN ('trigger','view') AND sql IS NOT NULL`)
	if err != nil {
		return false, err
	}
	defer stmt.Close()
	needle := strings.ToLower(table)
	for stmt.Step() {
		if strings.Contains(strings.ToLower(stmt.ColumnText(0)), needle) {
			return true, nil
		}
	}
	return false, stmt.Err()
}

// ensureHoldTables gives each table that holds posts (need) the
// server-owned approved_at TEXT column: NULL while a post waits for the
// owner, a time once approved. Rows already in a table when the column is
// added were public before, so they count as approved.
func ensureHoldTables(conn *sqlite3.Conn, need func(string) bool, before map[string]bool) error {
	tables, err := SQLTables(conn)
	if err != nil {
		return err
	}
	for table := range tables {
		if !need(table) {
			continue
		}
		cols, _, e := TableXInfo(conn, table)
		if e != nil {
			return e
		}
		column := schemaColumn(cols, "approved_at")
		if _, found := cols[column]; !found {
			if before[table] {
				return fmt.Errorf("approved_at TEXT required")
			}
			if e = conn.Exec("ALTER TABLE " + Quote(table) + " ADD COLUMN approved_at TEXT"); e != nil {
				return e
			}
			stmt, _, e := conn.Prepare("UPDATE " + Quote(table) + " SET approved_at = ?")
			if e != nil {
				return e
			}
			defer stmt.Close()
			_ = stmt.BindText(1, time.Now().UTC().Format(time.RFC3339Nano))
			e = stmt.Exec()
			stmt.Close()
			if e != nil {
				return e
			}
			cols["approved_at"] = "TEXT"
			column = "approved_at"
		}
		if cols[column] != "TEXT" {
			return fmt.Errorf("approved_at TEXT required")
		}
	}
	return nil
}

// BindStorageParam provides the existing runtime storage helper.
func BindStorageParam(stmt *sqlite3.Stmt, i int, raw json.RawMessage) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	switch x := v.(type) {
	case nil:
		return stmt.BindNull(i)
	case string:
		return stmt.BindText(i, x)
	case bool:
		return stmt.BindBool(i, x)
	case json.Number:
		if n, e := x.Int64(); e == nil {
			return stmt.BindInt64(i, n)
		}
		if f, e := x.Float64(); e == nil {
			return stmt.BindFloat(i, f)
		}
		return fmt.Errorf("invalid number")
	default:
		return fmt.Errorf("invalid type")
	}
}

// StorageColumn provides the existing runtime storage helper.
func StorageColumn(stmt *sqlite3.Stmt, i int) any {
	switch stmt.ColumnType(i) {
	case sqlite3.NULL:
		return nil
	case sqlite3.INTEGER:
		return stmt.ColumnInt64(i)
	case sqlite3.FLOAT:
		return stmt.ColumnFloat(i)
	case sqlite3.BLOB:
		return map[string]string{"base64": base64.StdEncoding.EncodeToString(stmt.ColumnBlob(i, nil))}
	default:
		return stmt.ColumnText(i)
	}
}

// StorageScalar reports whether raw is one JSON scalar: null, a string, a
// boolean or a number.
func StorageScalar(raw json.RawMessage) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return false
	}
	switch value.(type) {
	case nil, string, bool, json.Number:
		return true
	}
	return false
}

// RowID finds an accessible SQLite rowid.
func RowID(conn *sqlite3.Conn, table string, cols map[string]string) (string, error) {
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
		stmt, _, err := conn.Prepare("SELECT " + name + " FROM " + Quote(table) + " LIMIT 0")
		if err != nil {
			return "", err
		}
		stmt.Close()
		return name, nil
	}
	return "", fmt.Errorf("accessible rowid required")
}

// CreateIfNotExists reports whether sql is a CREATE ... IF NOT EXISTS statement.
func CreateIfNotExists(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	return strings.HasPrefix(upper, "CREATE") && strings.Contains(upper, "IF NOT EXISTS")
}

// CheckReferences reads the table's declared foreign keys, then
// checks the added or edited row against each parent: the parent row must
// exist and satisfy the parent table's read predicate for this caller. It runs
// inside the write's transaction, so a refused reference leaves no change. Reading the row back also covers
// foreign-key values supplied by SQLite defaults.
func CheckReferences(conn *sqlite3.Conn, table, rowid string, id int64, access func(string) (ruleengine.Predicate, error)) (int, error) {
	keys, err := foreignKeys(conn, table)
	if err != nil {
		return 400, err
	}
	for _, key := range keys {
		predicate, err := access(key.parent)
		if err != nil {
			return 404, err
		}
		if key.to[0] == "" {
			if key.to, err = primaryKeyOrder(conn, key.parent); err != nil {
				return 400, err
			}
			if len(key.to) != len(key.from) {
				return 400, fmt.Errorf("foreign key must reference a primary key")
			}
		}
		if err = checkReference(conn, table, rowid, id, key, predicate); err != nil {
			return 404, err
		}
	}
	return 0, nil
}

// foreignKey is one declared foreign key: child columns from, parent columns
// to (all empty when it names the parent's primary key implicitly).
type foreignKey struct {
	parent   string
	from, to []string
}

// foreignKeys lists a table's foreign keys in declaration order.
func foreignKeys(conn *sqlite3.Conn, table string) ([]foreignKey, error) {
	stmt, _, err := conn.Prepare("PRAGMA foreign_key_list(" + Quote(table) + ")")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	var keys []foreignKey
	last := -1
	for stmt.Step() {
		if n := stmt.ColumnInt(0); n != last {
			keys = append(keys, foreignKey{parent: stmt.ColumnText(2)})
			last = n
		}
		key := &keys[len(keys)-1]
		key.from = append(key.from, stmt.ColumnText(3))
		key.to = append(key.to, stmt.ColumnText(4))
	}
	return keys, stmt.Err()
}

// primaryKeyOrder lists a table's primary key columns in key order.
func primaryKeyOrder(conn *sqlite3.Conn, table string) ([]string, error) {
	info, _, err := conn.Prepare("PRAGMA table_info(" + Quote(table) + ")")
	if err != nil {
		return nil, err
	}
	defer info.Close()
	byPosition := map[int]string{}
	for info.Step() {
		if pos := info.ColumnInt(5); pos > 0 {
			byPosition[pos] = info.ColumnText(1)
		}
	}
	if err = info.Err(); err != nil {
		return nil, err
	}
	columns := make([]string, len(byPosition))
	for pos, name := range byPosition {
		columns[pos-1] = name
	}
	return columns, nil
}

// checkReference accepts the row when its reference is entirely NULL or
// names a parent row the predicate lets this caller read.
func checkReference(conn *sqlite3.Conn, table, rowid string, id int64, key foreignKey, predicate ruleengine.Predicate) error {
	joins, nulls := make([]string, len(key.from)), make([]string, len(key.from))
	for i, from := range key.from {
		joins[i] = "p." + Quote(key.to[i]) + " = c." + Quote(from)
		nulls[i] = "c." + Quote(from) + " IS NULL"
	}
	// Keep the predicate inside the parent subquery so its unqualified
	// columns cannot resolve to the child. Optional links are all NULL;
	// partial composite NULLs are refused.
	query := "SELECT (" + strings.Join(nulls, " OR ") + "), (" + strings.Join(nulls, " AND ") + "), p." + Quote(key.to[0]) + " IS NOT NULL FROM " + Quote(table) +
		" c LEFT JOIN (SELECT * FROM " + Quote(key.parent) + " WHERE " + predicate.SQL + ") p ON " + strings.Join(joins, " AND ") + " WHERE c." + rowid + " = ?"
	check, _, err := conn.Prepare(query)
	if err != nil {
		return err
	}
	defer check.Close()
	if err = BindRuleArgs(check, 1, predicate.Args); err != nil {
		return err
	}
	if err = check.BindInt64(len(predicate.Args)+1, id); err != nil {
		return err
	}
	if !check.Step() {
		if err = check.Err(); err != nil {
			return err
		}
		return fmt.Errorf("referenced row not found")
	}
	if check.ColumnBool(1) {
		return nil
	}
	if check.ColumnBool(0) || !check.ColumnBool(2) {
		return fmt.Errorf("referenced row not found")
	}
	return nil
}

var (
	// ErrSQLiteBusy indicates that a slot could not be acquired in time.
	ErrSQLiteBusy    = errors.New("storage is busy")
	ErrUnknownTable  = errors.New("unknown table")
	ErrTableInternal = errors.New("table internal error")
	ErrTableFull     = errors.New("table full")
)

// sqliteMemoryCeiling is the largest WithMaxMemory value accepted by
// github.com/ncruces/go-sqlite3 v0.35.6 (wrap.go): 65,536 WebAssembly pages.
const sqliteMemoryCeiling int64 = 4096 << 20

func sqliteMemory(size int64) (cap, cache int64) {
	cache = min(max(size/4, 256<<10), 2<<20)
	temp := max(2*size, 1<<20)
	cap = min(((3<<20)+cache+temp+(1<<20)-1)/(1<<20)*(1<<20), sqliteMemoryCeiling)
	return cap, cache
}

// sqliteReadSize includes uncheckpointed pages; a missing file is an empty DB.
func sqliteReadSize(path string) (int64, error) {
	var size int64
	for _, suffix := range []string{"", "-wal"} {
		fi, err := os.Stat(path + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return 0, err
		}
		size += fi.Size()
	}
	return size, nil
}

type sqliteMemoryKey struct{}

// SQLiteMemoryLimit reports the allocation cap to a connection observation hook
// installed with sqlite3.AutoExtension. It is not a process-memory measurement.
func SQLiteMemoryLimit(ctx context.Context) int64 {
	cap, _ := ctx.Value(sqliteMemoryKey{}).(int64)
	return cap
}

func sqliteMemoryContext(ctx context.Context, size, ceiling int64) (context.Context, int64) {
	cap, cache := sqliteMemory(size)
	// A smaller explicit ceiling is used by the injected-OOM tests. Production
	// leaves it zero so the database size alone determines the cap.
	if ceiling > 0 {
		cap = min(cap, ceiling)
	}
	ctx = context.WithValue(ctx, sqliteMemoryKey{}, cap)
	return sqlite3.WithMaxMemory(ctx, cap), cache
}

func configureSQLiteMemory(conn *sqlite3.Conn, cache int64) error {
	return conn.Exec(fmt.Sprintf("PRAGMA cache_size=-%d; PRAGMA temp_store=MEMORY", cache/1024))
}

// openSQLite opens uri with a memory cap sized for a database of size bytes
// and returns the capped context and the page cache size for
// configureSQLiteMemory. It closes the connection itself if the open stage
// panics; once it returns a connection, the caller owns it.
func openSQLite(ctx context.Context, uri string, size, maxMemory int64) (context.Context, *sqlite3.Conn, int64, error) {
	ctx, cache := sqliteMemoryContext(ctx, size, maxMemory)
	conn, err := sqlite3.OpenContext(ctx, uri)
	if err != nil {
		return ctx, nil, 0, err
	}
	opened := false
	defer func() {
		if !opened {
			closeTableConn(func() { defer conn.Close(); sqliteStage(ctx, "close") })
		}
	}()
	sqliteStage(ctx, "after_open")
	conn.SetInterrupt(ctx)
	opened = true
	return ctx, conn, cache, nil
}

// restrictSQLite applies the settings of every owner-data connection:
// memory, foreign keys, defensive mode and size limits, and WAL for writes.
func restrictSQLite(conn *sqlite3.Conn, cache int64, write bool) error {
	if err := configureSQLiteMemory(conn, cache); err != nil {
		return err
	}
	if err := conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return err
	}
	if _, err := conn.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		return err
	}
	_ = conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
	_ = conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	if write {
		return conn.Exec("PRAGMA journal_mode=WAL")
	}
	return nil
}

// capPages limits the database to the whole pages that fit in available
// bytes and returns that page count and the page size. ErrTableFull means
// not even one page fits.
func capPages(ctx context.Context, conn *sqlite3.Conn, available int64) (pages, pageSize int64, err error) {
	page, _, err := conn.Prepare("PRAGMA page_size")
	if err != nil {
		return 0, 0, err
	}
	defer page.Close()
	sqliteStage(ctx, "after_prepare")
	if !page.Step() {
		if err = page.Err(); err == nil {
			err = errors.New("page size unavailable")
		}
		return 0, 0, err
	}
	sqliteStage(ctx, "after_step")
	pageSize = page.ColumnInt64(0)
	page.Close()
	if pageSize <= 0 {
		return 0, 0, errors.New("invalid page size")
	}
	if pages = available / pageSize; pages < 1 {
		return 0, 0, ErrTableFull
	}
	return pages, pageSize, conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", pages))
}

// OpenTableConn opens the database for one table route: read-only
// for reads, and for writes with the site's allowance as the page cap.
func OpenTableConn(parent context.Context, dbPath string, write bool, timeout time.Duration, maxMemory int64, budget func() (int64, error)) (*sqlite3.Conn, func(), error) {
	uri, size, err := tableConnTarget(dbPath, write, budget)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	ready := false
	defer func() {
		if !ready {
			cancel()
		}
	}()
	ctx, conn, cache, err := openSQLite(ctx, uri, size, maxMemory)
	if err != nil {
		return nil, nil, err
	}
	done := func() { defer cancel(); defer conn.Close(); sqliteStage(ctx, "close") }
	// Setup also owns the connection until the successful return. A panic in a
	// pragma must not strand a connection before the caller can defer its close.
	defer func() {
		if !ready {
			closeTableConn(done)
		}
	}()
	if err = restrictSQLite(conn, cache, write); err == nil && write {
		_, _, err = capPages(ctx, conn, size)
	}
	if err != nil && err != ErrTableFull && !errors.Is(err, sqlite3.NOMEM) {
		err = ErrTableInternal
	}
	if err != nil {
		return nil, nil, err
	}
	ready = true
	return conn, done, nil
}

// tableConnTarget resolves what a table route opens and the size its memory
// (and, for a write, page) cap is based on. A read of a missing database
// opens an empty in-memory one; a write to it has no table to change.
func tableConnTarget(dbPath string, write bool, budget func() (int64, error)) (string, int64, error) {
	fi, err := os.Stat(dbPath)
	missing := os.IsNotExist(err)
	switch {
	case err != nil && !missing:
		return "", 0, ErrTableInternal
	case write && missing:
		return "", 0, ErrUnknownTable
	case write:
		remaining, err := budget()
		if err != nil {
			return "", 0, err
		}
		// The budget excludes this database and cannot exceed the site limit.
		return dbPath, fi.Size() + remaining, nil
	}
	// Reads include uncheckpointed pages.
	size, err := sqliteReadSize(dbPath)
	if err != nil {
		return "", 0, ErrTableInternal
	}
	if missing {
		return ":memory:", size, nil
	}
	return (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}).String(), size, nil
}

// TableInfo returns the declared types and ordered names of a table's columns.
func TableInfo(conn *sqlite3.Conn, table string) (map[string]string, []string, error) {
	cols, err := TableColumns(conn, table)
	if err != nil {
		return nil, nil, err
	}
	stmt, _, err := conn.Prepare("SELECT * FROM " + Quote(table) + " LIMIT 0")
	if err != nil {
		return nil, nil, err
	}
	defer stmt.Close()
	order := make([]string, stmt.ColumnCount())
	for i := range order {
		order[i] = stmt.ColumnName(i)
	}
	return cols, order, nil
}

func closeTableConn(done func()) {
	defer func() {
		if recover() != nil {
			log.Print("storage: recovered SQLite setup close panic")
		}
	}()
	done()
}

// sqliteStageKey is used only by package tests to inject failures at ownership boundaries.
type sqliteStageKey struct{}

// WithSQLiteStageHook injects failures at SQLite ownership boundaries in service
// tests. Ordinary request contexts carry no hook.
func WithSQLiteStageHook(ctx context.Context, hook func(string)) context.Context {
	return context.WithValue(ctx, sqliteStageKey{}, hook)
}

func sqliteStage(ctx context.Context, stage string) {
	if hook, ok := ctx.Value(sqliteStageKey{}).(func(string)); ok {
		hook(stage)
	}
}

// EnsureRuleColumns applies the owner schema safeguards with SQLite's
// case-insensitive column names, as rule saves do.
func EnsureRuleColumns(conn *sqlite3.Conn, before map[string]bool, own, hold func(string) bool) error {
	if err := ensureOwnTables(conn, before, own); err != nil {
		return err
	}
	return ensureHoldTables(conn, hold, before)
}
