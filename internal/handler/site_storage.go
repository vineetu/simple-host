package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

var storageNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type storageResource struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Read         string `json:"read"`
	Write        string `json:"write"`
	SitePasscode string `json:"site_passcode"`
}

type storageCall struct {
	siteID, ownerID, siteName, resourceName string
	owner                                   bool
	resource                                storageResource
}

func storageError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func (h *SiteHandler) storageSite(w http.ResponseWriter, r *http.Request) (storageCall, bool) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	if name == "" {
		storageError(w, 400, "invalid_site", "site name is required")
		return storageCall{}, false
	}
	id, err := h.resolveWriteSiteID(r, name)
	if err != nil {
		storageError(w, 404, "site_not_found", "site not found")
		return storageCall{}, false
	}
	_, ownerID, siteName, err := db.GetSiteOwner(r.Context(), h.database, id)
	if err != nil {
		storageError(w, 404, "site_not_found", "site not found")
		return storageCall{}, false
	}
	if h.refuseSuspendedSiteID(w, r, id) {
		return storageCall{}, false
	}
	c := storageCall{siteID: id, ownerID: ownerID, siteName: siteName}
	if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, e := h.resolveWriterKey(r.Context(), key)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return storageCall{}, false
		}
		if ok && u.KeyScope != db.KeyScopeDeploy && u.Team == nil && u.EventWebsite == nil && (u.ID == ownerID || u.IsAdmin) {
			c.owner = true
		}
		if !c.owner {
			storageError(w, 403, "forbidden", "owner credential required")
			return storageCall{}, false
		}
	} else if !h.storageSameSiteRequest(r, id) {
		storageError(w, 403, "forbidden", "use this site's own address")
		return storageCall{}, false
	}
	if !c.owner && h.refuseOffline(w, r, id) {
		return storageCall{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return c, true
}

// Browser storage is limited to the site's own address; direct unauthenticated
// reads are allowed only when the host itself resolves to that site.
func (h *SiteHandler) storageSameSiteRequest(r *http.Request, siteID string) bool {
	host := requestHostName(r)
	if h.isVisitorApexHost(host) || strings.EqualFold(host, h.contentHost) {
		return false
	}
	id, err := h.resolveSiteIDScoped(r, r.PathValue("sitename"))
	if err != nil || id != siteID {
		return false
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" {
		return sameOriginRequest(r)
	}
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

func (h *SiteHandler) storageOwner(w http.ResponseWriter, c storageCall) bool {
	if c.owner {
		return true
	}
	storageError(w, 403, "forbidden", "owner credential required")
	return false
}

func (h *SiteHandler) storageAccess(w http.ResponseWriter, r *http.Request, c storageCall, write bool) bool {
	if c.owner {
		return true
	}
	if c.resource.SitePasscode == "inherit" && !h.PasscodeLetsIn(r, c.siteID) {
		storageError(w, 403, "site_locked", "unlock this site first")
		return false
	}
	policy := c.resource.Read
	if write {
		policy = c.resource.Write
	}
	if policy == "owner" {
		storageError(w, 403, "forbidden", "owner access required")
		return false
	}
	if write && r.Header.Get("X-SH-CSRF") != "1" {
		storageError(w, 403, "csrf_required", "X-SH-CSRF: 1 required")
		return false
	}
	if policy == "signed-in" {
		sess, ok := h.strictVisitorSession(r, c.siteID)
		if !ok {
			storageError(w, 401, "sign_in_required", "sign in to this site first")
			return false
		}
		suspended, err := db.UserSuspended(r.Context(), h.database, sess.UserID)
		if err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return false
		}
		if suspended {
			writeAccountSuspended(w)
			return false
		}
		_ = db.TouchVisitorSession(r.Context(), h.database, sess.ID)
		return true
	}
	return true
}

func (h *SiteHandler) storageResourceFor(w http.ResponseWriter, r *http.Request, kind string) (storageCall, bool) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return c, false
	}
	c.resourceName = r.PathValue("name")
	if !storageNameRE.MatchString(c.resourceName) {
		storageError(w, 400, "invalid_resource", "invalid resource name")
		return c, false
	}
	err := h.database.QueryRowContext(r.Context(), `SELECT name,kind,read_policy,write_policy,site_passcode FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.siteID, c.resourceName).Scan(&c.resource.Name, &c.resource.Kind, &c.resource.Read, &c.resource.Write, &c.resource.SitePasscode)
	if errors.Is(err, sql.ErrNoRows) {
		storageError(w, 404, "resource_not_found", "resource not found")
		return c, false
	}
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return c, false
	}
	if kind != "" && c.resource.Kind != kind {
		if !c.owner {
			storageError(w, 404, "resource_not_found", "resource not found")
			return c, false
		}
		storageError(w, 409, "resource_kind_conflict", "resource has another kind")
		return c, false
	}
	return c, true
}

func (h *SiteHandler) listStorageResources(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	rows, err := h.database.QueryContext(r.Context(), `SELECT name,kind,read_policy,write_policy,site_passcode FROM site_storage_resources WHERE site_id=$1 ORDER BY name`, c.siteID)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	defer rows.Close()
	out := []storageResource{}
	for rows.Next() {
		var x storageResource
		if rows.Scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.SitePasscode) != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		out = append(out, x)
	}
	if rows.Err() != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	writeJSON(w, 200, map[string]any{"resources": out})
}

func (h *SiteHandler) putStorageResource(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	name := r.PathValue("name")
	if !storageNameRE.MatchString(name) {
		storageError(w, 400, "invalid_resource", "invalid resource name")
		return
	}
	var x storageResource
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&x)
	if err != nil {
		storageError(w, 400, "invalid_request", "invalid JSON body")
		return
	}
	x.Name = name
	if x.Read == "" {
		x.Read = "owner"
	}
	if x.Write == "" {
		x.Write = "owner"
	}
	if x.SitePasscode == "" {
		x.SitePasscode = "inherit"
	}
	if x.Kind != "kv" && x.Kind != "sqlite" && x.Kind != "files" || !validStoragePolicy(x.Read) || !validStoragePolicy(x.Write) || x.SitePasscode != "inherit" && x.SitePasscode != "off" {
		storageError(w, 400, "invalid_resource", "invalid kind or policy")
		return
	}
	var oldKind string
	e := h.database.QueryRowContext(r.Context(), `SELECT kind FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.siteID, name).Scan(&oldKind)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if oldKind != "" && oldKind != x.Kind {
		storageError(w, 409, "resource_kind_conflict", "resource kind cannot change")
		return
	}
	result, e := h.database.ExecContext(r.Context(), `INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,site_passcode) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(site_id,name) DO UPDATE SET read_policy=EXCLUDED.read_policy,write_policy=EXCLUDED.write_policy,site_passcode=EXCLUDED.site_passcode WHERE site_storage_resources.kind=EXCLUDED.kind`, c.siteID, name, x.Kind, x.Read, x.Write, x.SitePasscode)
	if e != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		storageError(w, 409, "resource_kind_conflict", "resource kind cannot change")
		return
	}
	status := 200
	if oldKind == "" {
		status = 201
	}
	writeJSON(w, status, x)
}
func validStoragePolicy(p string) bool { return p == "anyone" || p == "signed-in" || p == "owner" }

