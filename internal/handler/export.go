package handler

import (
	"archive/tar"
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

// writeSiteTar writes one site into tw under prefix: state.json (saved data),
// collections.json (every list, private ones included), and files/ (the live
// version).
func (h *SiteHandler) writeSiteTar(ctx context.Context, tw *tar.Writer, prefix string, site db.Site) error {
	// The saved data first, because it is the part nothing else preserves: the
	// files exist in whatever the person built from, the JSON only lives here.
	state := "null"
	if raw, _, err := db.GetSiteStateByID(ctx, h.database, site.ID); err == nil && len(raw) > 0 {
		state = string(raw)
	}
	if err := writeTarBytes(tw, prefix+"/state.json", []byte(state)); err != nil {
		return err
	}
	if items, err := exportCollections(ctx, h.database, site.ID); err == nil && len(items) > 0 {
		if b, err := json.MarshalIndent(items, "", "  "); err == nil {
			if err := writeTarBytes(tw, prefix+"/collections.json", b); err != nil {
				return err
			}
		}
	}

	current := filepath.Join(h.disk.SiteDir(site.UserID, site.Name), "current")
	if _, err := os.Stat(current); err != nil {
		return nil // nothing published yet; the data above is still worth having
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
		if ierr != nil {
			return nil
		}
		f, oerr := os.Open(path)
		if oerr != nil {
			return nil
		}
		defer f.Close()
		hdr := &tar.Header{
			Name: prefix + "/files/" + filepath.ToSlash(rel),
			Mode: 0o644, Size: info.Size(), ModTime: info.ModTime(), Typeflag: tar.TypeReg,
		}
		if werr := tw.WriteHeader(hdr); werr != nil {
			return werr
		}
		_, werr := io.Copy(tw, f)
		return werr
	})
}

func writeTarBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(b)), ModTime: time.Now(), Typeflag: tar.TypeReg,
	}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func exportCollections(ctx context.Context, database *sql.DB, siteID string) (map[string][]json.RawMessage, error) {
	rows, err := database.QueryContext(ctx,
		`SELECT collection, data FROM collection_items WHERE site_id=$1 ORDER BY collection, id`, siteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]json.RawMessage{}
	for rows.Next() {
		var coll string
		var data []byte
		if err := rows.Scan(&coll, &data); err != nil {
			return nil, err
		}
		out[coll] = append(out[coll], json.RawMessage(data))
	}
	return out, rows.Err()
}
