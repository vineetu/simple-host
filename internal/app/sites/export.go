package sites

import (
	"bytes"
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
	"time"

	repo "github.com/vsriram/simple-host/internal/repository/sites"
	"github.com/vsriram/simple-host/internal/storage"
)

// ExportInput selects a site's current or trashed directory and archive prefix.
type ExportInput struct{ Prefix, SiteID, Current string }

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
		}, in.Prefix, in.SiteID, in.Current)
		if err != nil && err != stopped {
			yield(ExportEntry{}, err)
		}
	}
}

type archivePut func(string, time.Time, int64, io.Reader) error

func putBytes(put archivePut, name string, b []byte) error {
	return put(name, time.Now(), int64(len(b)), bytes.NewReader(b))
}

// writeSiteArchive writes one site under prefix: state.json (saved data),
// collections.json (every list, private ones included, each entry with its
// id, time and submitter), and files/ (the version in current, the site's
// live folder or, for a site in Recently deleted, the one waiting in trash).
func (s *Service) writeSiteArchive(ctx context.Context, put archivePut, prefix, siteID, current string) error {
	ownerID, siteName, err := repo.ExportOwner(ctx, s.database, siteID)
	if err != nil {
		return err
	}
	unlock := s.deps.LockSite(ownerID, siteName)
	defer unlock()
	// The saved data first, because it is the part nothing else preserves: the
	// files exist in whatever the person built from, the JSON only lives here.
	state := "null"
	if raw, _, err := repo.GetSiteStateByID(ctx, s.database, siteID); err == nil && len(raw) > 0 {
		state = string(raw)
	}
	if err := putBytes(put, prefix+"/state.json", []byte(state)); err != nil {
		return err
	}
	if items, err := exportCollections(ctx, s.database, siteID); err == nil && len(items) > 0 {
		if b, err := json.MarshalIndent(items, "", "  "); err == nil {
			if err := putBytes(put, prefix+"/collections.json", b); err != nil {
				return err
			}
		}
	}

	// Runtime storage is a sibling of current, so it survives deploys and
	// version rollback and is included for both live and recently deleted sites.
	if err := s.writeStorageArchive(ctx, put, prefix, siteID, filepath.Join(filepath.Dir(current), "runtime")); err != nil {
		return err
	}
	if _, err := os.Stat(current); err != nil {
		return nil // nothing published yet; saved and runtime data still export
	}
	return filepath.WalkDir(current, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(current, path)
		if rerr != nil {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.Mode().IsRegular() {
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return nil
		}
		defer f.Close()
		return put(prefix+"/files/"+filepath.ToSlash(rel), info.ModTime(), info.Size(), f)
	})
}

func (s *Service) writeStorageArchive(ctx context.Context, put archivePut, prefix, siteID, runtime string) error {
	resources, err := s.deps.ExportResources(ctx, siteID)
	if err != nil {
		return err
	}
	if len(resources) > 0 {
		b, e := json.MarshalIndent(map[string]any{"resources": resources}, "", "  ")
		if e != nil {
			return e
		}
		if e = putBytes(put, prefix+"/storage/resources.json", b); e != nil {
			return e
		}
	}
	fileOwners, e := repo.ExportFileOwners(ctx, s.database, siteID)
	if e != nil {
		return e
	}
	if len(fileOwners) > 0 {
		b, e := json.Marshal(map[string]any{"items": fileOwners})
		if e != nil {
			return e
		}
		if e = putBytes(put, prefix+"/storage/file-writers.json", b); e != nil {
			return e
		}
	}
	entries, err := repo.ExportKV(ctx, s.database, siteID)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		b, e := json.MarshalIndent(map[string]any{"items": entries}, "", "  ")
		if e != nil {
			return e
		}
		if e = putBytes(put, prefix+"/storage/kv.json", b); e != nil {
			return e
		}
	}
	if _, err := os.Stat(runtime); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return filepath.WalkDir(runtime, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".deleting-") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return nil
		}
		if strings.HasSuffix(p, ".sqlite") {
			if e := storage.CheckpointExportSQLite(ctx, p, s.deps.ExportSQLiteMaxMemory); e != nil {
				return e
			}
		}
		rel, e := filepath.Rel(runtime, p)
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		defer f.Close()
		return put(prefix+"/storage/runtime/"+filepath.ToSlash(rel), info.ModTime(), info.Size(), f)
	})
}

// exportItem is one list entry in collections.json: its id, when it was
// saved, who sent it (when they were signed in) and what the page saved.
type exportItem struct {
	ID          int64           `json:"id"`
	CreatedAt   time.Time       `json:"created_at"`
	SubmittedBy string          `json:"submitted_by,omitempty"`
	Data        json.RawMessage `json:"data"`
}

func exportCollections(ctx context.Context, database *sql.DB, siteID string) (map[string][]exportItem, error) {
	items, err := repo.ListExportItems(ctx, database, siteID)
	if err != nil {
		return nil, err
	}
	out := map[string][]exportItem{}
	for _, it := range items {
		out[it.Collection] = append(out[it.Collection], exportItem{
			ID: it.ID, CreatedAt: it.CreatedAt.UTC(), SubmittedBy: it.SubmittedBy, Data: json.RawMessage(it.Data),
		})
	}
	return out, nil
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