func storageLimitMB(env string, fallback int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(env), 10, 64); err == nil && v > 0 && v <= 4096 {
		return v << 20
	}
	return fallback << 20
}
func storageSiteLimitBytes() int64 { return storageLimitMB("SITE_STORAGE_MAX_MB", 200) }
func storageResultLimitBytes() int { return int(storageLimitMB("SITE_STORAGE_SQL_RESULT_MAX_MB", 1)) }

// Runtime usage counts the durable SQLite main files and raw objects. WAL is
// transient and checkpointed for export; its possible short-lived overhead is
// shown separately in the measured storage guide.
func (h *SiteHandler) storageRuntimeOtherBytes(c storageCall) int64 {
	var total int64
	selected := h.storageSQLPath(c)
	_ = filepath.WalkDir(h.storageRuntimeDir(c), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == selected || strings.HasSuffix(p, "-wal") || strings.HasSuffix(p, "-shm") {
			return nil
		}
		if fi, e := d.Info(); e == nil && fi.Mode().IsRegular() {
			total += fi.Size()
		}
		return nil
	})
	return total
}

func (h *SiteHandler) storageKVBytes(ctx context.Context, c storageCall) (int64, error) {
	var n int64
	err := h.database.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(value::text)),0) FROM site_storage_kv WHERE site_id=$1`, c.siteID).Scan(&n)
	return n, err
}

func (h *SiteHandler) deleteStorageResource(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	c, ok = h.storageResourceFor(w, r, "")
	if !ok {
		return
	}
	unlock := h.lockSite(c.ownerID, c.siteName)
	defer unlock()
	staged, restore, err := h.stageStorageRuntime(c)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		restore()
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if _, err = tx.ExecContext(r.Context(), `DELETE FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.siteID, c.resourceName); err != nil {
		_ = tx.Rollback()
		restore()
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if err = tx.Commit(); err != nil {
		restore()
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if staged != "" {
		if err = os.RemoveAll(staged); err != nil {
			storageError(w, 500, "internal_error", "resource removed but staging cleanup failed")
			return
		}
	}
	h.disk.MarkChanged()
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func storagePage(r *http.Request) (string, string, int, bool) {
	q := r.URL.Query()
	prefix := q.Get("prefix")
	after := q.Get("after")
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 500 {
			return "", "", 0, false
		}
		limit = n
	}
	return prefix, after, limit, true
}

func (h *SiteHandler) storageKV(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageResourceFor(w, r, "kv")
	if !ok {
		return
	}
	key := r.PathValue("key")
	if key == "" {
		if r.Method != http.MethodGet || !h.storageAccess(w, r, c, false) {
			return
		}
		h.storageKVList(w, r, c)
		return
	}
	if len(key) > 512 || strings.ContainsRune(key, 0) {
		storageError(w, 400, "invalid_key", "invalid key")
		return
	}
	write := r.Method != http.MethodGet
	if !h.storageAccess(w, r, c, write) {
		return
	}
	switch r.Method {
	case http.MethodGet:
		var raw json.RawMessage
		e := h.database.QueryRowContext(r.Context(), `SELECT value FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3`, c.siteID, c.resourceName, key).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			storageError(w, 404, "key_not_found", "key not found")
			return
		}
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		writeJSON(w, 200, map[string]any{"key": key, "value": raw})
	case http.MethodPut:
		unlock := h.lockSite(c.ownerID, c.siteName)
		defer unlock()
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil {
			storageError(w, 400, "invalid_value", "value too large")
			return
		}
		var v struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(body, &v) != nil || len(v.Value) == 0 || !json.Valid(v.Value) {
			storageError(w, 400, "invalid_value", "valid JSON value required")
			return
		}
		var used, old int64
		e = h.database.QueryRowContext(r.Context(), `SELECT COALESCE(sum(octet_length(value::text)),0), COALESCE(sum(CASE WHEN resource_name=$2 AND key=$3 THEN octet_length(value::text) ELSE 0 END),0) FROM site_storage_kv WHERE site_id=$1`, c.siteID, c.resourceName, key).Scan(&used, &old)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if used-old+int64(len(v.Value))+h.storageRuntimeOtherBytes(c) > storageSiteLimitBytes() {
			storageError(w, 507, "site_full", "site storage is full")
			return
		}
		_, e = h.database.ExecContext(r.Context(), `INSERT INTO site_storage_kv(site_id,resource_name,key,value) VALUES($1,$2,$3,$4) ON CONFLICT(site_id,resource_name,key) DO UPDATE SET value=EXCLUDED.value,updated_at=now()`, c.siteID, c.resourceName, key, v.Value)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		writeJSON(w, 200, map[string]any{"key": key, "value": v.Value})
	case http.MethodDelete:
		unlock := h.lockSite(c.ownerID, c.siteName)
		defer unlock()
		_, e := h.database.ExecContext(r.Context(), `DELETE FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3`, c.siteID, c.resourceName, key)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		writeJSON(w, 200, map[string]any{"deleted": true})
	}
}

func (h *SiteHandler) storageKVList(w http.ResponseWriter, r *http.Request, c storageCall) {
	prefix, after, limit, ok := storagePage(r)
	if !ok {
		storageError(w, 400, "invalid_limit", "invalid limit")
		return
	}
	rows, e := h.database.QueryContext(r.Context(), `SELECT key,value FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key LIKE $3 ESCAPE '\' AND key>$4 ORDER BY key LIMIT $5`, c.siteID, c.resourceName, escapeStorageLike(prefix)+"%", after, limit+1)
	if e != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	defer rows.Close()
	type item struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	out := []item{}
	for rows.Next() {
		var x item
		if rows.Scan(&x.Key, &x.Value) != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		out = append(out, x)
	}
	if rows.Err() != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	next := ""
	if len(out) > limit {
		next = out[limit-1].Key
		out = out[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": out, "next_after": next})
}
func escapeStorageLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
