package datahttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// StorageCall is a call on a site's saved data: the site and who is calling,
// as Access resolved it (storageCaller), then the resource the data route
// resolved (appstorage.Service.ResourceCall).
type StorageCall = appstorage.Call

// StorageResource is one KV namespace, SQLite database or file bucket; its
// rules govern it (model.Resource.EffectiveRules).
type StorageResource = model.Resource

// storageRoute is a data route: it runs once Access has resolved the site and
// the caller (AccessHandler.StorageCaller).
type storageRoute func(http.ResponseWriter, *http.Request, StorageCall)

// StorageRouteFor puts Access's caller resolution (caller, which is
// AccessHandler.StorageCaller) in front of a data route.
func (h *DataHandler) StorageRouteFor(caller func(http.Handler) http.Handler, fn storageRoute) http.Handler {
	return caller(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := appstorage.CallFrom(r.Context())
		c.RuntimeDir = h.StorageRuntimeDir(c)
		fn(w, r, c)
	}))
}

func storageOwner(w http.ResponseWriter, c StorageCall) bool {
	if !c.Owner {
		httpx.WriteError(w, 403, "forbidden", "owner credential required")
	}
	return c.Owner
}

// storageResourceFor resolves the resource the route names and admits a
// visitor's request to it.
func (h *DataHandler) storageResourceFor(w http.ResponseWriter, r *http.Request, c StorageCall, kind string) (StorageCall, bool) {
	c, err := h.StorageSvc().ResourceCall(r.Context(), c, r.PathValue("name"), kind)
	if err != nil {
		WriteStorageError(w, err, 0)
		return c, false
	}
	return c, c.Owner || h.admitVisitor(w, r, &c)
}

// admitVisitor applies a visitor's write rate limits (per address, then per
// signed-in visitor) and runs the spam check.
func (h *DataHandler) admitVisitor(w http.ResponseWriter, r *http.Request, c *StorageCall) bool {
	write := storageVisitorWrite(r)
	if write && !h.StorageIPLimiter.Allow(c.SiteID+"/"+httpx.ClientIP(r)) ||
		write && c.VisitorID != "" && !h.StorageVisitorLimiter.Allow(c.SiteID+"/"+c.VisitorID) {
		httpx.TooManyRequests(w)
		return false
	}
	return h.storageSpamCheck(w, r, c)
}

// POST query and download-link routes only read; method alone is insufficient.
func storageVisitorWrite(r *http.Request) bool {
	return r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch ||
		r.Method == http.MethodPost && !strings.HasSuffix(r.URL.Path, "/query") && !strings.HasSuffix(r.URL.Path, "/download-link")
}

func (h *DataHandler) ListStorageResources(w http.ResponseWriter, r *http.Request, c StorageCall) {
	out, err := h.StorageSvc().ListStorageResources(r.Context(), c)
	if err != nil {
		WriteStorageError(w, err, 403)
		return
	}
	entries := make([]map[string]any, 0, len(out))
	for _, item := range out {
		entries = append(entries, appstorage.ResourceRulesJSON(item.Resource, item.RulesErrors))
	}
	httpx.WriteJSON(w, 200, map[string]any{"resources": entries})
}

func (h *DataHandler) putStorageResource(w http.ResponseWriter, r *http.Request, c StorageCall) {
	if !storageOwner(w, c) {
		return
	}
	name := r.PathValue("name")
	if !appstorage.ValidResourceName(name) {
		httpx.WriteError(w, 400, "invalid_resource", "invalid resource name")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		diagnostic := httpx.RuleError{Clause: "size", Position: -1, Message: "resource body exceeds 256 KiB"}
		WriteStorageError(w, &appstorage.Error{Code: "invalid_rules", Message: diagnostic.Message, RuleError: &diagnostic, RulesErrors: []httpx.RuleError{diagnostic}}, 400)
		return
	}
	in, decodeErr := appstorage.DecodeResourceInput(body)
	if err != nil || decodeErr != nil {
		httpx.WriteError(w, 400, "invalid_request", "invalid JSON body")
		return
	}
	in.Call, in.Name = c, name
	result, err := h.StorageFor(c).PutStorageResource(r.Context(), in)
	if err != nil {
		WriteStorageError(w, err, 400)
		return
	}
	status := 200
	if result.Created {
		status = 201
	}
	out := appstorage.ResourceRulesJSON(result.Resource, result.RulesErrors)
	if len(result.Warnings) > 0 {
		out["warnings"] = result.Warnings
	}
	httpx.WriteJSON(w, status, out)
}

