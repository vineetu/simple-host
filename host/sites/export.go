package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/sites/repo"
	"github.com/vsriram/simple-host/host/storage"
)

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
func (s *Service) Export(ctx context.Context, in ExportInput) iter.Seq2[ExportEntry, error] {
	return func(yield func(ExportEntry, error) bool) {
		stopped := errors.New("archive consumer stopped")
		err := s.writeSiteArchive(ctx, func(name string, modTime time.Time, size int64, r io.Reader) error {
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

type archivePut func(string, time.Time, int64, io.Reader) error

func putBytes(put archivePut, name string, b []byte) error {
	return put(name, time.Now(), int64(len(b)), bytes.NewReader(b))
}

// writeSiteArchive writes one site under prefix: storage/ (the site's saved
// data) and files/ (the version in current, the site's live folder or, for a
// site in Recently deleted, the one waiting in trash).
func (s *Service) writeSiteArchive(ctx context.Context, put archivePut, prefix, siteID string, files storage.FileVersion) error {
	ownerID, siteName, err := repo.ExportOwner(ctx, s.database, siteID)
	if err != nil {
		return err
	}
	unlock := s.deps.LockSite(ownerID, siteName)
	defer unlock()
	// Runtime resources remain local; deploy contents never use this path.
	runtimeDir := s.disk.SiteDir(files.UserID, files.SiteName)
	if files.TrashID != "" {
		runtimeDir = s.disk.TrashDir(files.UserID, files.TrashID)
	}
	if err := s.writeStorageArchive(ctx, put, prefix, siteID, filepath.Join(runtimeDir, "runtime")); err != nil {
		return err
	}
	if _, err := s.disk.StatVersion(ctx, files); err != nil {
		return nil // nothing published yet; saved and runtime data still export
	}
	// Keep the existing export of the live redirects marker, also from trash.
	var rules []byte
	if files.TrashID != "" {
		rules, err = s.disk.ReadTrashedRedirects(files.UserID, files.TrashID)
	} else {
		rules, err = s.disk.ReadRedirects(files.UserID, files.SiteName)
	}
	if err == nil {
		if err := putBytes(put, prefix+"/files/_redirects", rules); err != nil {
			return err
		}
	}
	return s.disk.WalkFiles(ctx, files, func(file storage.SiteFile, err error) error {
		if err != nil {
			return nil
		}
		if !file.Mode.IsRegular() {
			return nil
		}
		reader, _, err := s.disk.OpenFile(ctx, files, file.Name)
		if err != nil {
			return nil
		}
		defer reader.Close()
		return put(prefix+"/files/"+file.Name, file.ModTime, file.Size, reader)
	})
}

// writeStorageArchive writes the site's saved data under prefix/storage/:
// resources, file writers and KV entries as JSON, then the runtime tree.
func (s *Service) writeStorageArchive(ctx context.Context, put archivePut, prefix, siteID, runtime string) error {
	resources, err := s.deps.ExportResources(ctx, siteID)
	if err != nil {
		return err
	}
	if len(resources) > 0 {
		b, err := json.MarshalIndent(map[string]any{"resources": resources}, "", "  ")
		if err != nil {
			return err
		}
		if err := putBytes(put, prefix+"/storage/resources.json", b); err != nil {
			return err
		}
	}
	fileOwners, err := repo.ExportFileOwners(ctx, s.database, siteID)
	if err != nil {
		return err
	}
	if len(fileOwners) > 0 {
		b, err := json.Marshal(map[string]any{"items": fileOwners})
		if err != nil {
			return err
		}
		if err := putBytes(put, prefix+"/storage/file-writers.json", b); err != nil {
			return err
		}
	}
	entries, err := s.exportKVEntries(ctx, siteID, runtime)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		b, err := json.MarshalIndent(map[string]any{"items": entries}, "", "  ")
		if err != nil {
			return err
		}
		if err := putBytes(put, prefix+"/storage/kv.json", b); err != nil {
			return err
		}
	}
	return s.writeRuntimeArchive(ctx, put, prefix, siteID, runtime)
}

// exportKVEntries reads every KV namespace's runtime database, in database order.
func (s *Service) exportKVEntries(ctx context.Context, siteID, runtime string) ([]repo.ExportKVEntry, error) {
	entries := []repo.ExportKVEntry{}
	names, err := repo.ExportKVNamespaces(ctx, s.database, siteID)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		err := s.deps.AdmitSQLite(ctx, siteID, func(ctx context.Context) error {
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
				entries = append(entries, repo.ExportKVEntry{Resource: name, Key: item.Key, Value: item.Value, WriterID: item.VisitorID})
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
	return repo.OrderExportKV(ctx, s.database, entries)
}

// writeRuntimeArchive copies the runtime tree under prefix/storage/runtime/,
// checkpointing each SQLite database first and leaving out the KV folder,
// half-deleted folders and SQLite side files.
func (s *Service) writeRuntimeArchive(ctx context.Context, put archivePut, prefix, siteID, runtime string) error {
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
			if err := s.deps.AdmitSQLite(ctx, siteID, func(ctx context.Context) error {
				return storage.CheckpointExportSQLite(ctx, p, s.deps.ExportSQLiteMaxMemory)
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

// PreviewSiteInput selects a complete version for a team preview.
type PreviewSiteInput struct {
	AccountID, Name string
	Version         int
}

// PreviewSite reads the site and verifies that its requested version is kept.
func (s *Service) PreviewSite(ctx context.Context, in PreviewSiteInput) (repo.Site, bool, error) {
	site, err := repo.GetSiteByUser(ctx, s.database, in.AccountID, in.Name)
	if err != nil {
		return repo.Site{}, false, err
	}
	exists, err := repo.VersionKept(ctx, s.database, site.ID, in.Version)
	return site, exists, err
}

// ExportSiteInput selects an owner's site by name or authenticates a token-bound site ID.
type ExportSiteInput struct {
	UserID, Name, SiteID string
	DownloadLink         bool
}

// ExportSite resolves the site for an archive or a download link.
func (s *Service) ExportSite(ctx context.Context, in ExportSiteInput) (repo.Site, error) {
	if !in.DownloadLink {
		return repo.GetSiteByUser(ctx, s.database, in.UserID, in.Name)
	}
	site, err := repo.GetSiteByID(ctx, s.database, in.SiteID)
	if err != nil || site.UserID != in.UserID || site.Deleted {
		return repo.Site{}, &Error{Code: "export_link_invalid"}
	}
	// A link minted before suspension stops with its owner; a site-only takedown still exports.
	if site.OwnerSuspended {
		return repo.Site{}, &Error{Code: "account_suspended"}
	}
	return site, nil
}

// ExportSites lists all instance sites for an administrator's archive.
func (s *Service) ExportSites(ctx context.Context) ([]repo.Site, error) {
	return repo.ListAllSites(ctx, s.database)
}
