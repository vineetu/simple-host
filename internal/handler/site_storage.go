package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	rules "github.com/vsriram/simple-host/internal/rules/storage"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
	"github.com/vsriram/simple-host/internal/db"
	repostorage "github.com/vsriram/simple-host/internal/repository/storage"
)

var storageNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// storageResource is one KV namespace, SQLite database or file bucket. Read,
// Write and WriteMode are the older fields; Access
// is the preset matrix, nil on a resource saved before presets (legacy
// rules, storageLegacyMatrix). Tables are SQLite per-table matrices.
type storageResource struct {
	Name         string
	Kind         string
	Read         string
	Write        string
	SitePasscode string
	WriteMode    string
	Access       *storageAccessMatrix
	PresetBase   string
	Tables       map[string]storageTableRule
	// ApproveFirst: SQLite posts added through the spam check (add
	// checked) stay hidden from visitors until the owner approves them.
	ApproveFirst bool
}

type storageCall struct {
	siteID, ownerID, siteName, resourceName string
	accountID                               string
	owner                                   bool
	visitorAI                               bool
	visitorID                               string
	visitorSession                          db.VisitorSession
	linkScope                               string
	resource                                storageResource
	// ownerOnSite: the site's owner signed in on the site's own address
	// (storageOwnerOnSite); owner rights for storage data only.
	ownerOnSite   bool
	activeChecked bool
	currentMatrix storageAccessMatrix
	// filter: "" or the visitor id every entry must carry for the action
	// storageAccess allowed (own).
	filter string
	// spamCheck: the spam check's outcome for this request ("" none sent,
	// "ok", or a refusal code); checkedPost: the add was allowed by it, so
	// the post limits apply (turnstile.go).
	spamCheck   string
	checkedPost bool
	// uploaderID is the person behind an owner write when the credential
	// acts for someone (a Simple Hack team member or organiser), else the
	// account itself. Image matching names them (storageUploader).
	uploaderID string
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
	h.paperHackSite(r, ownerID, siteName, nil)
	c := storageCall{siteID: id, ownerID: ownerID, siteName: siteName}
	if scope, ok := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); ok {
		if !h.ed.Storage().EventOwners || scope.siteID != id || scope.ownerID != ownerID {
			storageError(w, 403, "forbidden", "event website scope required")
			return storageCall{}, false
		}
		c.owner = true
		c.accountID = scope.organiserID
		c.uploaderID = scope.organiserID
		c.linkScope = "event:" + scope.eventID + ":" + scope.organiserID
	} else if r.Header.Get(visitorPassHeader) != "" {
		if _, ok := h.internalVisitorSession(r, id); !ok {
			storageError(w, 403, "forbidden", "visitor pass does not cover this site")
			return storageCall{}, false
		}
		c.visitorAI = true
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, e := h.resolveWriterKey(r.Context(), key)
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return storageCall{}, false
		}
		if ok && u.KeyScope != db.KeyScopeDeploy && (u.ID == ownerID || u.IsAdmin) &&
			(u.Team == nil || (h.ed.Storage().EventOwners && u.Team.AccountID == ownerID && strings.EqualFold(u.Team.TeamSlug, siteName))) &&
			(u.EventWebsite == nil || (h.ed.Storage().EventOwners && u.EventWebsite.EventID == siteName && u.EventWebsite.OrganiserID != "")) {
			c.owner = true
			c.accountID = u.ID
			c.uploaderID = siteWriterID(&u)
			if u.Team != nil {
				c.linkScope = "team:" + u.Team.TeamID + ":" + u.Team.MemberID
			} else if h.ed.Storage().EventOwners && u.IsAdmin {
				c.linkScope = "admin"
			}
		}
		hackSitesPaperUser(r, &u)
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
	var err error
	c.resource, err = h.loadStorageResource(r.Context(), c.siteID, c.resourceName)
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
			c.ownerOnSite = h.storageOwnerOnSite(r, c)
		}
		if !h.storageSpamCheck(w, r, &c) {
			return c, false
		}
	}
	return c, true
}

