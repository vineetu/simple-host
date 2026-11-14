package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// KVSnapshotEntry is the complete on-disk record used by maintenance commands.
type KVSnapshotEntry struct{ Key, Value, Writer, Created, Updated string }

// WriteKVSnapshot builds a closed, single-file database without a WAL.
func WriteKVSnapshot(ctx context.Context, path string, entries []KVSnapshotEntry) (err error) {
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	conn, err := sqlite3.OpenContext(ctx, (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=rwc"}).String())
	if err != nil {
		return err
	}
	defer func() {
		if e := conn.Close(); err == nil {
			err = e
		}
	}()
	conn.SetInterrupt(ctx)
	if err = conn.Exec("PRAGMA journal_mode=DELETE; BEGIN;" + runtimeKVSchema); err != nil {
		return err
	}
	defer conn.Exec("ROLLBACK")
	var size int64
	for _, e := range entries {
		stmt, eerr := kvStatement(conn, `INSERT INTO kv VALUES(?,?,?,?,?)`, e.Key, e.Value, e.Writer, e.Created, e.Updated)
		if eerr != nil {
			return eerr
		}
		eerr = stmt.Exec()
		stmt.Close()
		if eerr != nil {
			return eerr
		}
		size += int64(len(e.Key) + len(e.Value))
	}
	if err = runtimeKVAdjust(conn, int64(len(entries)), size); err != nil {
		return err
	}
	return conn.Exec("COMMIT")
}

// ReadKVSnapshot checks health and metadata in the same read transaction as the
// records. It never changes journal mode or checkpoints a live writer's file.
func ReadKVSnapshot(ctx context.Context, path string) ([]KVSnapshotEntry, KVMeta, error) {
	conn, done, err := OpenRuntimeKV(ctx, path, false, false)
	if err != nil {
		return nil, KVMeta{}, err
	}
	defer done()
	if err = conn.Exec("BEGIN"); err != nil {
		return nil, KVMeta{}, err
	}
	defer conn.Exec("ROLLBACK")
	tables, _, err := conn.Prepare(`SELECT count(*),coalesce(sum(name IN ('kv','kv_meta')),0) FROM sqlite_schema WHERE type='table' AND substr(name,1,7)<>'sqlite_'`)
	if err != nil {
		return nil, KVMeta{}, err
	}
	validTables := tables.Step() && tables.ColumnInt64(0) == 2 && tables.ColumnInt64(1) == 2
	tables.Close()
	if !validTables {
		return nil, KVMeta{}, fmt.Errorf("expected kv and kv_meta tables")
	}
	check, _, err := conn.Prepare("PRAGMA integrity_check")
	if err != nil {
		return nil, KVMeta{}, err
	}
	ok := check.Step() && check.ColumnText(0) == "ok"
	more := check.Step()
	checkErr := check.Err()
	check.Close()
	if !ok || more || checkErr != nil {
		return nil, KVMeta{}, fmt.Errorf("integrity_check failed: %v", checkErr)
	}
	meta, err := RuntimeKVMeta(conn)
	if err != nil {
		return nil, KVMeta{}, err
	}
	stmt, _, err := conn.Prepare(`SELECT key,value,visitor_id,created_at,updated_at FROM kv`)
	if err != nil {
		return nil, KVMeta{}, err
	}
	defer stmt.Close()
	var out []KVSnapshotEntry
	var size int64
	var contentErr error
	for stmt.Step() {
		e := KVSnapshotEntry{stmt.ColumnText(0), stmt.ColumnText(1), stmt.ColumnText(2), stmt.ColumnText(3), stmt.ColumnText(4)}
		if !json.Valid([]byte(e.Value)) {
			contentErr = fmt.Errorf("invalid JSON value")
		}
		for _, stamp := range []string{e.Created, e.Updated} {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				contentErr = fmt.Errorf("invalid timestamp")
			}
		}
		out = append(out, e)
		size += int64(len(e.Key) + len(e.Value))
	}
	if err = stmt.Err(); err != nil {
		return nil, KVMeta{}, err
	}
	if meta.Entries != int64(len(out)) || meta.LogicalBytes != size {
		contentErr = errors.Join(contentErr, fmt.Errorf("kv_meta differs from recount"))
	}
	// There must be exactly the maintained singleton row, not merely id=1.
	count, _, err := conn.Prepare(`SELECT count(*) FROM kv_meta`)
	if err != nil {
		return nil, KVMeta{}, err
	}
	defer count.Close()
	if !count.Step() || count.ColumnInt64(0) != 1 {
		return nil, KVMeta{}, fmt.Errorf("invalid kv_meta row count")
	}
	return out, meta, errors.Join(contentErr, count.Err())
}
