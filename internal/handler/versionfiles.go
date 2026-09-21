package handler

import (
	"database/sql"
	"errors"
	"io"
	"io/fs"
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

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

var errInvalidVersionPath = errors.New("invalid path")

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
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || n < 1 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid version"})
		return
	}
	site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	versions, err := db.ListVersionsBySite(r.Context(), h.database, site.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	serveVersionFiles(w, r, h.disk.VersionDir(user.ID, siteName, n), versions, n, site.ActiveVersion, listing)
}

// serveVersionFiles checks retained metadata as well as disk: a pruned version
// must not be readable even if its directory has not yet been removed.
func serveVersionFiles(w http.ResponseWriter, r *http.Request, dir string, versions []db.Version, n, active int, listing bool) {
	var version *db.Version
	for i := range versions {
		if versions[i].VersionNumber == n {
			version = &versions[i]
			break
		}
	}
	if version == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) || (err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.IsDir())) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "version not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if listing {
		files, err := walkVersionFiles(dir)
		if err != nil {
			writeVersionFileError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, versionFilesResponse{n, n == active, version.CreatedAt, files})
		return
	}
	path, err := versionFilePath(dir, r.PathValue("path"))
	if err != nil {
		writeVersionFileError(w, err)
		return
	}
	file, err := os.Open(path)
	if err != nil {
		writeVersionFileError(w, err)
		return
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil {
		writeVersionFileError(w, err)
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "file not found"})
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
		log.Printf("serve version file %s: %v", path, err)
	}
}

func writeVersionFileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errInvalidVersionPath):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid path"})
	case os.IsNotExist(err), errors.Is(err, syscall.ENOTDIR):
		// ENOTDIR: a path component is a regular file (files/index.html/child).
		// To the caller that is simply a file that does not exist.
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "file not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	}
}

func withinVersionDir(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func versionFilePath(dir, path string) (string, error) {
	if strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\\\x00") {
		return "", errInvalidVersionPath
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", errInvalidVersionPath
		}
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.Clean(path))
	if !withinVersionDir(root, target) {
		return "", errInvalidVersionPath
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	if !withinVersionDir(root, resolved) {
		return "", errInvalidVersionPath
	}
	return resolved, nil
}

func walkVersionFiles(dir string) ([]versionFile, error) {
	files := make([]versionFile, 0)
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		safePath, err := versionFilePath(dir, filepath.ToSlash(rel))
		if errors.Is(err, errInvalidVersionPath) || os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		info, err := os.Stat(safePath)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			files = append(files, versionFile{filepath.ToSlash(rel), info.Size()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}
