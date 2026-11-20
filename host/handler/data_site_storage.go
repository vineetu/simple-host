package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

var storageNameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

// storageResource is one KV namespace, SQLite database or file bucket; its
// rules govern it (model.Resource.EffectiveRules).
type storageResource = model.Resource

type storageCall struct {
	siteID, ownerID, siteName, resourceName string
	accountID                               string
	owner                                   bool
	visitorAI                               bool
	visitorID                               string
	visitorSession                          model.VisitorSession
	linkScope                               string
	resource                                storageResource
	// ownerOnSite: the site's owner signed in on the site's own address
	// (storageOwnerOnSite); owner rights for storage data only.
	ownerOnSite   bool
	activeChecked bool
	// spamCheck: the spam check's outcome for this request ("" none sent,
	// "ok", or a refusal code); checkedPost: the add was allowed by it, so
	// the post limits apply (turnstile.go).
	spamCheck   string
	checkedPost bool
}

func storageError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: code})
}

func (h *DataHandler) storageSite(w http.ResponseWriter, r *http.Request) (storageCall, bool) {
	name := strings.ToLower(strings.TrimSpace(r.PathValue("sitename")))
	if name == "" {
		storageError(w, 400, "invalid_site", "site name is required")
		return storageCall{}, false
	}
	id, err := h.site.resolveWriteSiteID(r, name)
	if err != nil {
		storageError(w, 404, "site_not_found", "site not found")
		return storageCall{}, false
	}
	_, ownerID, siteName, err := h.site.sitesSvc().GetSiteOwner(r.Context(), id)
	if err != nil {
		storageError(w, 404, "site_not_found", "site not found")
		return storageCall{}, false
	}
	if h.access.refuseSuspendedSiteID(w, r, id) {
		return storageCall{}, false
	}
	h.site.paperSite(r, ownerID, siteName, nil)
	c := storageCall{siteID: id, ownerID: ownerID, siteName: siteName}
	if r.Header.Get(visitorPassHeader) != "" {
		if _, ok := h.access.internalVisitorSession(r, id); !ok {
			storageError(w, 403, "forbidden", "visitor pass does not cover this site")
			return storageCall{}, false
		}
		c.visitorAI = true
	} else if key := r.Header.Get("X-API-Key"); key != "" {
		u, ok, e := h.access.resolveWriterKey(r.Context(), key)
		if errors.Is(e, model.ErrKeyExpired) || errors.Is(e, model.ErrKeyExpiredIdle) {
			msg, code := auth.ExpiredKeyMessage(e, u.KeyExpiresAt, h.limits.KeyIdleExpiry)
			storageError(w, 401, code, msg)
			return storageCall{}, false
		} else if e != nil {
			storageError(w, 500, "internal_error", "internal server error")
			return storageCall{}, false
		}
		if ok && !u.DeployOnly() && (u.ID == ownerID || u.IsAdmin) {
			c.owner = true
			c.accountID = u.ID
			if u.Member != nil {
				// A file link made for a member or editor works only while
				// they may still act for the site (fileLinkScopeCurrent).
				c.linkScope = "as:" + u.Member.PersonID
			}
		}
		paperRecorder(r.Context()).User(r.Context(), &u)
		if !c.owner {
			storageError(w, 403, "forbidden", "owner credential required")
			return storageCall{}, false
		}
	} else if !h.storageSameSiteRequest(r, id) {
		storageError(w, 403, "forbidden", "use this site's own address")
		return storageCall{}, false
	}
	if !c.owner && h.site.refuseOffline(w, r, id) {
		return storageCall{}, false
	}
	w.Header().Set("Cache-Control", "no-store")
	return c, true
}

// Browser storage is limited to the site's own address; direct unauthenticated
// reads are allowed only when the host itself resolves to that site.
func (h *DataHandler) storageSameSiteRequest(r *http.Request, siteID string) bool {
	host := requestHostName(r)
	if h.access.isVisitorApexHost(host) || strings.EqualFold(host, h.contentHost) {
		return false
	}
	id, err := h.site.resolveSiteIDScoped(r, r.PathValue("sitename"))
	if err != nil || id != siteID {
		return false
	}
	if r.Header.Get("Origin") != "" || r.Header.Get("Referer") != "" {
		return sameOriginRequest(r)
	}
	return r.Method == http.MethodGet || r.Method == http.MethodHead
}

func (h *DataHandler) storageOwner(w http.ResponseWriter, c storageCall) bool {
	if c.owner {
		return true
	}
	storageError(w, 403, "forbidden", "owner credential required")
	return false
}

