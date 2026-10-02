package handler

import (
	"crypto/hmac"
	"crypto/sha256"
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

	"github.com/vsriram/simple-host/internal/db"
)

func storageFileMaxBytes() int64 {
	return storageLimitBytes(os.Getenv("SITE_STORAGE_FILE_MAX_BYTES"), 1000000, 10<<30)
}

func storageObjectPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) || len(p) > 1024 || path.Clean(p) != p {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if s == "" || s == "." || s == ".." || strings.HasPrefix(s, ".") {
			return false
		}
	}
	return true
}
func (h *SiteHandler) storageFilesDir(c storageCall) string {
	return filepath.Join(h.storageRuntimeDir(c), "files", c.resourceName)
}
func (h *SiteHandler) storageFilePath(c storageCall, p string) string {
	return filepath.Join(h.storageFilesDir(c), filepath.FromSlash(p))
}

func (h *SiteHandler) storageFiles(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "files")
	if !ok {
		return
	}
	p := r.PathValue("path")
	if p == "" {
		if r.Method != http.MethodGet || !h.storageAccess(w, r, c, false) {
			return
		}
		h.storageFileList(w, r, c)
		return
	}
	if !storageObjectPath(p) {
		storageError(w, 400, "invalid_path", "invalid file path")
		return
	}
	if !h.storageAccess(w, r, c, r.Method != http.MethodGet) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.serveStorageFile(w, r, c, p)
	case http.MethodPut:
		unlock := h.lockSite(c.ownerID, c.siteName)
		defer unlock()
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, storageFileMaxBytes()))
		if err != nil {
			storageError(w, 400, "invalid_file", "file too large")
			return
		}
		dest := h.storageFilePath(c, p)
		oldBytes := int64(0)
		if fi, e := os.Stat(dest); e == nil && fi.Mode().IsRegular() {
			oldBytes = fi.Size()
		}
		usage, err := h.measureSiteStorage(r.Context(), c)
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if usage.total()-oldBytes+int64(len(body)) > storageSiteLimitBytes() {
			storageError(w, 507, "site_full", "site storage is full")
			return
		}
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		f, err := os.CreateTemp(filepath.Dir(dest), ".upload-")
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		defer os.Remove(f.Name())
		if _, err = f.Write(body); err == nil {
			err = f.Sync()
		}
		if e := f.Close(); err == nil {
			err = e
		}
		if err == nil {
			err = os.Rename(f.Name(), dest)
		}
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		h.disk.MarkChanged()
		writeJSON(w, 200, map[string]any{"path": p, "bytes": len(body), "content_type": http.DetectContentType(body)})
	case http.MethodDelete:
		unlock := h.lockSite(c.ownerID, c.siteName)
		defer unlock()
		if err := os.Remove(h.storageFilePath(c, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		h.disk.MarkChanged()
		writeJSON(w, 200, map[string]any{"deleted": true})
	}
}

func (h *SiteHandler) storageFileList(w http.ResponseWriter, r *http.Request, c storageCall) {
	prefix, after, limit, ok := storagePage(r)
	if !ok {
		storageError(w, 400, "invalid_limit", "invalid limit")
		return
	}
	type item struct {
		Path        string `json:"path"`
		Bytes       int64  `json:"bytes"`
		ContentType string `json:"content_type"`
	}
	out := []item{}
	root := h.storageFilesDir(c)
	err := filepath.WalkDir(root, func(fp string, d os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, e := filepath.Rel(root, fp)
		if e != nil {
			return e
		}
		p := filepath.ToSlash(rel)
		if !strings.HasPrefix(p, prefix) || p <= after {
			return nil
		}
		fi, e := d.Info()
		if e != nil {
			return e
		}
		f, e := os.Open(fp)
		if e != nil {
			return e
		}
		var b [512]byte
		n, _ := f.Read(b[:])
		f.Close()
		out = append(out, item{p, fi.Size(), http.DetectContentType(b[:n])})
		if len(out) >= limit+1 {
			return io.EOF
		}
		return nil
	})
	if err != nil && !errors.Is(err, io.EOF) {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	// WalkDir's lexical ordering gives deterministic pagination.
	next := ""
	if len(out) > limit {
		next = out[limit-1].Path
		out = out[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": out, "next_after": next})
}

func (h *SiteHandler) serveStorageFile(w http.ResponseWriter, r *http.Request, c storageCall, p string) {
	f, err := os.Open(h.storageFilePath(c, p))
	if err != nil {
		storageError(w, 404, "file_not_found", "file not found")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		storageError(w, 404, "file_not_found", "file not found")
		return
	}
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
	_, _ = io.Copy(w, f)
}

func (h *SiteHandler) storageFileToken(c storageCall, p string, expires time.Time) string {
	payload := c.ownerID + "." + c.siteID + "." + c.resourceName + "." + base64.RawURLEncoding.EncodeToString([]byte(p)) + "." + strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, h.exportKey)
	mac.Write([]byte("site storage file v1\x00"))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (h *SiteHandler) createStorageFileLink(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "files")
	if !ok || !h.storageOwner(w, c) {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || !storageObjectPath(req.Path) {
		storageError(w, 400, "invalid_path", "invalid path")
		return
	}
	if _, err := os.Stat(h.storageFilePath(c, req.Path)); err != nil {
		storageError(w, 404, "file_not_found", "file not found")
		return
	}
	expires := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	writeJSON(w, 200, map[string]any{"url": h.exportLinkBase() + "/v1/storage-download?token=" + url.QueryEscape(h.storageFileToken(c, req.Path, expires)), "expires_at": expires.UTC().Format(time.RFC3339)})
}
func (h *SiteHandler) downloadStorageFile(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	encoded, sig, ok := strings.Cut(token, ".")
	if !ok {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	raw, e := base64.RawURLEncoding.DecodeString(encoded)
	if e != nil {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	mac := hmac.New(sha256.New, h.exportKey)
	mac.Write([]byte("site storage file v1\x00"))
	mac.Write(raw)
	given, e := base64.RawURLEncoding.DecodeString(sig)
	if e != nil || !hmac.Equal(given, mac.Sum(nil)) {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	fields := strings.Split(string(raw), ".")
	if len(fields) != 5 {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	ownerID, siteID, name := fields[0], fields[1], fields[2]
	pBytes, e := base64.RawURLEncoding.DecodeString(fields[3])
	expiry, ee := strconv.ParseInt(fields[4], 10, 64)
	if e != nil || ee != nil || time.Now().Unix() > expiry || expiry > time.Now().Add(10*time.Minute).Unix()+1 || !storageObjectPath(string(pBytes)) {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	site, e := db.GetSiteByID(r.Context(), h.database, siteID)
	if e != nil || site.Deleted || site.UserID != ownerID || site.OwnerSuspended || site.SiteSuspended {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	c := storageCall{siteID: siteID, ownerID: ownerID, siteName: site.Name, resourceName: name}
	e = h.database.QueryRowContext(r.Context(), `SELECT kind,read_policy,write_policy,site_passcode FROM site_storage_resources WHERE site_id=$1 AND name=$2`, siteID, name).Scan(&c.resource.Kind, &c.resource.Read, &c.resource.Write, &c.resource.SitePasscode)
	if e != nil || c.resource.Kind != "files" {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	h.serveStorageFile(w, r, c, string(pBytes))
}
