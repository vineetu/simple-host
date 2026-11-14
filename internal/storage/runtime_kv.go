package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// RuntimeKVPath is separate from owner-queryable SQLite databases.
func RuntimeKVPath(root, resource string) string {
	return filepath.Join(root, "kv", resource+".sqlite")
}

const runtimeKVSchema = `CREATE TABLE kv (
 key TEXT PRIMARY KEY NOT NULL,
 value TEXT NOT NULL,
 visitor_id TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
) WITHOUT ROWID;
CREATE TABLE kv_meta (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 entries INTEGER NOT NULL,
 logical_bytes INTEGER NOT NULL
);
INSERT INTO kv_meta VALUES (1,0,0);`

// KVEntry carries the normalized JSON text and the original author.
type KVEntry struct {
	Key       string
	Value     json.RawMessage
	VisitorID string
}

// KVMeta is maintained in the same transaction as the entry mutation.
type KVMeta struct{ Entries, LogicalBytes int64 }

// OpenRuntimeKV opens only a declared namespace. Callers own admission and writes
// own the site lock. Unlike row databases, this file has no physical page cap.
func OpenRuntimeKV(ctx context.Context, path string, write, create bool) (*sqlite3.Conn, func(), error) {
	if !create {
		if _, err := os.Stat(path); err != nil {
			return nil, nil, err
		}
	}
	mode := "ro"
	if write {
		mode = "rw"
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, nil, err
		}
		mode = "rwc"
	}
	size, err := sqliteReadSize(path)
	if err != nil {
		return nil, nil, err
	}
	ctx, cache := sqliteMemoryContext(ctx, size, 0)
	conn, err := sqlite3.OpenContext(ctx, (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=" + mode}).String())
	if err != nil {
		return nil, nil, err
	}
	done := func() { conn.Close() }
	ready := false
	defer func() {
		if !ready {
			closeTableConn(done)
		}
	}()
	conn.SetInterrupt(ctx)
	// Set before the first statement: a connection that is closing the WAL
	// holds the file exclusively for a moment, and the pragmas below would
	// fail at once with "database is locked". The wait ends early when ctx
	// is done.
	if err = conn.BusyTimeout(2 * time.Second); err != nil {
		return nil, nil, err
	}
	if err = configureSQLiteMemory(conn, cache); err != nil {
		return nil, nil, err
	}
	if write {
		if err = conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return nil, nil, err
		}
	}
	if create {
		// Creation happens before the Postgres marker is inserted. A previous failed
		// save can leave an empty file; initialize it afresh for the new namespace.
		if err = conn.Exec("BEGIN; DROP TABLE IF EXISTS kv; DROP TABLE IF EXISTS kv_meta;" + runtimeKVSchema + "COMMIT;"); err != nil {
			return nil, nil, err
		}
		if err = conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			return nil, nil, err
		}
	}
	ready = true
	return conn, done, nil
}

func kvStatement(conn *sqlite3.Conn, query string, args ...string) (*sqlite3.Stmt, error) {
	stmt, _, err := conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	for i, v := range args {
		if err = stmt.BindText(i+1, v); err != nil {
			stmt.Close()
			return nil, err
		}
	}
	return stmt, nil
}

func RuntimeKVGet(conn *sqlite3.Conn, key, filter string) (KVEntry, error) {
	stmt, err := kvStatement(conn, `SELECT key,value,visitor_id FROM kv WHERE key=? AND (?='' OR visitor_id=?)`, key, filter, filter)
	if err != nil {
		return KVEntry{}, err
	}
	defer stmt.Close()
	if !stmt.Step() {
		if err = stmt.Err(); err == nil {
			err = sql.ErrNoRows
		}
		return KVEntry{}, err
	}
	return KVEntry{stmt.ColumnText(0), json.RawMessage(stmt.ColumnText(1)), stmt.ColumnText(2)}, nil
}

func RuntimeKVList(conn *sqlite3.Conn, prefix, after string, limit int, filter string) ([]KVEntry, error) {
	stmt, err := kvStatement(conn, `SELECT key,value,visitor_id FROM kv WHERE instr(key,?)=1 AND key>? AND (?='' OR visitor_id=?) ORDER BY key COLLATE BINARY LIMIT ?`, prefix, after, filter, filter)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	if err = stmt.BindInt(5, limit); err != nil {
		return nil, err
	}
	entries := []KVEntry{}
	for stmt.Step() {
		entries = append(entries, KVEntry{stmt.ColumnText(0), json.RawMessage(stmt.ColumnText(1)), stmt.ColumnText(2)})
	}
	return entries, stmt.Err()
}