func (h *SiteHandler) listStorageResources(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	out, err := h.storageSvc().ListStorageResources(r.Context(), h.storageServiceCall(c))
	if err != nil {
		var serviceErr *appstorage.Error
		if errors.As(err, &serviceErr) {
			storageError(w, 403, serviceErr.Code, serviceErr.Message)
			return
		}
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
	var req struct {
		Kind string `json:"kind"`
		rules.AccessRequest
		Write        string                          `json:"write"`
		WriteMode    string                          `json:"write_mode"`
		SitePasscode string                          `json:"site_passcode"`
		Tables       *map[string]rules.AccessRequest `json:"tables"`
		ApproveFirst *bool                           `json:"approve_first"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	err := dec.Decode(&req)
	if err != nil || dec.Decode(new(any)) != io.EOF {
		storageError(w, 400, "invalid_request", "invalid JSON body")
		return
	}
	call := h.storageServiceCall(c)
	result, err := h.storageRequestSvc(r, c).PutStorageResource(r.Context(), call, appstorage.ResourceInput{
		Name: name, Kind: req.Kind, AccessRequest: req.AccessRequest, Write: req.Write, WriteMode: req.WriteMode, SitePasscode: req.SitePasscode, Tables: req.Tables, ApproveFirst: req.ApproveFirst,
	})
	if err != nil {
		e := err.(*appstorage.Error)
		if e.Rule != "" {
			storageAccessError(w, e.Rule, e.Message)
			return
		}
		switch e.Code {
		case "storage_busy":
			storageBusy(w)
		case "resource_kind_conflict", "tables_present":
			storageError(w, 409, e.Code, e.Message)
		case "site_full":
			storageError(w, 507, e.Code, e.Message)
		case "internal_error":
			storageError(w, 500, e.Code, e.Message)
		default:
			storageError(w, 400, e.Code, e.Message)
		}
		return
	}
	status := 200
	if result.Created {
		status = 201
	}
	writeJSON(w, status, result.Body)
}

// storageAccessError refuses a matrix that breaks a rule (R1 to R4) or names
// an unknown value.
func storageAccessError(w http.ResponseWriter, rule, hint string) {
	code := "invalid_access"
	if rule == "invalid_value" || rule == "invalid_preset" {
		code = "invalid_resource"
	}
	writeJSON(w, 400, map[string]any{"error": hint, "code": code, "rule": rule})
}

func (h *SiteHandler) listStorageResourceRows(ctx context.Context, siteID string) ([]storageResource, error) {
	resources, err := repostorage.ListResources(ctx, h.database, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]storageResource, 0, len(resources))
	for _, resource := range resources {
		out = append(out, fromRulesResource(resource))
	}
	return out, nil
}

func validStoragePolicy(p string) bool { return p == "anyone" || p == "signed-in" || p == "owner" }

func storageLimitBytes(raw string, fallback, max int64) int64 {
	return appstorage.LimitBytes(raw, fallback, max)
}
func storageSiteLimitBytes(combined bool) int64 {
	return appstorage.SiteLimitBytes(os.Getenv("SITE_STORAGE_MAX_BYTES"), combined)
}
func storageResultLimitBytes() int {
	return appstorage.ResultLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES"))
}

type siteStorageUsage = appstorage.Usage

func (h *SiteHandler) measureSiteStorage(ctx context.Context, c storageCall) (siteStorageUsage, error) {
	return h.storageSvc().MeasureSiteStorage(ctx, c.siteID, h.storageRuntimeDir(c))
}

func (h *SiteHandler) getStorageUsage(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	result, err := h.storageSvc().GetStorageUsage(r.Context(), h.storageServiceCall(c))
	if err != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	writeJSON(w, 200, result)
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
	if err := h.storageRequestSvc(r, c).DeleteStorageResource(r.Context(), h.storageServiceCall(c)); err != nil {
		e := err.(*appstorage.Error)
		switch e.Code {
		case "storage_busy":
			storageBusy(w)
		case "resource_changed":
			storageError(w, 409, e.Code, e.Message)
		default:
			storageError(w, 500, e.Code, e.Message)
		}
		return
	}
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
		key = appstorage.NormalizeKVKey(key)
	}
	if key == "" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			return
		}
		h.storageKVList(w, r, c)
		return
	}
	if len(key) > 512 || strings.ContainsRune(key, 0) {
		storageError(w, 400, "invalid_key", "invalid key")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		result, e := h.storageRequestSvc(r, c).GetKV(r.Context(), appstorage.KVReadInput{Call: call, Key: key})
		if auth := result.Authorization; auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, http.StatusForbidden, auth.Code, auth.Message)
				return
			}
			writeStorageRefusals(w, auth.Refusals)
			if !auth.Allowed {
				return
			}
		}
		item := result.Item
		var serviceErr *appstorage.Error
		if errors.As(e, &serviceErr) && serviceErr.Code == "key_not_found" {
			storageError(w, 404, serviceErr.Code, serviceErr.Message)
			return
		}
		if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return
		}
		resp := map[string]any{"key": key, "value": item.Value}
		if item.VisitorID != "" {
			resp["visitor_id"] = item.VisitorID
		}
		if item.Mine != nil {
			resp["mine"] = *item.Mine
		}
		writeJSON(w, 200, resp)
	case http.MethodPut:
		// Add or edit is decided below, inside the write lock, by whether
		// the key exists; the page cannot choose.
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		reader := http.MaxBytesReader(w, r.Body, 1<<20)
		value, auth, e := h.storageRequestSvc(r, c).PutKV(r.Context(), appstorage.KVPutInput{Call: call, Key: key, Requested: r.Header.Get("X-SH-Visitor-Action"), Body: reader})
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
		if e != nil {
			var serviceErr *appstorage.Error
			if errors.As(e, &serviceErr) {
				status := 400
				if serviceErr.Code == "storage_busy" {
					storageBusy(w)
					return
				}
				if serviceErr.Code == "resource_changed" {
					status = 409
				} else if serviceErr.Code == "key_not_found" {
					status = 404
				} else if serviceErr.Code == "post_too_large" {
					status = 413
				} else if serviceErr.Code == "site_full" {
					status = 507
				} else if serviceErr.Code == "key_exists" {
					status = 409
				}
				storageError(w, status, serviceErr.Code, serviceErr.Message)
			} else {
				storageError(w, 500, "internal_error", "internal server error")
			}
			return
		}
		writeJSON(w, 200, map[string]any{"key": key, "value": value})
	case http.MethodDelete:
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		auth, e := h.storageRequestSvc(r, c).DeleteKV(r.Context(), appstorage.KVDeleteInput{Call: call, Key: key})
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
		if e != nil {
			var serviceErr *appstorage.Error
			if errors.As(e, &serviceErr) {
				switch serviceErr.Code {
				case "storage_busy":
					storageBusy(w)
				case "resource_changed":
					storageError(w, 409, serviceErr.Code, serviceErr.Message)
				case "key_not_found":
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

// stampID is who a new entry belongs to: the signed-in visitor (the owner
// signed in on the site included), or "" for the owner's tools and for
// someone not signed in.
func (c storageCall) stampID() string {
	return appstorage.StampID(c.owner, c.visitorID)
}

func (h *SiteHandler) storageKVList(w http.ResponseWriter, r *http.Request, c storageCall) {
	prefix, after, limit, ok := storagePage(r)
	call := h.storageServiceCall(c)
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	result, e := h.storageRequestSvc(r, c).ListKV(r.Context(), appstorage.KVListInput{Call: call, Prefix: prefix, After: after, Limit: limit, ValidLimit: ok})
	if auth := result.Authorization; auth != nil {
		if len(auth.Refusals) == 0 {
			storageError(w, http.StatusForbidden, auth.Code, auth.Message)
			return
		}
		writeStorageRefusals(w, auth.Refusals)
		if !auth.Allowed {
			return
		}
	}
	var serviceErr *appstorage.Error
	if errors.As(e, &serviceErr) && serviceErr.Code == "invalid_limit" {
		storageError(w, 400, serviceErr.Code, serviceErr.Message)
		return
	}
	if e != nil {
		storageError(w, 500, "internal_error", "internal server error")
		return
	}
	rows, next := result.Items, result.Next
	type item struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
		// VisitorID: who wrote it, for the owner only (empty: the owner).
		VisitorID string `json:"visitor_id,omitempty"`
		// Mine: a visitor added this entry (visitors only).
		Mine *bool `json:"mine,omitempty"`
	}
	out := []item{}
	for _, row := range rows {
		x := item{Key: row.Key, Value: row.Value, VisitorID: row.VisitorID, Mine: row.Mine}
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"items": out, "next_after": next})
}
func escapeStorageLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Bodies are consumed before this bounded write/quota critical section. Reads
// never use the deploy mutex. Recheck policy after waiting for a concurrent PUT.
func (h *SiteHandler) storageWriteLock(w http.ResponseWriter, r *http.Request, c storageCall, recheck bool) (func(), bool) {
	var check func(context.Context) bool
	if recheck {
		check = func(ctx context.Context) bool {
			current, err := h.loadStorageResource(ctx, c.siteID, c.resourceName)
			return err == nil && current.fingerprint() == c.resource.fingerprint()
		}
	}
	timeout := h.limits.StorageVisitorWriteTimeout
	if c.owner {
		timeout = h.limits.StorageOwnerTimeout
	}
	ctx, unlock, err := h.storageSvc().WriteLock(r.Context(), c.ownerID+"/"+c.siteName, h.limits.StorageWriteLockWait, timeout, check)
	if err != nil {
		if e, ok := err.(*appstorage.Error); ok && e.Code == "storage_busy" {
			storageBusy(w)
		} else {
			storageError(w, 409, "resource_changed", "resource changed; retry")
		}
		return nil, false
	}
	*r = *r.WithContext(ctx)
	return unlock, true
}

// POST query and download-link routes only read; method alone is insufficient.
func storageVisitorWrite(r *http.Request) bool {
	return r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch ||
		r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/query") && !strings.HasSuffix(r.URL.Path, "/download-link")
}
func storageBusy(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	storageError(w, 503, "storage_busy", "storage is busy; retry shortly")
}
