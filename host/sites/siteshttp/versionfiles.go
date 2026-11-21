package siteshttp

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
)

var errInvalidVersionPath = sites.ErrInvalidFilePath

type versionFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type versionFilesResponse struct {
	VersionNumber int           `json:"version_number"`
	IsActive      bool          `json:"is_active"`
	CreatedAt     time.Time     `json:"created_at"`
	Files         []versionFile `json:"files"`
}

func (h *SiteHandler) listVersionFiles(w http.ResponseWriter, r *http.Request) {
	h.readVersionFiles(w, r, true)
}

func (h *SiteHandler) getVersionFile(w http.ResponseWriter, r *http.Request) {
	h.readVersionFiles(w, r, false)
}

func (h *SiteHandler) readVersionFiles(w http.ResponseWriter, r *http.Request, listing bool) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid version"})
		return
	}
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeSiteNotFound(w, siteName)
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	versions, err := h.SitesSvc().ListVersionsBySite(r.Context(), site.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	serveVersionFiles(w, r, h.Disk, sites.FileVersion{UserID: user.ID, SiteName: siteName, Version: n}, versions, n, site.ActiveVersion, listing)
}

// serveVersionFiles checks retained metadata as well as disk: a pruned version
// must not be readable even if its directory has not yet been removed.
func serveVersionFiles(w http.ResponseWriter, r *http.Request, files sites.SiteFiles, ref sites.FileVersion, versions []model.Version, n, active int, listing bool) {
	var version *model.Version
	for i := range versions {
		if versions[i].VersionNumber == n {
			version = &versions[i]
			break
		}
	}
	if version == nil {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	_, err := files.StatVersion(r.Context(), ref)
	if os.IsNotExist(err) {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "version not found"})
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	if listing {
		entries, err := walkVersionFiles(r.Context(), files, ref)
		if err != nil {
			writeVersionFileError(w, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, versionFilesResponse{n, n == active, version.CreatedAt, entries})
		return
	}
	if !validVersionFilePath(r.PathValue("path")) {
		writeVersionFileError(w, errInvalidVersionPath)
		return
	}
	file, info, err := files.OpenFile(r.Context(), ref, r.PathValue("path"))
	if err != nil {
		writeVersionFileError(w, err)
		return
	}
	defer file.Close()
	if info.IsDir() {
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "file not found"})
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(r.PathValue("path")))
	if contentType == "" {
		contentType = "application/octet-stream"
	} else if contentType == "text/html" {
		contentType += "; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, file); err != nil {
		log.Printf("serve version file %s: %v", r.PathValue("path"), err)
	}
}

func writeVersionFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidVersionPath):
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid path"})
	case os.IsNotExist(err), errors.Is(err, syscall.ENOTDIR):
		// ENOTDIR: a path component is a regular file (files/index.html/child).
		// To the caller that is simply a file that does not exist.
		httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "file not found"})
	default:
		httpx.WriteInternal(w)
	}
}

func validVersionFilePath(path string) bool {
	if strings.ContainsAny(path, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func walkVersionFiles(ctx context.Context, files sites.SiteFiles, ref sites.FileVersion) ([]versionFile, error) {
	entries := make([]versionFile, 0)
	err := files.WalkFiles(ctx, ref, func(file sites.SiteFile, err error) error {
		if err != nil {
			return err
		}
		// Retain the API's support for safe internal file symlinks and skip
		// invalid names and links that escape or no longer exist.
		if !validVersionFilePath(file.Name) {
			return nil
		}
		info, err := files.StatFile(ctx, ref, file.Name)
		if errors.Is(err, errInvalidVersionPath) || os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			entries = append(entries, versionFile{file.Name, info.Size()})
		}
		return nil
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, err
}
