package kv

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/httpx"
)

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
func StorageKV(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	c, ok := h.StorageResourceFor(w, r, c, "kv")
	if !ok {
		return
	}
	key := r.PathValue("key")
	if r.Method == http.MethodPut {
		key = NormalizeKVKey(key)
	}
	if key == "" {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			storageKVList(h, w, r, c)
		}
		return
	}
	if len(key) > 512 || strings.ContainsRune(key, 0) {
		httpx.WriteError(w, 400, "invalid_key", "invalid key")
		return
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		storageKVGet(h, w, r, c, key)
	case http.MethodPut:
		storageKVPut(h, w, r, c, key)
	case http.MethodDelete:
		storageKVDelete(h, w, r, c, key)
	}
}

func storageKVList(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	prefix, after, limit, ok := data.StoragePage(r)
	result, err := ListKV(h.StorageFor(c), r.Context(), KVListInput{Call: c, Prefix: prefix, After: after, Limit: limit, ValidLimit: ok})
	if data.StorageAnswered(w, result.Authorization, err, 0) {
		return
	}
	out := make([]storageKVItem, 0, len(result.Items))
	for _, row := range result.Items {
		out = append(out, storageKVItem{Key: row.Key, Value: row.Value, VisitorID: row.VisitorID, Mine: row.Mine})
	}
	httpx.WriteJSON(w, 200, map[string]any{"items": out, "next_after": result.Next})
}

func storageKVGet(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call, key string) {
	result, err := GetKV(h.StorageFor(c), r.Context(), KVReadInput{Call: c, Key: key})
	if data.StorageAnswered(w, result.Authorization, err, 0) {
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

func storageKVPut(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call, key string) {
	body := http.MaxBytesReader(w, r.Body, 1<<20)
	value, auth, err := PutKV(h.StorageFor(c), r.Context(), KVPutInput{Call: c, Key: key, Requested: r.Header.Get("X-SH-Visitor-Action"), Body: body})
	if data.StorageAnswered(w, auth, err, 400) {
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"key": key, "value": value})
}

func storageKVDelete(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call, key string) {
	auth, err := DeleteKV(h.StorageFor(c), r.Context(), KVDeleteInput{Call: c, Key: key})
	if data.StorageAnswered(w, auth, err, 0) {
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
}

// Register maps the key-value routes behind caller (Access's StorageCaller).
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/sites/{sitename}/storage/kv/{name}/keys", h.StorageRouteFor(caller, StorageKV))
	mux.Handle("GET /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", h.StorageRouteFor(caller, StorageKV))
	mux.Handle("PUT /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", h.StorageRouteFor(caller, StorageKV))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/kv/{name}/keys/{key}", h.StorageRouteFor(caller, StorageKV))
}
