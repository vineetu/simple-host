package storage

import (
	"context"
	"net/url"
	"os"
	"path/filepath"

	sqlite3 "github.com/ncruces/go-sqlite3"
)

// OpenLegacySQLite opens and configures a legacy SQL connection.
func OpenLegacySQLite(ctx context.Context, uri string, maxMemory, writeBytes int64, write bool) (*sqlite3.Conn, error) {
	size := writeBytes // database size plus the remaining pool, capped at the site limit
	if !write {
		size = 0
		var err error
		if uri != ":memory:" {
			u, parseErr := url.Parse(uri)
			if parseErr != nil {
				return nil, parseErr
			}
			size, err = sqliteReadSize(u.Path)
			if err != nil {
				return nil, err
			}
		}
	}
	ctx, cache := sqliteMemoryContext(ctx, size, maxMemory)
	conn, err := sqlite3.OpenContext(ctx, uri)
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			closeTableConn(func() { defer conn.Close(); sqliteStage(ctx, "close") })
		}
	}()
	sqliteStage(ctx, "after_open")
	conn.SetInterrupt(ctx)
	if err = configureSQLiteMemory(conn, cache); err != nil {
		return nil, err
	}
	if err = conn.Exec("PRAGMA foreign_keys=ON"); err == nil {
		_, err = conn.Config(sqlite3.DBCONFIG_DEFENSIVE, true)
	}
	if err == nil {
		_ = conn.Limit(sqlite3.LIMIT_SQL_LENGTH, 32768)
		_ = conn.Limit(sqlite3.LIMIT_LENGTH, 1<<20)
		if write {
			err = conn.Exec("PRAGMA journal_mode=WAL")
		}
	}
	if err != nil {
		return nil, err
	}
	ready = true
	return conn, nil
}

// LegacySQLiteSize reports the current database size and whether it is new.
func LegacySQLiteSize(path string) (int64, bool, error) {
	fi, err := os.Stat(path)
	if err == nil {
		return fi.Size(), false, nil
	}
	if os.IsNotExist(err) {
		return 0, true, nil
	}
	return 0, false, err
}

// LegacySQLiteQueryURI uses an empty in-memory database when none exists.
func LegacySQLiteQueryURI(path string) (string, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ":memory:", nil
	}
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}).String(), nil
}

// RemoveNewSQLite removes a newly created database and its sidecars.
func RemoveNewSQLite(path string) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
}

// EnsureSQLiteDirectory creates the directory for a SQLite database.
func EnsureSQLiteDirectory(path string) error {
	return os.MkdirAll(filepath.Dir(path), 0700)
}
