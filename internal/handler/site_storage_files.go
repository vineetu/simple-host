package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
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

	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
	"golang.org/x/text/unicode/norm"
)

func storageFileMaxBytes() int64 {
	return storageLimitBytes(os.Getenv("SITE_STORAGE_FILE_MAX_BYTES"), 1000000, 10<<30)
}

func storageObjectPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsRune(p, 0) || len(p) > 1024 || path.Clean(p) != p || len(strings.Split(p, "/")) > 16 {
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
	if r.Method == http.MethodPut {
		p = norm.NFC.String(p)
	}
	if p == "" {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !h.storageAccess(w, r, &c, "read") {
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
		if !h.storageAccess(w, r, &c, "read") {
			return
		}
		h.serveStorageFile(w, r, c, p)
	case http.MethodPut:
		// Add or edit is decided inside the write lock by whether the path
		// exists; the page cannot choose.
		if !h.storageGate(w, r, c, true) {
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, storageFileMaxBytes()))
		if err != nil {
			storageError(w, 400, "invalid_file", "file too large; shrink photos to about 1600 px WebP")
			return
		}
		unlock, ok := h.storageWriteLock(w, r, c, true)
		if !ok {
			return
		}
		defer unlock()
		dest := h.storageFilePath(c, p)
		oldBytes := int64(0)
		existed := false
		if fi, e := os.Lstat(dest); e == nil {
			if !fi.Mode().IsRegular() {
				storageError(w, 409, "file_exists", "this path already exists")
				return
			}
			oldBytes = fi.Size()
			existed = true
		} else if !errors.Is(e, os.ErrNotExist) {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		c.currentMatrix = c.resource.matrix()
		action := "add"
		if existed {
			action = "edit"
			if c.storageTaken(c.currentMatrix) {
				storageError(w, 409, "file_exists", "this path already exists")
				return
			}
		}
		filter, ok := h.storageAllow(w, r, &c, action, c.currentMatrix.value(action))
		if !ok {
			return
		}
		if existed && filter != "" {
			var writer string
			e := h.database.QueryRowContext(r.Context(), `SELECT writer_id FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3`, c.siteID, c.resourceName, p).Scan(&writer)
			if e != nil && !errors.Is(e, sql.ErrNoRows) {
				storageError(w, 500, "internal_error", "internal server error")
				return
			}
			if writer != filter {
				storageError(w, 409, "file_exists", "this path already exists")
				return
			}
		}
		usage, err := h.measureSiteStorage(r.Context(), c)
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if !existed && usage.FileObjects[c.resourceName] >= config.Active().StorageFileObjects {
			storageError(w, 507, "bucket_full", "file bucket has too many objects; remove unused files")
			return
		}
		used := usage.Files
		if hackMode {
			used = usage.total()
		}
		if used-oldBytes+int64(len(body)) > h.storageFilesLimitBytes() {
			storageError(w, 507, "site_full", "file storage is full; shrink photos to about 1600 px WebP and remove unused files")
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
			// Record identity before publishing. An edit keeps the original
			// author; an add records who added it.
			if existed && hackMode && !c.owner {
				// Simple Hack keeps its earlier attribution: a visitor's
				// overwrite records that visitor.
				_, err = h.database.ExecContext(r.Context(), `INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) VALUES($1,$2,$3,$4) ON CONFLICT(site_id,resource_name,path) DO UPDATE SET writer_id=EXCLUDED.writer_id`, c.siteID, c.resourceName, p, c.stampID())
				if err == nil {
					err = os.Rename(f.Name(), dest)
				}
			} else if existed {
				_, err = h.database.ExecContext(r.Context(), `INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) VALUES($1,$2,$3,'') ON CONFLICT(site_id,resource_name,path) DO NOTHING`, c.siteID, c.resourceName, p)
				if err == nil {
					err = os.Rename(f.Name(), dest)
				}
			} else if err = os.Link(f.Name(), dest); errors.Is(err, os.ErrExist) {
				// Create-only publish: a path that appeared meanwhile is not
				// replaced. Until its row is written an own reader cannot see it.
				storageError(w, 409, "file_exists", "this path already exists")
				return
			} else if err == nil {
				_, err = h.database.ExecContext(r.Context(), `INSERT INTO site_storage_files(site_id,resource_name,path,writer_id) VALUES($1,$2,$3,$4) ON CONFLICT(site_id,resource_name,path) DO UPDATE SET writer_id=EXCLUDED.writer_id, created_at=now()`, c.siteID, c.resourceName, p, c.stampID())
				if err != nil {
					_ = os.Remove(dest)
				}
			}
		}
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		h.disk.MarkChanged()
		unlock()
		writeJSON(w, 200, map[string]any{"path": p, "bytes": len(body), "content_type": http.DetectContentType(body)})
	case http.MethodDelete:
		if !h.storageAccess(w, r, &c, "delete") {
			return
		}
		unlock, ok := h.storageWriteLock(w, r, c, true)
		if !ok {
			return
		}
		defer unlock()
		if c.filter != "" {
			var mine bool
			if err := h.database.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3 AND writer_id=$4)`, c.siteID, c.resourceName, p, c.filter).Scan(&mine); err != nil {
				storageError(w, 500, "internal_error", "internal server error")
				return
			}
			if !mine {
				storageError(w, 404, "file_not_found", "file not found")
				return
			}
		}
		if err := os.Remove(h.storageFilePath(c, p)); err != nil && !errors.Is(err, os.ErrNotExist) {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if _, err := h.database.ExecContext(r.Context(), `DELETE FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3`, c.siteID, c.resourceName, p); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		h.disk.MarkChanged()
		unlock()
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
		// VisitorID: who uploaded it, for the owner only (empty: the owner).
		VisitorID string `json:"visitor_id,omitempty"`
		// Mine: a signed-in visitor uploaded this file (visitors only).
		Mine *bool `json:"mine,omitempty"`
	}
	out := []item{}
	if c.filter != "" {
		rows, e := h.database.QueryContext(r.Context(), `SELECT path FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND writer_id=$3 AND path>$4 AND path LIKE $5 ESCAPE '\' ORDER BY path LIMIT $6`, c.siteID, c.resourceName, c.filter, after, escapeStorageLike(prefix)+"%", limit+1)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		defer rows.Close()
		scanned, next := 0, ""
		hasMore := false
		for rows.Next() {
			var p string
			if rows.Scan(&p) != nil {
				storageError(w, 500, "internal_error", "internal server error")
				return
			}
			if scanned == limit {
				hasMore = true
				break
			}
			scanned++
			next = p
			if !storageObjectPath(p) {
				continue
			}
			f, e := os.Open(h.storageFilePath(c, p))
			if os.IsNotExist(e) {
				continue
			}
			if e != nil {
				storageError(w, 500, "internal_error", "internal server error")
				return
			}
			fi, e := f.Stat()
			if e != nil || !fi.Mode().IsRegular() {
				f.Close()
				continue
			}
			var b [512]byte
			n, _ := f.Read(b[:])
			f.Close()
			mine := true
			out = append(out, item{Path: p, Bytes: fi.Size(), ContentType: http.DetectContentType(b[:n]), Mine: &mine})
		}
		if rows.Err() != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if !hasMore {
			next = ""
		}
		writeJSON(w, 200, map[string]any{"items": out, "next_after": next})
		return
	}
	root := h.storageFilesDir(c)
	err := filepath.WalkDir(root, func(fp string, d os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
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
		if !storageObjectPath(p) {
			return nil
		}
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
		out = append(out, item{Path: p, Bytes: fi.Size(), ContentType: http.DetectContentType(b[:n])})
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
	// The owner sees who uploaded each file (storage_visitors.go turns the
	// id into an email).
	if (c.owner || c.ownerOnSite || c.visitorID != "") && len(out) > 0 {
		paths := make([]string, len(out))
		for i := range out {
			paths[i] = out[i].Path
		}
		writers, e := h.storageFileWriters(r.Context(), c, paths)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		for i := range out {
			out[i].VisitorID = writers[out[i].Path]
			if !c.owner && !c.ownerOnSite {
				mine := out[i].VisitorID != "" && out[i].VisitorID == c.visitorID
				out[i].VisitorID = ""
				if !hackMode {
					out[i].Mine = &mine
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"items": out, "next_after": next})
}

func (h *SiteHandler) serveStorageFile(w http.ResponseWriter, r *http.Request, c storageCall, p string) {
	if c.filter != "" {
		var allowed bool
		err := h.database.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM site_storage_files WHERE site_id=$1 AND resource_name=$2 AND path=$3 AND writer_id=$4)`, c.siteID, c.resourceName, p, c.filter).Scan(&allowed)
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if !allowed {
			storageError(w, 404, "file_not_found", "file not found")
			return
		}
	}
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
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, f)
	}
}

func (h *SiteHandler) storageFileToken(c storageCall, p string, expires time.Time) string {
	payload := c.ownerID + "." + c.siteID + "." + c.resourceName + "." + base64.RawURLEncoding.EncodeToString([]byte(p)) + "." + strconv.FormatInt(expires.Unix(), 10)
	if c.linkScope != "" {
		payload += "." + base64.RawURLEncoding.EncodeToString([]byte(c.linkScope))
	}
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
	if len(fields) != 5 && len(fields) != 6 {
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
	if len(fields) == 6 {
		claim, err := base64.RawURLEncoding.DecodeString(fields[5])
		if err != nil || !h.storageLinkScopeCurrent(r, site, string(claim)) {
			storageError(w, 404, "file_link_invalid", "link invalid")
			return
		}
	} else if hackMode {
		// Legacy unscoped owner links cannot stand in for a current team or
		// organiser credential on the hosted event platform.
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	c := storageCall{siteID: siteID, ownerID: ownerID, siteName: site.Name, resourceName: name, owner: true}
	c.resource, e = h.loadStorageResource(r.Context(), siteID, name)
	if e != nil || c.resource.Kind != "files" {
		storageError(w, 404, "file_link_invalid", "link invalid")
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	h.serveStorageFile(w, r, c, string(pBytes))
}

func (h *SiteHandler) storageLinkScopeCurrent(r *http.Request, site db.Site, scope string) bool {
	if scope == "admin" {
		return true
	}
	kind, rest, ok := strings.Cut(scope, ":")
	if !ok {
		return false
	}
	id, memberID, ok := strings.Cut(rest, ":")
	if !ok || id == "" || memberID == "" {
		return false
	}
	switch kind {
	case "team":
		team, err := db.ResolveTeamIdentity(r.Context(), h.database, memberID, id)
		return err == nil && team.AccountID == site.UserID && team.TeamSlug == site.Name
	case "event":
		ev, err := db.GetEventByAccount(r.Context(), h.database, site.UserID)
		if err != nil || ev.ID != id || site.Name != ev.ID || ev.TakenDown() {
			return false
		}
		member, err := db.GetEventMember(r.Context(), h.database, ev.ID, memberID)
		return err == nil && member.Role == "organiser"
	}
	return false
}
