package ops

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appstorage "github.com/vsriram/simple-host/host/data"
	datarepo "github.com/vsriram/simple-host/host/data/repo"
	appsites "github.com/vsriram/simple-host/host/sites"
	sitesrepo "github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/storage"
)

// SiteArchive exports a site: its files from Sites and its saved data from
// Data, under the site's upload lock, with each SQLite read admitted by
// Data's per-site slots (site lock first, then the slot).
type SiteArchive struct {
	Database *sql.DB
	Disk     appsites.Disk
	Locks    *sync.Map
	Slots    *appstorage.SiteSlots
	// SQLiteMaxMemory caps a checkpoint's memory (0: the storage default).
	SQLiteMaxMemory int64
}

// WriteArchive streams one site (its files, data and rules, as Export
// yields them while it holds the site lock) into put under prefix.
func (a *SiteArchive) WriteArchive(ctx context.Context, put appsites.ArchivePut, prefix, siteID string, files appsites.FileVersion) error {
	for entry, err := range a.Export(ctx, ExportInput{Prefix: prefix, SiteID: siteID, Files: files}) {
		if err != nil {
			return err
		}
		if err := put(entry.Name, entry.ModTime, entry.Size, entry.Reader); err != nil {
			return err
		}
	}
	return nil
}

// exportResources is the site's saved-data resources as their rules JSON.
func (a *SiteArchive) exportResources(ctx context.Context, siteID string) ([]map[string]any, error) {
	list, err := datarepo.ListResources(ctx, a.Database, siteID)
	if err != nil {
		return nil, err
	}
	resources := []map[string]any{}
	for _, x := range list {
		resources = append(resources, appstorage.ResourceRulesJSON(x, nil))
	}
	return resources, nil
}

// admit runs fn holding one of the site's SQLite slots.
func (a *SiteArchive) admit(ctx context.Context, siteID string, fn func(context.Context) error) error {
	return a.Slots.Admit(ctx, siteID, func(ctx context.Context, _ *appstorage.Admission) error { return fn(ctx) })
}

// ExportInput selects logical deploy contents and a separately local runtime tree.
type ExportInput struct {
	Prefix, SiteID string
	Files          storage.FileVersion
}

// ExportEntry is one archive member. Reader remains open until iteration resumes.
type ExportEntry struct {
	Name    string
	ModTime time.Time
	Size    int64
	Reader  io.Reader
}

// Export yields saved data and files under the site lock, in archive order.
func (a *SiteArchive) Export(ctx context.Context, in ExportInput) iter.Seq2[ExportEntry, error] {
	return func(yield func(ExportEntry, error) bool) {
		stopped := errors.New("archive consumer stopped")
		err := a.writeSiteArchive(ctx, func(name string, modTime time.Time, size int64, r io.Reader) error {
			if !yield(ExportEntry{Name: name, ModTime: modTime, Size: size, Reader: r}, nil) {
				return stopped
			}
			return nil
		}, in.Prefix, in.SiteID, in.Files)
		if err != nil && err != stopped {
			yield(ExportEntry{}, err)
		}
	}
}

// writeSiteArchive writes one site under prefix: storage/ (the site's saved
// data) and files/ (the version in current, the site's live folder or, for a
// site in Recently deleted, the one waiting in trash).
func (a *SiteArchive) writeSiteArchive(ctx context.Context, put appsites.ArchivePut, prefix, siteID string, files storage.FileVersion) error {
	ownerID, siteName, err := sitesrepo.ExportOwner(ctx, a.Database, siteID)
	if err != nil {
		return err
	}
	unlock := appsites.LockSiteName(a.Locks, ownerID, siteName)
	defer unlock()
	// Runtime resources remain local; deploy contents never use this path.
	runtimeDir := a.Disk.SiteDir(files.UserID, files.SiteName)
	if files.TrashID != "" {
		runtimeDir = a.Disk.TrashDir(files.UserID, files.TrashID)
	}
	if err := a.writeStorageArchive(ctx, put, prefix, siteID, filepath.Join(runtimeDir, "runtime")); err != nil {
		return err
	}
	if _, err := a.Disk.StatVersion(ctx, files); err != nil {
		return nil // nothing published yet; saved and runtime data still export
	}
	// The live redirects marker is exported too, also from trash.
	var rules []byte
	if files.TrashID != "" {
		rules, err = a.Disk.ReadTrashedRedirects(files.UserID, files.TrashID)
	} else {
		rules, err = a.Disk.ReadRedirects(files.UserID, files.SiteName)
	}
	if err == nil {
		if err := appsites.PutBytes(put, prefix+"/files/_redirects", rules); err != nil {
			return err
		}
	}
	return a.Disk.WalkFiles(ctx, files, func(file storage.SiteFile, err error) error {
		if err != nil {
			return nil
		}
		if !file.Mode.IsRegular() {
			return nil
		}
		reader, _, err := a.Disk.OpenFile(ctx, files, file.Name)
		if err != nil {
			return nil
		}
		defer reader.Close()
		return put(prefix+"/files/"+file.Name, file.ModTime, file.Size, reader)
	})
}

