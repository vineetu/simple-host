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
	"path/filepath"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
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

// TableColumns provides the existing runtime storage helper.
func TableColumns(conn *sqlite3.Conn, table string) (map[string]string, error) {
	tables, err := SQLTables(conn)
	if err != nil {
		return nil, err
	}
	if !tables[table] {
		return nil, fmt.Errorf("unknown table")
	}
	stmt, _, err := conn.Prepare("PRAGMA table_info(" + Quote(table) + ")")
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
// EnsureOwnTables provides the existing runtime storage helper.
func EnsureOwnTables(conn *sqlite3.Conn, before map[string]bool, need func(string) bool) error {
	tables, err := SQLTables(conn)
	if err != nil {
		return err
	}
	for table := range tables {
		if need != nil && !need(table) {
			continue
		}
		cols, e := TableColumns(conn, table)
		if e != nil {
			return e
		}
		if _, ok := cols["visitor_id"]; !ok && !before[table] {
			if e = conn.Exec("ALTER TABLE " + Quote(table) + " ADD COLUMN visitor_id TEXT"); e != nil {
				return e
			}
			cols["visitor_id"] = "TEXT"
		}
		if cols["visitor_id"] != "TEXT" {
			return fmt.Errorf("visitor_id TEXT required")
		}
		if e = conn.Exec("CREATE INDEX IF NOT EXISTS " + Quote("sh_visitor_"+table) + " ON " + Quote(table) + " (visitor_id)"); e != nil {
			return e
		}
		// IF NOT EXISTS must not accept a same-named index on another column.
		index, _, e := conn.Prepare("SELECT tbl_name FROM sqlite_schema WHERE type='index' AND name=?")
		if e != nil {
			return e
		}
		defer index.Close()
		_ = index.BindText(1, "sh_visitor_"+table)
		correct := index.Step() && index.ColumnText(0) == table
		index.Close()
		info, _, e := conn.Prepare("PRAGMA index_info(" + Quote("sh_visitor_"+table) + ")")
		if e != nil {
			return e
		}
		defer info.Close()
		correct = correct && info.Step() && info.ColumnText(2) == "visitor_id"
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
// someone with for (SIM-358): unlike visitor_id (EnsureOwnTables, run at
// preset-save time, on every table the preset's own/add rule needs), this
// runs under the write lock, within the same locked window as the insert
// it is for, so an existing records/personal table works on its very first
// for use without the owner having to resave its preset.
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
func EnsureWrittenByColumn(conn *sqlite3.Conn, table string) error {
	cols, err := TableColumns(conn, table)
	if err != nil {
		return err
	}
	if cols["written_by"] == "TEXT" {
		return nil
	}
	if _, ok := cols["written_by"]; ok {
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

// EnsureHoldTables gives each table that holds posts (need) the
// server-owned approved_at TEXT column: NULL while a post waits for the
// owner, a time once approved. Rows already in a table when the column is
// added were public before, so they count as approved.
// EnsureHoldTables provides the existing runtime storage helper.
func EnsureHoldTables(conn *sqlite3.Conn, need func(string) bool) error {
	tables, err := SQLTables(conn)
	if err != nil {
		return err
	}
	for table := range tables {
		if !need(table) {
			continue
		}
		cols, e := TableColumns(conn, table)
		if e != nil {
			return e
		}
		if _, ok := cols["approved_at"]; !ok {
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
		}
		if cols["approved_at"] != "TEXT" {
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

// StorageScalar provides the existing runtime storage helper.
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
// exist and be readable by this caller under the parent table's read value
// (own: the caller added it). It runs inside the write's transaction, so a
// refused reference leaves no change. Reading the row back also covers
// foreign-key values supplied by SQLite defaults.
func CheckReferences(conn *sqlite3.Conn, table, rowid string, id int64, access func(string) (string, bool, bool)) (int, error) {
	stmt, _, err := conn.Prepare("PRAGMA foreign_key_list(" + Quote(table) + ")")
	if err != nil {
		return 400, err
	}
	defer stmt.Close()
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
		parent, err := TableColumns(conn, ref.table)
		if errors.Is(err, sqlite3.NOMEM) {
			return 400, err
		}
		if err != nil {
			return 404, fmt.Errorf("referenced row not found")
		}
		filter, ok, held := access(ref.table)
		if !ok || filter != "" && parent["visitor_id"] != "TEXT" {
			return 404, fmt.Errorf("referenced row not found")
		}
		if ref.to[0] == "" {
			info, _, err := conn.Prepare("PRAGMA table_info(" + Quote(ref.table) + ")")
			if err != nil {
				return 400, err
			}
			defer info.Close()
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
			joins = append(joins, "p."+Quote(ref.to[i])+" = c."+Quote(from))
			nulls = append(nulls, "c."+Quote(from)+" IS NULL")
		}
		if held {
			if parent["approved_at"] != "TEXT" {
				return 404, fmt.Errorf("referenced row not found")
			}
			joins = append(joins, "p.approved_at IS NOT NULL")
		}
		writer := "''"
		if parent["visitor_id"] != "" {
			writer = "COALESCE(p.visitor_id,'')"
		}
		// Optional links are all NULL. Partial composite NULLs are refused.
		query := "SELECT (" + strings.Join(nulls, " OR ") + "), (" + strings.Join(nulls, " AND ") + "), p." + Quote(ref.to[0]) + " IS NOT NULL, " + writer + " FROM " + Quote(table) +
			" c LEFT JOIN " + Quote(ref.table) + " p ON " + strings.Join(joins, " AND ") + " WHERE c." + rowid + " = ?"
		check, _, err := conn.Prepare(query)
		if err != nil {
			return 404, err
		}
		defer check.Close()
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

var (
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

// OpenTableConn opens the database for one table route: read-only
// for reads, and for writes with the site's allowance as the page cap.
func OpenTableConn(parent context.Context, dbPath string, write bool, timeout time.Duration, maxMemory int64, budget func() (int64, error)) (*sqlite3.Conn, func(), error) {
	uri := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro"}).String()
	var remaining int64
	var currentDBBytes int64
	if fi, err := os.Stat(dbPath); os.IsNotExist(err) {
		if write {
			// No tables yet, so no row to add or change.
			return nil, nil, ErrUnknownTable
		}
		uri = ":memory:"
	} else if err != nil {
		return nil, nil, ErrTableInternal
	} else {
		currentDBBytes = fi.Size()
	}
	if write {
		var err error
		if remaining, err = budget(); err != nil {
			return nil, nil, err
		}
		uri = dbPath
		if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
			return nil, nil, ErrTableInternal
		}
	}
	size := currentDBBytes + remaining // budget excludes this DB and cannot exceed the site limit
	if !write {
		var err error
		size, err = sqliteReadSize(dbPath)
		if err != nil {
			return nil, nil, ErrTableInternal
		}
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	ctx, cache := sqliteMemoryContext(ctx, size, maxMemory)
	opened := false
	defer func() {
		if !opened {
			cancel()
		}
	}()
	conn, err := sqlite3.OpenContext(ctx, uri)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	done := func() { defer cancel(); defer conn.Close(); sqliteStage(ctx, "close") }
	// Setup also owns the connection until the successful return. A panic in a
	// pragma must not strand a connection before the caller can defer its close.
	ready := false
	defer func() {
		if !ready {
			closeTableConn(done)
		}
	}()
	sqliteStage(ctx, "after_open")
	conn.SetInterrupt(ctx)
	fail := func(err error) (*sqlite3.Conn, func(), error) {
		if errors.Is(err, sqlite3.NOMEM) {
			return nil, nil, err
		}
		return nil, nil, ErrTableInternal
	}
	if err := configureSQLiteMemory(conn, cache); err != nil {
		return fail(err)
	}
	if err := conn.Exec("PRAGMA foreign_keys=ON"); err != nil {
		return fail(err)
	}
	if _, err = conn.Config(sqlite3.DBCONFIG_DEFENSIVE, true); err != nil {
		return fail(err)
	}
	_ = conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
	_ = conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
	if write {
		if err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return fail(err)
		}
		page, _, e := conn.Prepare("PRAGMA page_size")
		if e != nil || page == nil {
			return fail(e)
		}
		defer page.Close()
		sqliteStage(ctx, "after_prepare")
		if !page.Step() {
			e = page.Err()
			if page != nil {
				page.Close()
			}
			return fail(e)
		}
		sqliteStage(ctx, "after_step")
		pageSize := page.ColumnInt64(0)
		page.Close()
		if pageSize <= 0 {
			return fail(err)
		}
		maxPages := (remaining + currentDBBytes) / pageSize
		if maxPages < 1 {
			return nil, nil, ErrTableFull
		}
		if err := conn.Exec(fmt.Sprintf("PRAGMA max_page_count=%d", maxPages)); err != nil {
			return fail(err)
		}
	}
	ready = true
	opened = true
	return conn, done, nil
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
