package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"unicode/utf8"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repository "github.com/vsriram/simple-host/internal/repository/storage"
	runtime "github.com/vsriram/simple-host/internal/storage"
)

func (s *Service) createKVSQLite(ctx context.Context, call Call, resource string) error {
	return s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *Admission) error {
		_, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, resource), true, true)
		if err != nil {
			return err
		}
		defer closeSQLite(done)
		return nil
	})
}

// clearKVWritersSQLite forgets every writer of a namespace kept on SQLite, as
// ClearResourceWriters does for one kept on Postgres. save does the Postgres
// work and calls commit right before its own commit; any earlier failure rolls
// the file back with the Postgres transaction. The caller owns the site write
// lock.
func (s *Service) clearKVWritersSQLite(ctx context.Context, call Call, resource string, save func(commit func() error) error) error {
	return s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, resource), true, false)
		if err != nil {
			return err
		}
		defer closeSQLite(done)
		if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
			return err
		}
		defer conn.Exec("ROLLBACK")
		if err = runtime.RuntimeKVClearWriters(conn); err != nil {
			return err
		}
		return save(func() error {
			if err := conn.Exec("COMMIT"); err != nil {
				return err
			}
			if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
				log.Print("storage: KV checkpoint failed after commit")
			}
			return nil
		})
	})
}

// withKV keeps the connection and all accounting reads inside one admission.
// The caller already owns the site write lock for a mutation.
func (s *Service) withKV(ctx context.Context, call Call, write bool, fn func(context.Context, *sqlite3.Conn, *Admission) error) error {
	if call.Resource.KVStore != "sqlite" {
		return fn(ctx, nil, nil)
	}
	err := s.deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *Admission) error {
		if !write && s.deps.SQLiteTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.deps.SQLiteTimeout)
			defer cancel()
		}
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, call.ResourceName), write, false)
		if err != nil {
			return err
		}
		defer closeSQLite(done)
		return fn(ctx, conn, a)
	})
	return kvFileError(err)
}

func (s *Service) kvWriter(ctx context.Context, conn *sqlite3.Conn, siteID, resource, key string) (string, error) {
	if conn == nil {
		return repository.KVWriter(ctx, s.db, siteID, resource, key)
	}
	// Postgres rejects these during the same author lookup, before a write.
	if !utf8.ValidString(key) {
		return "", errors.New("invalid UTF-8 key")
	}
	item, err := runtime.RuntimeKVGet(conn, key, "")
	return item.VisitorID, err
}

func (s *Service) putKVSQLite(ctx context.Context, conn *sqlite3.Conn, a *Admission, call Call, key string, value json.RawMessage, exists bool) error {
	usage, err := s.measureSiteStorageAdmitted(ctx, call.SiteID, call.RuntimeDir)
	if err != nil {
		return err
	}
	old, err := runtime.RuntimeKVGet(conn, key, "")
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var oldBytes int64
	if exists {
		oldBytes = int64(len(key) + len(old.Value))
	}
	normalized, err := repository.KVNormalize(ctx, s.db, value)
	if err != nil {
		return &Error{Code: "invalid_value", Message: "invalid JSON value"}
	}
	if s.BudgetUsed(usage)-oldBytes+int64(len(key)+len(normalized)) > SiteLimitBytes(s.deps.SiteLimit, s.deps.CombinedPool) {
		return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
	}
	if err = conn.Exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.Exec("ROLLBACK")
	if err = runtime.RuntimeKVPut(conn, key, normalized, StampID(call.Owner, call.VisitorID), exists, oldBytes); err != nil {
		return err
	}
	return s.commitKVSQLite(ctx, conn, a)
}

func (s *Service) commitKVSQLite(ctx context.Context, conn *sqlite3.Conn, a *Admission) error {
	if err := conn.Exec("COMMIT"); err != nil {
		return err
	}
	a.Committed()
	s.deps.Committed(ctx)
	if err := conn.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		log.Print("storage: KV checkpoint failed after commit")
	}
	return nil
}

func (s *Service) deleteKVSQLite(ctx context.Context, call Call, key, filter string) (n int64, writer string, err error) {
	ctx, unlock, err := s.writeLock(ctx, call.OwnerID+"/"+call.SiteName, s.deps.WriteLockWait, s.deps.WriteTimeout, s.deps.Recheck)
	if err != nil {
		return 0, "", err
	}
	defer unlock()
	err = s.withKV(ctx, call, true, func(ctx context.Context, conn *sqlite3.Conn, a *Admission) error {
		item, e := runtime.RuntimeKVGet(conn, key, filter)
		if errors.Is(e, sql.ErrNoRows) {
			if filter != "" {
				return &Error{Code: "key_not_found", Message: "key not found"}
			}
			s.deps.Committed(ctx)
			return nil
		}
		if e != nil {
			return e
		}
		if e = conn.Exec("BEGIN IMMEDIATE"); e != nil {
			return e
		}
		defer conn.Exec("ROLLBACK")
		if e = runtime.RuntimeKVDelete(conn, key, int64(len(key)+len(item.Value))); e != nil {
			return e
		}
		n, writer = 1, item.VisitorID
		return s.commitKVSQLite(ctx, conn, a)
	})
	return
}

func kvFileError(err error) error {
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return sqliteBusy()
	}
	if errors.Is(err, os.ErrNotExist) {
		return &Error{Code: "storage_failed", Message: "key-value namespace file is missing"}
	}
	return err
}

// KVFileStats is the admitted disk half of paper metrics. A failure discards
// this site's partial result; the collector can skip and count that site.
func (s *Service) KVFileStats(ctx context.Context, siteID, root string) (entries, bytes int64, err error) {
	names, err := repository.SQLiteKVNamespaces(ctx, s.db, siteID)
	if err != nil {
		return 0, 0, err
	}
	for _, name := range names {
		var n, b int64
		err = s.deps.SiteSlots.Admit(ctx, siteID, func(ctx context.Context, _ *Admission) error {
			conn, done, e := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(root, name), false, false)
			if e != nil {
				return e
			}
			defer closeSQLite(done)
			n, b, e = runtime.RuntimeKVStats(conn)
			return e
		})
		if err != nil {
			return 0, 0, kvFileError(err)
		}
		entries += n
		bytes += b
	}
	return entries, bytes, nil
}
