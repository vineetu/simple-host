package handler

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// exportSite streams one site as a .tar.gz: its files and its saved data.
//
// This exists because an event box is destroyed when the event ends, and
// everything on it goes with it. Nothing is backed up anywhere, so the honest
// answer is to make it trivial for the person who made something to take it
// with them.
//
// Streamed rather than assembled in memory: a site can hold a hundred megabytes
// of files, and an event box has a gigabyte of RAM.
func (h *SiteHandler) exportSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	h.writeExport(w, r, site)
}

// writeExport streams the archive for one site, already authorized. Shared by
// the keyed route and the short-lived download link (exportlink.go).
func (h *SiteHandler) writeExport(w http.ResponseWriter, r *http.Request, site db.Site) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, site.Name))

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	_ = h.writeSiteTar(r.Context(), tw, site.Name, site)
}

// exportAll streams every site on the instance as one .tar.gz, each under
// <handle>/<site>/ in the same layout as the per-site export. For an organiser
// who has to delete the box today and wants to keep every entry and what it
// saved. Admin only.
func (h *SiteHandler) exportAll(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	sites, err := db.ListAllSites(r.Context(), h.database)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="all-entries-%s.tar.gz"`, time.Now().UTC().Format("2006-01-02")))
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	for _, s := range sites {
		owner := s.OwnerHandle
		if owner == "" {
			owner = s.OwnerUsername
		}
		// Owner names are handles or account names; keep the archive path to
		// one plain segment whatever they hold.
		owner = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(owner)
		if err := h.writeSiteTar(r.Context(), tw, owner+"/"+s.Name, s); err != nil {
			return // the client went away, or the stream broke
		}
	}
}

// archivePut adds one file to an archive being streamed (a .tar.gz or a
// .zip), so one site's layout is written the same way into either.
type archivePut func(name string, modTime time.Time, size int64, r io.Reader) error

func tarPut(tw *tar.Writer) archivePut {
	return func(name string, modTime time.Time, size int64, r io.Reader) error {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o644, Size: size, ModTime: modTime, Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		_, err := io.Copy(tw, r)
		return err
	}
}

func zipPut(zw *zip.Writer) archivePut {
	return func(name string, modTime time.Time, _ int64, r io.Reader) error {
		f, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modTime})
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		return err
	}
}

func putBytes(put archivePut, name string, b []byte) error {
	return put(name, time.Now(), int64(len(b)), bytes.NewReader(b))
}

// writeSiteTar writes one live site into tw under prefix (writeSiteArchive).
func (h *SiteHandler) writeSiteTar(ctx context.Context, tw *tar.Writer, prefix string, site db.Site) error {
	return h.writeSiteArchive(ctx, tarPut(tw), prefix, site.ID, filepath.Join(h.disk.SiteDir(site.UserID, site.Name), "current"))
}

// writeSiteArchive writes one site under prefix: state.json (saved data),
// collections.json (every list, private ones included, each entry with its
// id, time and submitter), and files/ (the version in current, the site's
// live folder or, for a site in Recently deleted, the one waiting in trash).
func (h *SiteHandler) writeSiteArchive(ctx context.Context, put archivePut, prefix, siteID, current string) error {
	var ownerID, siteName string
	if err := h.database.QueryRowContext(ctx, `SELECT user_id::text,name FROM sites WHERE id=$1`, siteID).Scan(&ownerID, &siteName); err != nil {
		return err
	}
	unlock := h.lockSite(ownerID, siteName)
	defer unlock()
	// The saved data first, because it is the part nothing else preserves: the
	// files exist in whatever the person built from, the JSON only lives here.
	state := "null"
	if raw, _, err := db.GetSiteStateByID(ctx, h.database, siteID); err == nil && len(raw) > 0 {
		state = string(raw)
	}
	if err := putBytes(put, prefix+"/state.json", []byte(state)); err != nil {
		return err
	}
	if items, err := exportCollections(ctx, h.database, siteID); err == nil && len(items) > 0 {
		if b, err := json.MarshalIndent(items, "", "  "); err == nil {
			if err := putBytes(put, prefix+"/collections.json", b); err != nil {
				return err
			}
		}
	}

	// Runtime storage is a sibling of current, so it survives deploys and
	// version rollback and is included for both live and recently deleted sites.
	if err := h.writeStorageArchive(ctx, put, prefix, siteID, filepath.Join(filepath.Dir(current), "runtime")); err != nil {
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

type storageExportKV struct {
	Resource string          `json:"resource"`
	Key      string          `json:"key"`
	Value    json.RawMessage `json:"value"`
	WriterID string          `json:"writer_id"`
}

func (h *SiteHandler) writeStorageArchive(ctx context.Context, put archivePut, prefix, siteID, runtime string) error {
	rows, err := h.database.QueryContext(ctx, `SELECT name,kind,read_policy,write_policy,site_passcode,write_mode FROM site_storage_resources WHERE site_id=$1 ORDER BY name`, siteID)
	if err != nil {
		return err
	}
	resources := []storageResource{}
	for rows.Next() {
		var x storageResource
		if err := rows.Scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.SitePasscode, &x.WriteMode); err != nil {
			rows.Close()
			return err
		}
		resources = append(resources, x)
	}
	err = rows.Err()
	rows.Close()
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
	fileRows, e := h.database.QueryContext(ctx, `SELECT resource_name,path,writer_id FROM site_storage_files WHERE site_id=$1 ORDER BY resource_name,path`, siteID)
	if e != nil {
		return e
	}
	fileOwners := []map[string]string{}
	for fileRows.Next() {
		var resource, path, writer string
		if e = fileRows.Scan(&resource, &path, &writer); e != nil {
			fileRows.Close()
			return e
		}
		fileOwners = append(fileOwners, map[string]string{"resource": resource, "path": path, "writer_id": writer})
	}
	e = fileRows.Err()
	fileRows.Close()
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
	kvRows, err := h.database.QueryContext(ctx, `SELECT resource_name,key,value,writer_id FROM site_storage_kv WHERE site_id=$1 ORDER BY resource_name,key`, siteID)
	if err != nil {
		return err
	}
	entries := []storageExportKV{}
	for kvRows.Next() {
		var x storageExportKV
		if err := kvRows.Scan(&x.Resource, &x.Key, &x.Value, &x.WriterID); err != nil {
			kvRows.Close()
			return err
		}
		entries = append(entries, x)
	}
	err = kvRows.Err()
	kvRows.Close()
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
			conn, e := sqlite3.OpenContext(sqlite3.WithMaxMemory(ctx, storageSQLiteMaxMemory), p)
			if e != nil {
				return e
			}
			_, _, e = conn.WALCheckpoint("main", sqlite3.CHECKPOINT_TRUNCATE)
			if ce := conn.Close(); e == nil {
				e = ce
			}
			if e != nil {
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
	items, err := db.ListExportItems(ctx, database, siteID)
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
