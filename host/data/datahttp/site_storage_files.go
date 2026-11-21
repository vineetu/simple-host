package datahttp

import (
	"encoding/base64"
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

	appstorage "github.com/vsriram/simple-host/host/data"
	httpx "github.com/vsriram/simple-host/host/httpx"
	"golang.org/x/text/unicode/norm"
)

func (h *DataHandler) StorageFilesDir(c StorageCall) string {
	return filepath.Join(h.StorageRuntimeDir(c), "files", c.ResourceName)
}
func (h *DataHandler) StorageFilePath(c StorageCall, p string) string {
	return storageFileOnDisk(h.Disk, c.OwnerID, c.SiteName, c.ResourceName, p)
}
func storageFileOnDisk(disk interface{ SiteDir(string, string) string }, ownerID, siteName, resourceName, p string) string {
	return filepath.Join(disk.SiteDir(ownerID, siteName), "runtime", "files", resourceName, filepath.FromSlash(p))
}

// storageFiles serves the file routes: GET .../objects lists, and GET, PUT
// and DELETE .../objects/{path} read, store and delete one file.
func (h *DataHandler) storageFiles(w http.ResponseWriter, r *http.Request, c StorageCall) {
	c, ok := h.storageResourceFor(w, r, c, "files")
	if !ok {
		return
	}
	p := r.PathValue("path")
	if r.Method == http.MethodPut {
		p = norm.NFC.String(p)
	}
	if p == "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			h.storageFileList(w, r, c)
		}
		return
	}
	if !appstorage.ValidFilePath(p) {
		httpx.WriteError(w, 400, "invalid_path", "invalid file path")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.serveStorageFile(w, r, c, p)
	case http.MethodPut:
		h.storeStorageFile(w, r, c, p)
	case http.MethodDelete:
		auth, err := h.StorageFor(c).DeleteFile(r.Context(), appstorage.FileDeleteInput{Call: c, Path: p})
		if !storageAnswered(w, auth, err, 0) {
			httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
		}
	}
}

func (h *DataHandler) storeStorageFile(w http.ResponseWriter, r *http.Request, c StorageCall, p string) {
	body := http.MaxBytesReader(w, r.Body, appstorage.FileMaxBytes())
	result, auth, err := h.StorageFor(c).PutFile(r.Context(), appstorage.FilePutInput{Call: c, Path: p, Body: body})
	if !storageAnswered(w, auth, err, 0) {
		httpx.WriteJSON(w, 200, map[string]any{"path": p, "bytes": result.Bytes, "content_type": result.ContentType})
	}
}

func (h *DataHandler) storageFileList(w http.ResponseWriter, r *http.Request, c StorageCall) {
	prefix, after, limit, valid := storagePage(r)
	items, next, auth, err := h.StorageFor(c).ListFiles(r.Context(), appstorage.FileListInput{Call: c, Prefix: prefix, After: after, Limit: limit, Valid: valid, Root: h.StorageFilesDir(c)})
	if !storageAnswered(w, auth, err, 0) {
		httpx.WriteJSON(w, 200, map[string]any{"items": items, "next_after": next})
	}
}

func (h *DataHandler) serveStorageFile(w http.ResponseWriter, r *http.Request, c StorageCall, p string) {
	f, fi, auth, err := h.StorageFor(c).OpenFile(r.Context(), appstorage.FileReadInput{Call: c, Path: p})
	if !storageAnswered(w, auth, err, 0) {
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

func (h *DataHandler) StorageFileToken(c StorageCall, p string, expires time.Time) string {
	payload := c.OwnerID + "." + c.SiteID + "." + c.ResourceName + "." + base64.RawURLEncoding.EncodeToString([]byte(p)) + "." + strconv.FormatInt(expires.Unix(), 10)
	if c.LinkScope != "" {
		payload += "." + base64.RawURLEncoding.EncodeToString([]byte(c.LinkScope))
	}
	return httpx.SealLink(h.ExportKey, "site storage file v1", []byte(payload))
}
func (h *DataHandler) createStorageFileLink(w http.ResponseWriter, r *http.Request, c StorageCall) {
	c, ok := h.storageResourceFor(w, r, c, "files")
	if !ok || !storageOwner(w, c) {
		return
	}
	h.CreateIdempotency.WrapAccount(c.AccountID, c.SiteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || !appstorage.ValidFilePath(req.Path) {
			httpx.WriteError(w, 400, "invalid_path", "invalid path")
			return
		}
		link, err := h.StorageFor(c).CreateFileLink(r.Context(), appstorage.FileLinkInput{Call: c, Path: req.Path})
		if err != nil {
			WriteStorageError(w, err, http.StatusNotFound)
			return
		}
		httpx.WriteJSON(w, 200, map[string]any{"url": h.linkBase() + "/v1/storage-download?token=" + url.QueryEscape(link.Token), "expires_at": link.Expires.UTC().Format(time.RFC3339)})
	})).ServeHTTP(w, r)
}

// linkBase is the apex address download links are minted on.
func (h *DataHandler) linkBase() string {
	if h.publicBaseURL != "" {
		return h.publicBaseURL
	}
	return "https://" + h.Hosts.Addressing().SiteDomain
}

func (h *DataHandler) DownloadStorageFile(w http.ResponseWriter, r *http.Request) {
	f, fi, p, err := h.StorageSvc().DownloadFile(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		var e *appstorage.Error
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
