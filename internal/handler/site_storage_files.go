package handler

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
	"strconv"
	"strings"
	"time"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/storage"
	"golang.org/x/text/unicode/norm"
)

func storageFileMaxBytes() int64 {
	return storageLimitBytes(os.Getenv("SITE_STORAGE_FILE_MAX_BYTES"), 1000000, 10<<30)
}

func storageObjectPath(p string) bool { return storage.ValidObjectPath(p) }
func (h *SiteHandler) storageFilesDir(c storageCall) string {
	return storage.RuntimeFilesDir(h.disk, c.ownerID, c.siteName, c.resourceName)
}
func (h *SiteHandler) storageFilePath(c storageCall, p string) string {
	return storageFileOnDisk(h.disk, c.ownerID, c.siteName, c.resourceName, p)
}
func storageFileOnDisk(disk *storage.DiskStorage, ownerID, siteName, resourceName, p string) string {
	return storage.RuntimeFilePath(disk, ownerID, siteName, resourceName, p)
}

func (h *SiteHandler) storageFiles(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "files")
	if !ok {
		return
	}
	p := r.PathValue("path")
	if r.Method == http.MethodPut {
		p = norm.NFC.String(p)
	}
	if p == "" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return
		}
		h.storageFileList(w, r, c)
		return
	}
	if !storageObjectPath(p) {
		storageError(w, 400, "invalid_path", "invalid file path")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.serveStorageFile(w, r, c, p, false)
	case http.MethodPut:
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		service := h.storageRequestSvc(r, c)
		result, auth, err := service.PutFile(r.Context(), appstorage.FilePutInput{Call: call, Path: p, Body: http.MaxBytesReader(w, r.Body, storageFileMaxBytes())})
		if auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, http.StatusForbidden, auth.Code, auth.Message)
				return
			}
			writeStorageRefusals(w, auth.Refusals)
			if !auth.Allowed {
				return
			}
		}
		if err != nil {
			var e *appstorage.Error
			if errors.As(err, &e) {
				switch e.Code {
				case "storage_busy":
					storageBusy(w)
				case "resource_changed", "file_exists":
					storageError(w, 409, e.Code, e.Message)
				case "site_full", "bucket_full":
					storageError(w, 507, e.Code, e.Message)
				case "invalid_file":
					storageError(w, 400, e.Code, e.Message)
				default:
					storageError(w, 500, "internal_error", "internal server error")
				}
			} else {
				storageError(w, 500, "internal_error", "internal server error")
			}
			return
		}
		writeJSON(w, 200, map[string]any{"path": p, "bytes": result.Bytes, "content_type": result.ContentType})
	case http.MethodDelete:
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		auth, err := h.storageRequestSvc(r, c).DeleteFile(r.Context(), appstorage.FileDeleteInput{Call: call, Path: p})
		if auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, http.StatusForbidden, auth.Code, auth.Message)
				return
			}
			writeStorageRefusals(w, auth.Refusals)
			if !auth.Allowed {
				return
			}
		}
		if err != nil {
			var serviceErr *appstorage.Error
			if errors.As(err, &serviceErr) {
				switch serviceErr.Code {
				case "storage_busy":
					storageBusy(w)
				case "resource_changed":
					storageError(w, 409, serviceErr.Code, serviceErr.Message)
				case "file_not_found":
					storageError(w, 404, serviceErr.Code, serviceErr.Message)
				}
			} else {
				storageError(w, 500, "internal_error", "internal server error")
			}
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true})
	}
}

func (h *SiteHandler) storageFileList(w http.ResponseWriter, r *http.Request, c storageCall) {
	prefix, after, limit, valid := storagePage(r)
	call := h.storageServiceCall(c)
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	items, next, auth, err := h.storageRequestSvc(r, c).ListFiles(r.Context(), appstorage.FileListInput{Call: call, Prefix: prefix, After: after, Limit: limit, Valid: valid, Root: h.storageFilesDir(c)})
	if auth != nil {
		if len(auth.Refusals) == 0 {
			storageError(w, http.StatusForbidden, auth.Code, auth.Message)
			return
		}
		writeStorageRefusals(w, auth.Refusals)
		if !auth.Allowed {
			return
		}
	}
	if err != nil {
		var e *appstorage.Error
		if errors.As(err, &e) && e.Code == "invalid_limit" {
			storageError(w, 400, e.Code, e.Message)
		} else {
			storageError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "next_after": next})
}

func (h *SiteHandler) serveStorageFile(w http.ResponseWriter, r *http.Request, c storageCall, p string, linkAuthorized bool) {
	call := h.storageServiceCall(c)
	f, fi, auth, err := h.storageRequestSvc(r, c).OpenFile(r.Context(), appstorage.FileReadInput{Call: call, Path: p, LinkAuthorized: linkAuthorized})
	if auth != nil {
		if len(auth.Refusals) == 0 {
			storageError(w, http.StatusForbidden, auth.Code, auth.Message)
			return
		}
		writeStorageRefusals(w, auth.Refusals)
		if !auth.Allowed {
			return
		}
	}
	if err != nil {
		var serviceErr *appstorage.Error
		if errors.As(err, &serviceErr) && serviceErr.Code == "file_not_found" {
			storageError(w, 404, serviceErr.Code, serviceErr.Message)
		} else {
			storageError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	streamStorageFile(w, r, f, fi, p)
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

func (h *SiteHandler) storageFileToken(c storageCall, p string, expires time.Time) string {
	payload := c.ownerID + "." + c.siteID + "." + c.resourceName + "." + base64.RawURLEncoding.EncodeToString([]byte(p)) + "." + strconv.FormatInt(expires.Unix(), 10)
	if c.linkScope != "" {
		payload += "." + base64.RawURLEncoding.EncodeToString([]byte(c.linkScope))
	}
	return sealLink(h.exportKey, "site storage file v1", []byte(payload))
}
func (h *SiteHandler) createStorageFileLink(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "files")
	if !ok || !h.storageOwner(w, c) {
		return
	}
	h.createIdempotency.wrapAccount(c.accountID, c.siteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || !storageObjectPath(req.Path) {
			storageError(w, 400, "invalid_path", "invalid path")
			return
		}
		link, err := h.storageRequestSvc(r, c).CreateFileLink(r.Context(), appstorage.FileLinkInput{Call: h.storageServiceCall(c), Path: req.Path})
		if err != nil {
			serviceErr := err.(*appstorage.Error)
			status := http.StatusNotFound
			if serviceErr.Code == "forbidden" {
				status = http.StatusForbidden
			}
			storageError(w, status, serviceErr.Code, serviceErr.Message)
			return
		}
		writeJSON(w, 200, map[string]any{"url": h.exportLinkBase() + "/v1/storage-download?token=" + url.QueryEscape(link.Token), "expires_at": link.Expires.UTC().Format(time.RFC3339)})
	})).ServeHTTP(w, r)
}

func (h *SiteHandler) downloadStorageFile(w http.ResponseWriter, r *http.Request) {
	f, fi, p, err := h.storageSvc().DownloadFile(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		var e *appstorage.Error
		if errors.As(err, &e) && (e.Code == "file_link_invalid" || e.Code == "file_not_found") {
			if e.Code == "file_not_found" {
				w.Header().Set("Referrer-Policy", "no-referrer")
			}
			storageError(w, 404, e.Code, e.Message)
		} else {
			storageError(w, 500, "internal_error", "internal server error")
		}
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	streamStorageFile(w, r, f, fi, p)
}
