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
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"

	sqlite3 "github.com/ncruces/go-sqlite3"
	"github.com/vsriram/simple-host/internal/config"
	"github.com/vsriram/simple-host/internal/db"
)

var storageNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

type storageResource struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Read         string `json:"read"`
	Write        string `json:"write"`
	SitePasscode string `json:"site_passcode"`
	WriteMode    string `json:"write_mode"`
}

type storageCall struct {
	siteID, ownerID, siteName, resourceName string
	owner                                   bool
	visitorID                               string
	visitorSession                          db.VisitorSession
	linkScope                               string
	resource                                storageResource
}

type eventStorageContextKey struct{}

type eventStorageScope struct{ siteID, ownerID, eventID, organiserID string }

func storageError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

// ServeEventWebsiteStorage is called only after the Hack handler has checked
// current organiser membership on the event. It maps the public event slug to
// the holding account's immutable event-ID site before using the shared routes.
func (h *SiteHandler) ServeEventWebsiteStorage(w http.ResponseWriter, r *http.Request, ev db.Event, organiserID string) {
	site, err := db.GetSiteByUser(r.Context(), h.database, ev.AccountID, ev.ID)
	if err != nil {
		storageError(w, 404, "site_not_found", "publish the custom event website first")
		return
	}
	// A private mux keeps the route mapping identical to team and Host storage.
	mux := http.NewServeMux()
	h.registerStorageRoutes(mux)
	prefix := "/v1/hack/events/" + ev.Slug + "/website/storage/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok || rest == "" {
		storageError(w, 404, "not_found", "storage route not found")
		return
	}
	copy := r.Clone(context.WithValue(r.Context(), eventStorageContextKey{}, eventStorageScope{site.ID, ev.AccountID, ev.ID, organiserID}))
	copy.URL.Path = "/v1/sites/" + ev.ID + "/storage/" + rest
	copy.URL.RawPath = ""
	mux.ServeHTTP(w, copy)
}

