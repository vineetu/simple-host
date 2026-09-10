package handler

import (
	"archive/tar"
	"compress/gzip"
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

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, name))

	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	// The saved data first, because it is the part nothing else preserves: the
	// files exist in whatever the person built from, the JSON only lives here.
	state := "null"
	if raw, _, err := db.GetSiteState(r.Context(), h.database, name); err == nil && len(raw) > 0 {
		state = string(raw)
	}
	if err := writeTarBytes(tw, name+"/state.json", []byte(state)); err != nil {
		return
	}
	if items, err := exportCollections(r, h.database, site.ID); err == nil && len(items) > 0 {
		if b, err := json.MarshalIndent(items, "", "  "); err == nil {
			_ = writeTarBytes(tw, name+"/collections.json", b)
		}
	}

	root := h.disk.SiteDir(site.UserID, name)
	current := filepath.Join(root, "current")
	if _, err := os.Stat(current); err != nil {
		return // nothing published yet; the data above is still worth having
	}
	_ = filepath.WalkDir(current, func(path string, d fs.DirEntry, err error) error {
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
			Name: name + "/files/" + filepath.ToSlash(rel),
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

func exportCollections(r *http.Request, database *sql.DB, siteID string) (map[string][]json.RawMessage, error) {
	rows, err := database.QueryContext(r.Context(),
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
