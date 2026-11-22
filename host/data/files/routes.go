package files

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/data"

	httpx "github.com/vsriram/simple-host/host/httpx"
	"golang.org/x/text/unicode/norm"
)

func StorageFilesDir(h *data.Handler, c data.Call) string {
	return filepath.Join(h.StorageRuntimeDir(c), "files", c.ResourceName)
}
func StorageFilePath(h *data.Handler, c data.Call, p string) string {
	return FilePath(h.Disk, c, p)
}

// storageFiles serves the file routes: GET .../objects lists, and GET, PUT
// and DELETE .../objects/{path} read, store and delete one file.
func storageFiles(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	c, ok := h.StorageResourceFor(w, r, c, "files")
	if !ok {
		return
	}
	p := r.PathValue("path")
	if r.Method == http.MethodPut {
		p = norm.NFC.String(p)
	}
	if p == "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			storageFileList(h, w, r, c)
		}
		return
	}
	if !data.ValidFilePath(p) {
		httpx.WriteError(w, 400, "invalid_path", "invalid file path")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		serveStorageFile(h, w, r, c, p)
	case http.MethodPut:
		storeStorageFile(h, w, r, c, p)
	case http.MethodDelete:
		deleteStorageFile(h, w, r, c, p)
	}
}

func deleteStorageFile(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call, p string) {
	auth, err := DeleteFile(h.StorageFor(c), r.Context(), FileDeleteInput{Call: c, Path: p})
	if !data.StorageAnswered(w, auth, err, 0) {
		httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
	}
}

func storeStorageFile(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call, p string) {
	body := http.MaxBytesReader(w, r.Body, data.FileMaxBytes())
	result, auth, err := PutFile(h.StorageFor(c), r.Context(), FilePutInput{Call: c, Path: p, Body: body})
	if !data.StorageAnswered(w, auth, err, 0) {
		httpx.WriteJSON(w, 200, map[string]any{"path": p, "bytes": result.Bytes, "content_type": result.ContentType})
	}
}

func storageFileList(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	prefix, after, limit, valid := data.StoragePage(r)
	items, next, auth, err := ListFiles(h.StorageFor(c), r.Context(), FileListInput{Call: c, Prefix: prefix, After: after, Limit: limit, Valid: valid, Root: StorageFilesDir(h, c)})
	if !data.StorageAnswered(w, auth, err, 0) {
		httpx.WriteJSON(w, 200, map[string]any{"items": items, "next_after": next})
	}
}

func serveStorageFile(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call, p string) {
	f, fi, auth, err := OpenFile(h.StorageFor(c), r.Context(), FileReadInput{Call: c, Path: p})
	if !data.StorageAnswered(w, auth, err, 0) {
		streamStorageFile(w, r, f, fi, p)
	}
}

func streamStorageFile(w http.ResponseWriter, r *http.Request, f *os.File, fi os.FileInfo, p string) {
	defer f.Close()
	var head [512]byte
	n, _ := f.Read(head[:])
	_, _ = f.Seek(0, io.SeekStart)
	ct := http.DetectContentType(head[:n])
	inline := strings.HasPrefix(ct, "image/png") || strings.HasPrefix(ct, "image/jpeg") || strings.HasPrefix(ct, "image/gif") || strings.HasPrefix(ct, "image/webp")
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	if !inline {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(p)))
	}
	w.Header().Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, f)
	}
}

func StorageFileToken(h *data.Handler, c data.Call, p string, expires time.Time) string {
	return FileToken(h.ExportKey, c, p, expires)
}
func createStorageFileLink(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	c, ok := h.StorageResourceFor(w, r, c, "files")
	if !ok || !data.StorageOwner(w, c) {
		return
	}
	h.CreateIdempotency.WrapAccount(c.AccountID, c.SiteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil {
			httpx.WriteError(w, 400, "invalid_path", "invalid path")
			return
		}
		link, err := CreateFileLink(h.StorageFor(c), r.Context(), FileLinkInput{Call: c, Path: req.Path})
		if err != nil {
			data.WriteStorageError(w, err, http.StatusNotFound)
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"url": linkBase(h) + "/v1/storage-download?token=" + url.QueryEscape(link.Token), "expires_at": link.Expires.UTC().Format(time.RFC3339)})
	})).ServeHTTP(w, r)
}

// linkBase is the apex address download links are minted on.
func linkBase(h *data.Handler) string {
	if h.PublicBaseURL != "" {
		return h.PublicBaseURL
	}
	return "https://" + h.Hosts.Addressing().SiteDomain
}

func DownloadStorageFile(h *data.Handler, w http.ResponseWriter, r *http.Request) {
	f, fi, p, err := DownloadFile(h.StorageSvc(), r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		var e *data.Error
		if errors.As(err, &e) && (e.Code == "file_link_invalid" || e.Code == "file_not_found") {
			if e.Code == "file_not_found" {
				w.Header().Set("Referrer-Policy", "no-referrer")
			}
			httpx.WriteError(w, 404, e.Code, e.Message)
		} else {
			httpx.WriteError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	streamStorageFile(w, r, f, fi, p)
}

// Register maps the file routes behind caller (Access's StorageCaller), and
// the download link, which carries its own sealed token instead.
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/sites/{sitename}/storage/files/{name}/objects", h.StorageRouteFor(caller, storageFiles))
	mux.Handle("GET /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", h.StorageRouteFor(caller, storageFiles))
	mux.Handle("PUT /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", h.StorageRouteFor(caller, storageFiles))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/files/{name}/objects/{path...}", h.StorageRouteFor(caller, storageFiles))
	mux.Handle("POST /v1/sites/{sitename}/storage/files/{name}/download-link", h.StorageRouteFor(caller, createStorageFileLink))
	mux.HandleFunc("GET /v1/storage-download", func(w http.ResponseWriter, r *http.Request) { DownloadStorageFile(h, w, r) })
}