func (h *SiteHandler) storageSite(w http.ResponseWriter, r *http.Request) (storageCall, bool) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	if name == "" {
		storageError(w, 400, "invalid_site", "site name is required")
		return storageCall{}, false
	}
	var id string
	var err error
	if scope, ok := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); ok {
		id = scope.siteID
	} else {
		id, err = h.resolveWriteSiteID(r, name)
	}
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
	if scope, ok := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); ok {
		if !hackMode || scope.siteID != id || scope.ownerID != ownerID {
			storageError(w, 403, "forbidden", "event website scope required")
			return storageCall{}, false
		}
		c.owner = true
		c.linkScope = "event:" + scope.eventID + ":" + scope.organiserID
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, e := h.resolveWriterKey(r.Context(), key)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return storageCall{}, false
		}
		if ok && u.KeyScope != db.KeyScopeDeploy && (u.ID == ownerID || u.IsAdmin) &&
			(u.Team == nil || (hackMode && u.Team.AccountID == ownerID && strings.EqualFold(u.Team.TeamSlug, siteName))) &&
			(u.EventWebsite == nil || (hackMode && u.EventWebsite.EventID == siteName && u.EventWebsite.OrganiserID != "")) {
			c.owner = true
			if u.Team != nil {
				c.linkScope = "team:" + u.Team.TeamID + ":" + u.Team.MemberID
			} else if hackMode && u.IsAdmin {
				c.linkScope = "admin"
			}
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
	if policy == "signed-in" || policy == "own" || write && c.resource.Read == "own" {
		sess := c.visitorSession
		if c.visitorID == "" {
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
	err := h.database.QueryRowContext(r.Context(), `SELECT name,kind,read_policy,write_policy,site_passcode,write_mode FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.siteID, c.resourceName).Scan(&c.resource.Name, &c.resource.Kind, &c.resource.Read, &c.resource.Write, &c.resource.SitePasscode, &c.resource.WriteMode)
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
	if !c.owner {
		if storageVisitorWrite(r) && !h.storageIPLimiter.allow(c.siteID+"/"+clientIP(r)) {
			tooManyRequests(w)
			return c, false
		}
		if sess, ok := h.strictVisitorSession(r, c.siteID); ok && sess.UserID != "" {
			c.visitorSession = sess
			c.visitorID = sess.UserID
			if storageVisitorWrite(r) && !h.storageVisitorLimiter.allow(c.siteID+"/"+c.visitorID) {
				tooManyRequests(w)
				return c, false
			}
		}
	}
	return c, true
}

func (h *SiteHandler) listStorageResources(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	rows, err := h.database.QueryContext(r.Context(), `SELECT name,kind,read_policy,write_policy,site_passcode,write_mode FROM site_storage_resources WHERE site_id=$1 ORDER BY name`, c.siteID)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	defer rows.Close()
	out := []storageResource{}
	for rows.Next() {
		var x storageResource
		if rows.Scan(&x.Name, &x.Kind, &x.Read, &x.Write, &x.SitePasscode, &x.WriteMode) != nil {
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	err := dec.Decode(&x)
	if err != nil || dec.Decode(new(any)) != io.EOF {
		storageError(w, 400, "invalid_request", "invalid JSON body")
		return
	}
	x.Name = name
	unlock, ok := h.storageWriteLock(w, r, c, false)
	if !ok {
		return
	}
	defer unlock()
	var oldKind, oldRead, oldMode string
	e := h.database.QueryRowContext(r.Context(), `SELECT kind,read_policy,write_mode FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.siteID, name).Scan(&oldKind, &oldRead, &oldMode)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	if x.WriteMode == "" {
		x.WriteMode = oldMode
		if x.WriteMode == "" {
			x.WriteMode = "full"
		}
	}
	if x.Read == "" {
		x.Read = "owner"
	}
	if x.Write == "" {
		x.Write = "owner"
	}
	if x.SitePasscode == "" {
		x.SitePasscode = "inherit"
	}
	if x.Kind != "kv" && x.Kind != "sqlite" && x.Kind != "files" || !(validStoragePolicy(x.Read) || x.Read == "own") || !validStoragePolicy(x.Write) || x.SitePasscode != "inherit" && x.SitePasscode != "off" || (x.WriteMode != "full" && x.WriteMode != "add") || (x.Read == "own" && x.Write != "owner" && x.WriteMode != "add") {
		storageError(w, 400, "invalid_resource", "invalid kind or policy")
		return
	}
	if hackMode && (x.Read == "own" || x.WriteMode == "add") {
		storageError(w, 400, "invalid_resource", "own reads and add-only writes are Simple Host policies")
		return
	}
	if oldKind != "" && oldKind != x.Kind {
		storageError(w, 409, "resource_kind_conflict", "resource kind cannot change")
		return
	}
	if x.Kind == "sqlite" && x.Read == "own" {
		c.resourceName = name
		c.resource.Read = oldRead
		if err := h.validateStorageOwnDatabase(r.Context(), c); err != nil {
			if errors.Is(err, errStorageBusy) {
				storageBusy(w)
				return
			}
			if errors.Is(err, sqlite3.FULL) {
				storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
				return
			}
			storageError(w, 400, "visitor_id_required", "own reads require visitor_id TEXT in every table and an empty database when switching from another read policy")
			return
		}
	}
	result, e := h.database.ExecContext(r.Context(), `INSERT INTO site_storage_resources(site_id,name,kind,read_policy,write_policy,site_passcode,write_mode) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(site_id,name) DO UPDATE SET read_policy=EXCLUDED.read_policy,write_policy=EXCLUDED.write_policy,site_passcode=EXCLUDED.site_passcode,write_mode=EXCLUDED.write_mode WHERE site_storage_resources.kind=EXCLUDED.kind`, c.siteID, name, x.Kind, x.Read, x.Write, x.SitePasscode, x.WriteMode)
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
	unlock()
	writeJSON(w, status, x)
}
func validStoragePolicy(p string) bool { return p == "anyone" || p == "signed-in" || p == "owner" }

func storageLimitBytes(raw string, fallback, max int64) int64 {
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 && v <= max {
		return v
	}
	return fallback
}
func storageSiteLimitBytes() int64 {
	return storageLimitBytes(os.Getenv("SITE_STORAGE_MAX_BYTES"), 1000000, 10<<30)
}
func storageResultLimitBytes() int {
	return int(storageLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES"), 1000000, 64<<20))
}

type siteStorageUsage struct {
	KV, SQLite, Files int64
	FileObjects       map[string]int
}

func (u siteStorageUsage) data() int64  { return u.KV + u.SQLite }
func (u siteStorageUsage) total() int64 { return u.KV + u.SQLite + u.Files }

// The owner report and all three write paths use this same durable-byte
// definition: normalized JSONB value text, SQLite main files after checkpoint,
// and raw object bytes. WAL, retained deploys and legacy data are separate.
func (h *SiteHandler) measureSiteStorage(ctx context.Context, c storageCall) (siteStorageUsage, error) {
	u := siteStorageUsage{FileObjects: map[string]int{}}
	if err := h.database.QueryRowContext(ctx, `SELECT COALESCE(sum(octet_length(key)+octet_length(value::text)),0) FROM site_storage_kv WHERE site_id=$1`, c.siteID).Scan(&u.KV); err != nil {
		return u, err
	}
	root := h.storageRuntimeDir(c)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if os.IsNotExist(e) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") || !d.Type().IsRegular() {
			return nil
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		fi, e := d.Info()
		if e != nil {
			return e
		}
		if strings.HasPrefix(rel, "sqlite"+string(filepath.Separator)) && strings.HasSuffix(rel, ".sqlite") {
			u.SQLite += fi.Size()
		}
		if strings.HasPrefix(rel, "files"+string(filepath.Separator)) {
			u.Files += fi.Size()
			parts := strings.Split(rel, string(filepath.Separator))
			if len(parts) > 2 {
				u.FileObjects[parts[1]]++
			}
		}
		return nil
	})
	return u, err
}

func (h *SiteHandler) getStorageUsage(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	u, err := h.measureSiteStorage(r.Context(), c)
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	limit := storageSiteLimitBytes()
	remaining := limit - h.storageBudgetUsed(u)
	if remaining < 0 {
		remaining = 0
	}
	writeJSON(w, 200, map[string]any{"used_bytes": h.storageBudgetUsed(u), "limit_bytes": limit, "remaining_bytes": remaining, "files_used_bytes": u.Files, "files_limit_bytes": h.storageFilesLimitBytes(), "files_remaining_bytes": h.storageFilesRemaining(u), "breakdown": map[string]int64{"kv_bytes": u.KV, "sqlite_bytes": u.SQLite, "files_bytes": u.Files}})
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
	unlock, ok := h.storageWriteLock(w, r, c, true)
	if !ok {
		return
	}
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
	unlock()
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
	if r.Method == http.MethodPut {
		key = norm.NFC.String(key)
	}
	if key == "" {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || !h.storageAccess(w, r, c, false) {
			return
		}
		h.storageKVList(w, r, c)
		return
	}
	if len(key) > 512 || strings.ContainsRune(key, 0) {
		storageError(w, 400, "invalid_key", "invalid key")
		return
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	if !h.storageAccess(w, r, c, write) || !storageAddDeleteOK(w, r, c) {
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		var raw json.RawMessage
		e := h.database.QueryRowContext(r.Context(), `SELECT value FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3 AND ($4='' OR writer_id=$4)`, c.siteID, c.resourceName, key, c.ownReader()).Scan(&raw)
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
		unlock, ok := h.storageWriteLock(w, r, c, true)
		if !ok {
			return
		}
		defer unlock()
		usage, e := h.measureSiteStorage(r.Context(), c)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		var exists bool
		if err := h.database.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3)`, c.siteID, c.resourceName, key).Scan(&exists); err != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if c.addOnly() && exists {
			storageError(w, 409, "key_exists", "this key already exists")
			return
		}
		var old int64
		e = h.database.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT octet_length(key)+octet_length(value::text) FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3),0)`, c.siteID, c.resourceName, key).Scan(&old)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		var normalized int64
		e = h.database.QueryRowContext(r.Context(), `SELECT octet_length($1::jsonb::text)`, string(v.Value)).Scan(&normalized)
		if e != nil {
			storageError(w, 400, "invalid_value", "invalid JSON value")
			return
		}
		if h.storageBudgetUsed(usage)-old+normalized+int64(len(key)) > storageSiteLimitBytes() {
			storageError(w, 507, "site_full", "KV and SQLite storage is full; remove unused data before retrying")
			return
		}
		result, e := h.storageKVSave(r.Context(), c, key, v.Value)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		if n, err := result.RowsAffected(); err != nil || n == 0 {
			storageError(w, 409, "key_exists", "this key already exists")
			return
		}
		unlock()
		writeJSON(w, 200, map[string]any{"key": key, "value": v.Value})
	case http.MethodDelete:
		unlock, ok := h.storageWriteLock(w, r, c, true)
		if !ok {
			return
		}
		defer unlock()
		_, e := h.database.ExecContext(r.Context(), `DELETE FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key=$3`, c.siteID, c.resourceName, key)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		unlock()
		writeJSON(w, 200, map[string]any{"deleted": true})
	}
}

