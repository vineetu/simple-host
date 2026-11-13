package handler

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
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
	site, err := h.lifecycleSvc().ExportSite(r.Context(), sites.ExportSiteInput{UserID: user.ID, Name: name})
	if err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "not found"})
		return
	}

	h.writeExport(w, r, site)
}

// writeExport streams the archive for one site, already authorized. Shared by
// the keyed route and the short-lived download link (exportlink.go).
func (h *SiteHandler) writeExport(w http.ResponseWriter, r *http.Request, site model.Site) {
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, site.Name))

	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	if err := h.writeSiteTar(r.Context(), tw, site.Name, site); err != nil {
		panic(http.ErrAbortHandler)
	}
	finishSiteTar(tw, gz)
}

// exportAll streams every site on the instance as one .tar.gz, each under
// <handle>/<site>/ in the same layout as the per-site export. For an organiser
// who has to delete the box today and wants to keep every entry and what it
// saved. Admin only.
func (h *SiteHandler) exportAll(w http.ResponseWriter, r *http.Request) {
	if !accountAdmin(w, r) {
		return
	}
	sites, err := h.lifecycleSvc().ExportSites(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="all-entries-%s.tar.gz"`, time.Now().UTC().Format("2006-01-02")))
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, s := range sites {
		owner := s.OwnerHandle
		if owner == "" {
			owner = s.OwnerUsername
		}
		// Owner names are handles or account names; keep the archive path to
		// one plain segment whatever they hold.
		owner = strings.NewReplacer("/", "_", "\\", "_", "..", "_").Replace(owner)
		if err := h.writeSiteTar(r.Context(), tw, owner+"/"+s.Name, s); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	finishSiteTar(tw, gz)
}

// finishSiteTar finalizes only complete exports. Aborting without closing the
// archive writers prevents a failed export from becoming a valid partial backup.
func finishSiteTar(tw *tar.Writer, gz *gzip.Writer) {
	if err := tw.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
	if err := gz.Close(); err != nil {
		panic(http.ErrAbortHandler)
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
func (h *SiteHandler) writeSiteTar(ctx context.Context, tw *tar.Writer, prefix string, site model.Site) error {
	return h.writeSiteArchive(ctx, tarPut(tw), prefix, site.ID, filepath.Join(h.disk.SiteDir(site.UserID, site.Name), "current"))
}

// writeSiteArchive streams the values yielded while the service holds the site lock.
func (h *SiteHandler) writeSiteArchive(ctx context.Context, put archivePut, prefix, siteID, current string) error {
	for entry, err := range h.lifecycleSvc().Export(ctx, sites.ExportInput{Prefix: prefix, SiteID: siteID, Current: current}) {
		if err != nil {
			return err
		}
		if err := put(entry.Name, entry.ModTime, entry.Size, entry.Reader); err != nil {
			return err
		}
	}
	return nil
}