func (h *DataHandler) storageResourceFor(w http.ResponseWriter, r *http.Request, kind string) (storageCall, bool) {
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
		if storageVisitorWrite(r) && !h.storageIPLimiter.Allow(c.siteID+"/"+clientIP(r)) {
			tooManyRequests(w)
			return c, false
		}
		if sess, ok := h.access.strictVisitorSession(r, c.siteID); ok && sess.UserID != "" {
			c.visitorSession = sess
			c.visitorID = sess.UserID
			if storageVisitorWrite(r) && !h.storageVisitorLimiter.Allow(c.siteID+"/"+c.visitorID) {
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

func (h *DataHandler) listStorageResources(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok {
		return
	}
	out, err := h.storageSvc().ListStorageResources(r.Context(), h.storageServiceCall(c))
	if err != nil {
		writeStorageError(w, err, 403)
		return
	}
	entries := make([]map[string]any, 0, len(out))
	for _, item := range out {
		entries = append(entries, storageResourceRulesJSON(item.Resource, item.RulesErrors))
	}
	writeJSON(w, 200, map[string]any{"resources": entries})
}

func (h *DataHandler) putStorageResource(w http.ResponseWriter, r *http.Request) {
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
		Kind         string                      `json:"kind"`
		Rules        *json.RawMessage            `json:"rules"`
		SitePasscode string                      `json:"site_passcode"`
		Tables       *map[string]json.RawMessage `json:"tables"`
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		diagnostic := httpx.RuleError{Clause: "size", Position: -1, Message: "resource body exceeds 256 KiB"}
		writeStorageError(w, &appstorage.Error{Code: "invalid_rules", Message: diagnostic.Message, RuleError: &diagnostic, RulesErrors: []httpx.RuleError{diagnostic}}, 400)
		return
	}
	if err != nil || json.Unmarshal(data, &req) != nil {
		storageError(w, 400, "invalid_request", "invalid JSON body")
		return
	}
	call := h.storageServiceCall(c)
	var tables *map[string]appstorage.ResourceTableInput
	if req.Tables != nil {
		mapped := map[string]appstorage.ResourceTableInput{}
		for name, raw := range *req.Tables {
			var table struct {
				Rules *json.RawMessage `json:"rules"`
			}
			if json.Unmarshal(raw, &table) != nil {
				storageError(w, 400, "invalid_request", "invalid JSON body")
				return
			}
			mapped[name] = appstorage.ResourceTableInput{Rules: table.Rules, PresetFieldsPresent: storagePresetFieldsPresent(raw)}
		}
		tables = &mapped
	}
	result, err := h.storageRequestSvc(r, c).PutStorageResource(r.Context(), appstorage.ResourceInput{
		Call: call, Rules: req.Rules, PresetFieldsPresent: storagePresetFieldsPresent(data),
		Name: name, Kind: req.Kind, SitePasscode: req.SitePasscode, Tables: tables,
	})
	if err != nil {
		writeStorageError(w, err, 400)
		return
	}
	status := 200
	if result.Created {
		status = 201
	}
	body := storageResourceRulesJSON(result.Resource, result.RulesErrors)
	if len(result.Warnings) > 0 {
		body["warnings"] = result.Warnings
	}
	writeJSON(w, status, body)
}

// storagePresetFieldsPresent: the body names a field of the removed presets,
// even empty or null, which is refused (invalid_resource).
func storagePresetFieldsPresent(data []byte) bool {
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(data, &fields)
	for _, name := range []string{"preset", "read", "add", "edit", "delete", "write", "write_mode", "approve_first"} {
		for key := range fields {
			if strings.EqualFold(key, name) {
				return true
			}
		}
	}
	return false
}

func (h *DataHandler) listStorageResourceRows(ctx context.Context, siteID string) ([]storageResource, error) {
	return h.storageSvc().ListResourceRecords(ctx, appstorage.ResourceListInput{SiteID: siteID})
}

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

func (h *DataHandler) getStorageUsage(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	result, err := h.storageSvc().GetStorageUsage(r.Context(), h.storageServiceCall(c))
	if err != nil {
		writeStorageError(w, err, 0)
		return
	}
	writeJSON(w, 200, result)
}

func (h *DataHandler) deleteStorageResource(w http.ResponseWriter, r *http.Request) {
	c, ok := h.storageSite(w, r)
	if !ok || !h.storageOwner(w, c) {
		return
	}
	c, ok = h.storageResourceFor(w, r, "")
	if !ok {
		return
	}
	if err := h.storageRequestSvc(r, c).DeleteStorageResource(r.Context(), h.storageServiceCall(c)); err != nil {
		writeStorageError(w, err, 0)
		return
	}
	writeJSON(w, 200, map[string]any{"deleted": true})
}

func storagePage(r *http.Request) (string, string, int, bool) {
	q := r.URL.Query()
	limit, ok := httpx.Limit(q, 100, 500)
	return q.Get("prefix"), q.Get("after"), limit, ok
}

func (h *DataHandler) storageKV(w http.ResponseWriter, r *http.Request) {
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
		if storageRefused(w, result.Authorization) {
			return
		}
		item := result.Item
		if e != nil {
			writeStorageError(w, e, 0)
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
		if storageRefused(w, auth) {
			return
		}
		if e != nil {
			writeStorageError(w, e, 400)
			return
		}
		writeJSON(w, 200, map[string]any{"key": key, "value": value})
	case http.MethodDelete:
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		auth, e := h.storageRequestSvc(r, c).DeleteKV(r.Context(), appstorage.KVDeleteInput{Call: call, Key: key})
		if storageRefused(w, auth) {
			return
		}
		if e != nil {
			writeStorageError(w, e, 0)
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

func (h *DataHandler) storageKVList(w http.ResponseWriter, r *http.Request, c storageCall) {
	prefix, after, limit, ok := storagePage(r)
	call := h.storageServiceCall(c)
	call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
	result, e := h.storageRequestSvc(r, c).ListKV(r.Context(), appstorage.KVListInput{Call: call, Prefix: prefix, After: after, Limit: limit, ValidLimit: ok})
	if storageRefused(w, result.Authorization) {
		return
	}
	if e != nil {
		writeStorageError(w, e, 0)
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

// POST query and download-link routes only read; method alone is insufficient.
func storageVisitorWrite(r *http.Request) bool {
	return r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch ||
		r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/query") && !strings.HasSuffix(r.URL.Path, "/download-link")
}