func (h *DataHandler) getStorageUsage(w http.ResponseWriter, r *http.Request, c StorageCall) {
	if !storageOwner(w, c) {
		return
	}
	result, err := h.StorageSvc().GetStorageUsage(r.Context(), c)
	if err != nil {
		WriteStorageError(w, err, 0)
		return
	}
	httpx.WriteJSON(w, 200, result)
}

func (h *DataHandler) deleteStorageResource(w http.ResponseWriter, r *http.Request, c StorageCall) {
	if !storageOwner(w, c) {
		return
	}
	c, ok := h.storageResourceFor(w, r, c, "")
	if !ok {
		return
	}
	if err := h.StorageFor(c).DeleteStorageResource(r.Context(), c); err != nil {
		WriteStorageError(w, err, 0)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
}

func (h *DataHandler) ListStorageResourceRows(ctx context.Context, siteID string) ([]StorageResource, error) {
	return h.StorageSvc().ListResourceRecords(ctx, appstorage.ResourceListInput{SiteID: siteID})
}
func storageResultLimitBytes() int {
	return appstorage.ResultLimitBytes(os.Getenv("SITE_STORAGE_SQL_RESULT_MAX_BYTES"))
}

type SiteStorageUsage = appstorage.Usage

func storagePage(r *http.Request) (string, string, int, bool) {
	q := r.URL.Query()
	limit, ok := httpx.Limit(q, 100, 500)
	return q.Get("prefix"), q.Get("after"), limit, ok
}

// storageKVItem is one entry of a KV listing. VisitorID: who wrote it, for
// the owner only (empty: the owner); Mine: a visitor added it (visitors only).
type storageKVItem struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	VisitorID string          `json:"visitor_id,omitempty"`
	Mine      *bool           `json:"mine,omitempty"`
}

// StorageKV serves the KV routes: GET .../keys lists, and GET, PUT and
// DELETE .../keys/{key} read, add or edit (decided under the write lock by
// whether the key exists; the page cannot choose) and delete one key.
func (h *DataHandler) StorageKV(w http.ResponseWriter, r *http.Request, c StorageCall) {
	c, ok := h.storageResourceFor(w, r, c, "kv")
	if !ok {
		return
	}
	key := r.PathValue("key")
	if r.Method == http.MethodPut {
		key = appstorage.NormalizeKVKey(key)
	}
	if key == "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			h.storageKVList(w, r, c)
		}
		return
	}
	if len(key) > 512 || strings.ContainsRune(key, 0) {
		httpx.WriteError(w, 400, "invalid_key", "invalid key")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.storageKVGet(w, r, c, key)
	case http.MethodPut:
		h.storageKVPut(w, r, c, key)
	case http.MethodDelete:
		h.storageKVDelete(w, r, c, key)
	}
}

func (h *DataHandler) storageKVList(w http.ResponseWriter, r *http.Request, c StorageCall) {
	prefix, after, limit, ok := storagePage(r)
	result, err := h.StorageFor(c).ListKV(r.Context(), appstorage.KVListInput{Call: c, Prefix: prefix, After: after, Limit: limit, ValidLimit: ok})
	if storageAnswered(w, result.Authorization, err, 0) {
		return
	}
	out := make([]storageKVItem, 0, len(result.Items))
	for _, row := range result.Items {
		out = append(out, storageKVItem{Key: row.Key, Value: row.Value, VisitorID: row.VisitorID, Mine: row.Mine})
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out, "next_after": result.Next})
}

func (h *DataHandler) storageKVGet(w http.ResponseWriter, r *http.Request, c StorageCall, key string) {
	result, err := h.StorageFor(c).GetKV(r.Context(), appstorage.KVReadInput{Call: c, Key: key})
	if storageAnswered(w, result.Authorization, err, 0) {
		return
	}
	item := result.Item
	out := map[string]any{"key": key, "value": item.Value}
	if item.VisitorID != "" {
		out["visitor_id"] = item.VisitorID
	}
	if item.Mine != nil {
		out["mine"] = *item.Mine
	}
	httpx.WriteJSON(w, 200, out)
}

func (h *DataHandler) storageKVPut(w http.ResponseWriter, r *http.Request, c StorageCall, key string) {
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	value, auth, err := h.StorageFor(c).PutKV(r.Context(), appstorage.KVPutInput{Call: c, Key: key, Requested: r.Header.Get("X-SH-Visitor-Action"), Body: body})
	if storageAnswered(w, auth, err, 400) {
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"key": key, "value": value})
}

func (h *DataHandler) storageKVDelete(w http.ResponseWriter, r *http.Request, c StorageCall, key string) {
	auth, err := h.StorageFor(c).DeleteKV(r.Context(), appstorage.KVDeleteInput{Call: c, Key: key})
	if storageAnswered(w, auth, err, 0) {
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
}