func RuntimeKVMeta(conn *sqlite3.Conn) (KVMeta, error) {
	stmt, _, err := conn.Prepare(`SELECT entries,logical_bytes FROM kv_meta WHERE id=1`)
	if err != nil {
		return KVMeta{}, err
	}
	defer stmt.Close()
	if !stmt.Step() {
		if err = stmt.Err(); err == nil {
			err = sql.ErrNoRows
		}
		return KVMeta{}, err
	}
	return KVMeta{stmt.ColumnInt64(0), stmt.ColumnInt64(1)}, nil
}

// RuntimeKVPut and RuntimeKVDelete run inside the caller's transaction.
func RuntimeKVPut(conn *sqlite3.Conn, key, value, visitor string, exists bool, oldBytes int64) error {
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
	query := `INSERT INTO kv(key,value,visitor_id,created_at,updated_at) VALUES(?,?,?,?,?)`
	args := []string{key, value, visitor, stamp, stamp}
	if exists {
		query = `UPDATE kv SET value=?,updated_at=? WHERE key=?`
		args = []string{value, stamp, key}
	}
	stmt, err := kvStatement(conn, query, args...)
	if err != nil {
		return err
	}
	err = stmt.Exec()
	stmt.Close()
	if err != nil {
		return err
	}
	entries := int64(1)
	if exists {
		entries = 0
	}
	return runtimeKVAdjust(conn, entries, int64(len(key)+len(value))-oldBytes)
}

func RuntimeKVDelete(conn *sqlite3.Conn, key string, oldBytes int64) error {
	stmt, err := kvStatement(conn, `DELETE FROM kv WHERE key=?`, key)
	if err != nil {
		return err
	}
	err = stmt.Exec()
	stmt.Close()
	if err != nil {
		return err
	}
	return runtimeKVAdjust(conn, -1, -oldBytes)
}

// RuntimeKVClearWriters makes every entry the owner's, inside the caller's
// transaction. Values and kv_meta do not change.
func RuntimeKVClearWriters(conn *sqlite3.Conn) error {
	return conn.Exec(`UPDATE kv SET visitor_id='' WHERE visitor_id<>''`)
}

func runtimeKVAdjust(conn *sqlite3.Conn, entries, bytes int64) error {
	stmt, _, err := conn.Prepare(`UPDATE kv_meta SET entries=entries+?,logical_bytes=logical_bytes+? WHERE id=1`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	if err = stmt.BindInt64(1, entries); err != nil {
		return err
	}
	if err = stmt.BindInt64(2, bytes); err != nil {
		return err
	}
	return stmt.Exec()
}

// RuntimeKVExport reads all entries, including historical keys, in byte order.
func RuntimeKVExport(conn *sqlite3.Conn) ([]KVEntry, error) {
	stmt, _, err := conn.Prepare(`SELECT key,value,visitor_id FROM kv ORDER BY key COLLATE BINARY`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	entries := []KVEntry{}
	for stmt.Step() {
		entries = append(entries, KVEntry{stmt.ColumnText(0), json.RawMessage(stmt.ColumnText(1)), stmt.ColumnText(2)})
	}
	return entries, stmt.Err()
}

// RuntimeKVWriters returns authors without reading their saved values.
func RuntimeKVWriters(conn *sqlite3.Conn) ([]string, error) {
	stmt, _, err := conn.Prepare(`SELECT DISTINCT visitor_id FROM kv WHERE visitor_id <> ''`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()
	var ids []string
	for stmt.Step() {
		ids = append(ids, stmt.ColumnText(0))
	}
	return ids, stmt.Err()
}

// RuntimeKVStats reports entries and value text bytes (keys are not included).
// Both reads share one snapshot; the entry count is the maintained kv_meta count.
func RuntimeKVStats(conn *sqlite3.Conn) (entries, bytes int64, err error) {
	stmt, _, err := conn.Prepare(`SELECT entries, (SELECT coalesce(sum(length(CAST(value AS BLOB))),0) FROM kv) FROM kv_meta WHERE id=1`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()
	if !stmt.Step() {
		if err = stmt.Err(); err == nil {
			err = sql.ErrNoRows
		}
		return 0, 0, err
	}
	return stmt.ColumnInt64(0), stmt.ColumnInt64(1), stmt.Err()
}