// writeStorageArchive writes the site's saved data under prefix/storage/:
// resources, file writers and KV entries as JSON, then the runtime tree.
func (a *SiteArchive) writeStorageArchive(ctx context.Context, put appsites.ArchivePut, prefix, siteID, runtime string) error {
	resources, err := a.exportResources(ctx, siteID)
	if err != nil {
		return err
	}
	if len(resources) > 0 {
		b, err := json.MarshalIndent(map[string]any{"resources": resources}, "", "  ")
		if err != nil {
			return err
		}
		if err := appsites.PutBytes(put, prefix+"/storage/resources.json", b); err != nil {
			return err
		}
	}
	fileOwners, err := sitesrepo.ExportFileOwners(ctx, a.Database, siteID)
	if err != nil {
		return err
	}
	if len(fileOwners) > 0 {
		b, err := json.Marshal(map[string]any{"items": fileOwners})
		if err != nil {
			return err
		}
		if err := appsites.PutBytes(put, prefix+"/storage/file-writers.json", b); err != nil {
			return err
		}
	}
	entries, err := a.exportKVEntries(ctx, siteID, runtime)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		b, err := json.MarshalIndent(map[string]any{"items": entries}, "", "  ")
		if err != nil {
			return err
		}
		if err := appsites.PutBytes(put, prefix+"/storage/kv.json", b); err != nil {
			return err
		}
	}
	return a.writeRuntimeArchive(ctx, put, prefix, siteID, runtime)
}

// exportKVEntries reads every KV namespace's runtime database, in database order.
func (a *SiteArchive) exportKVEntries(ctx context.Context, siteID, runtime string) ([]sitesrepo.ExportKVEntry, error) {
	entries := []sitesrepo.ExportKVEntry{}
	names, err := sitesrepo.ExportKVNamespaces(ctx, a.Database, siteID)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		err := a.admit(ctx, siteID, func(ctx context.Context) error {
			conn, done, err := storage.OpenRuntimeKV(ctx, storage.RuntimeKVPath(runtime, name), false, false)
			if err != nil {
				return err
			}
			defer done()
			items, err := storage.RuntimeKVExport(conn)
			if err != nil {
				return err
			}
			for _, item := range items {
				entries = append(entries, sitesrepo.ExportKVEntry{Resource: name, Key: item.Key, Value: item.Value, WriterID: item.VisitorID})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(names) == 0 {
		return entries, nil
	}
	// Preserve the export's existing database collation: the keys are read
	// from SQLite, whose visitor list uses byte order.
	return sitesrepo.OrderExportKV(ctx, a.Database, entries)
}

// writeRuntimeArchive copies the runtime tree under prefix/storage/runtime/,
// checkpointing each SQLite database first and leaving out the KV folder,
// half-deleted folders and SQLite side files.
func (a *SiteArchive) writeRuntimeArchive(ctx context.Context, put appsites.ArchivePut, prefix, siteID, runtime string) error {
	if _, err := os.Stat(runtime); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(runtime, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (strings.HasPrefix(d.Name(), ".deleting-") || p == filepath.Join(runtime, "kv")) {
			return filepath.SkipDir
		}
		if d.IsDir() || !d.Type().IsRegular() || strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return nil
		}
		if strings.HasSuffix(p, ".sqlite") {
			if err := a.admit(ctx, siteID, func(ctx context.Context) error {
				return storage.CheckpointExportSQLite(ctx, p, a.SQLiteMaxMemory)
			}); err != nil {
				return err
			}
		}
		rel, err := filepath.Rel(runtime, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		return put(prefix+"/storage/runtime/"+filepath.ToSlash(rel), info.ModTime(), info.Size(), f)
	})
}