func (h *SiteHandler) storageKVList(w http.ResponseWriter, r *http.Request, c storageCall) {
	prefix, after, limit, ok := storagePage(r)
	if !ok {
		storageError(w, 400, "invalid_limit", "invalid limit")
		return
	}
	rows, e := h.database.QueryContext(r.Context(), `SELECT key,value FROM site_storage_kv WHERE site_id=$1 AND resource_name=$2 AND key LIKE $3 ESCAPE '\' AND key>$4 AND ($6='' OR writer_id=$6) ORDER BY key LIMIT $5`, c.siteID, c.resourceName, escapeStorageLike(prefix)+"%", after, limit+1, c.ownReader())
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

// Bodies are consumed before this bounded write/quota critical section. Reads
// never use the deploy mutex. Recheck policy after waiting for a concurrent PUT.
func (h *SiteHandler) storageWriteLock(w http.ResponseWriter, r *http.Request, c storageCall, recheck bool) (func(), bool) {
	ctx, cancel := context.WithTimeout(r.Context(), config.Active().StorageWriteLockWait)
	defer cancel()
	mu, _ := h.uploadLocks.LoadOrStore(c.ownerID+"/"+c.siteName, &sync.Mutex{})
	m := mu.(*sync.Mutex)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !m.TryLock() {
		select {
		case <-ctx.Done():
			storageBusy(w)
			return nil, false
		case <-tick.C:
		}
	}
	if recheck {
		var current storageResource
		err := h.database.QueryRowContext(ctx, `SELECT name,kind,read_policy,write_policy,site_passcode,write_mode FROM site_storage_resources WHERE site_id=$1 AND name=$2`, c.siteID, c.resourceName).Scan(&current.Name, &current.Kind, &current.Read, &current.Write, &current.SitePasscode, &current.WriteMode)
		if err != nil || current != c.resource {
			m.Unlock()
			storageError(w, 409, "resource_changed", "resource changed; retry")
			return nil, false
		}
	}
	timeout := config.Active().StorageVisitorWriteTimeout
	if c.owner {
		timeout = config.Active().StorageOwnerTimeout
	}
	writeCtx, writeCancel := context.WithTimeout(r.Context(), timeout)
	*r = *r.WithContext(writeCtx)
	var once sync.Once
	return func() { once.Do(func() { writeCancel(); m.Unlock() }) }, true
}

// PostgreSQL enforces create-only even when independent processes race.
func (h *SiteHandler) storageKVSave(ctx context.Context, c storageCall, key string, value json.RawMessage) (sql.Result, error) {
	conflict := `DO UPDATE SET value=EXCLUDED.value,updated_at=now(),writer_id=CASE WHEN $6 THEN site_storage_kv.writer_id ELSE EXCLUDED.writer_id END`
	args := []any{c.siteID, c.resourceName, key, value, c.visitorID, c.owner}
	if c.addOnly() {
		conflict = "DO NOTHING"
		args = args[:5]
	}
	return h.database.ExecContext(ctx, `INSERT INTO site_storage_kv(site_id,resource_name,key,value,writer_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT(site_id,resource_name,key) `+conflict, args...)
}

// POST query and download-link routes only read; method alone is insufficient.
func storageVisitorWrite(r *http.Request) bool {
	return r.Method == http.MethodPut || r.Method == http.MethodDelete ||
		r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/query") && !strings.HasSuffix(r.URL.Path, "/download-link")
}
func storageBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	storageError(w, 503, "storage_busy", "storage is busy; retry shortly")
}
