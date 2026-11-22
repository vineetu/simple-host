package data

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"

	sqlite3 "github.com/ncruces/go-sqlite3"
	repository "github.com/vsriram/simple-host/host/data/repo"
	siterepo "github.com/vsriram/simple-host/host/sites/repo"
	runtime "github.com/vsriram/simple-host/host/storage"
)

func (s *Service) CreateKVSQLite(ctx context.Context, call Call, resource string) error {
	return s.Deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, a *Admission) error {
		_, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, resource), true, true)
		if err != nil {
			return err
		}
		defer CloseSQLite(done)
		return nil
	})
}

// clearKVWritersSQLite forgets every writer of a namespace, as
// ClearFileWriters does for a file bucket. save does the Postgres
// work and calls commit right before its own commit; any earlier failure rolls
// the file back with the Postgres transaction. The caller owns the site write
// lock.
func (s *Service) ClearKVWritersSQLite(ctx context.Context, call Call, resource string, save func(commit func() error) error) error {
	return s.Deps.SiteSlots.Admit(ctx, call.SiteID, func(ctx context.Context, _ *Admission) error {
		conn, done, err := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(call.RuntimeDir, resource), true, false)
		if err != nil {
			return err
		}
		defer CloseSQLite(done)
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

func KVFileError(err error) error {
	if errors.Is(err, sqlite3.BUSY) || errors.Is(err, sqlite3.LOCKED) {
		return SQLiteBusy()
	}
	if errors.Is(err, os.ErrNotExist) {
		return &Error{Code: "storage_failed", Message: "key-value namespace file is missing"}
	}
	return err
}

// SiteKVFileStats is KVFileStats for a site by id. Trashed sites keep their
// namespaces until permanent deletion, just as Postgres rows do, so a site in
// Recently deleted is measured in its trash directory.
func (s *Service) SiteKVFileStats(ctx context.Context, siteID string) (entries, bytes int64, err error) {
	site, err := siterepo.GetSiteByID(ctx, s.DB, siteID)
	if err != nil {
		return 0, 0, err
	}
	dir := s.Deps.Disk.SiteDir(site.UserID, site.Name)
	if site.Deleted {
		dir = s.Deps.Disk.TrashDir(site.UserID, site.ID)
	}
	return s.KVFileStats(ctx, siteID, filepath.Join(dir, "runtime"))
}

// KVFileStats is the admitted disk half of paper metrics. A failure discards
// this site's partial result; the collector can skip and count that site.
func (s *Service) KVFileStats(ctx context.Context, siteID, root string) (entries, bytes int64, err error) {
	names, err := repository.KVNamespaces(ctx, s.DB, siteID)
	if err != nil {
		return 0, 0, err
	}
	for _, name := range names {
		var n, b int64
		err = s.Deps.SiteSlots.Admit(ctx, siteID, func(ctx context.Context, _ *Admission) error {
			conn, done, e := runtime.OpenRuntimeKV(ctx, runtime.RuntimeKVPath(root, name), false, false)
			if e != nil {
				return e
			}
			defer CloseSQLite(done)
			n, b, e = runtime.RuntimeKVStats(conn)
			return e
		})
		if err != nil {
			return 0, 0, KVFileError(err)
		}
		entries += n
		bytes += b
	}
	return entries, bytes, nil
}

func KVFull() *Error {
	return &Error{Code: "site_full", Message: "KV and SQLite storage is full; remove unused data before retrying"}
}
