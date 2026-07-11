package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// maxStateOps caps the number of operations per PATCH request.
const maxStateOps = 100

// stateOp is one atomic edit. Supported ops (path is dot-separated, e.g.
// "settings.theme" or "_comments"):
//
//	{"op":"set","path":"settings.theme","value":"dark"}   set/replace a value
//	{"op":"inc","path":"votes.a","by":1}                  add to a number (by defaults to 1)
//	{"op":"append","path":"_comments","value":{...}}      push onto an array (creates [] if missing)
//	{"op":"remove","path":"settings.theme"}               delete a key
//	{"op":"removeWhere","path":"_comments","match":{"id":"x"}}  drop array items matching all pairs
type stateOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
	By    *float64        `json:"by,omitempty"`
	Match map[string]any  `json:"match,omitempty"`
}

var errMissingPath = errors.New("path not found")

// patchSiteState applies atomic ops to the site's JSON state inside a row-locked
// transaction. Concurrent PATCHes serialize on the lock — conflict-free and no
// optimistic-retry CPU. Origin-gated like the other state routes.
func (h *SiteHandler) patchSiteState(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return
	}
	if !h.viewSessionOK(r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this site is private — view it first to unlock its data"})
		return
	}

	// Resolve name -> site_id once; all subsequent state ops key by id.
	siteID, err := db.GetSiteIDByName(r.Context(), h.database, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSiteStateSize)
	var req struct {
		Ops []stateOp `json:"ops"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return
	}
	if len(req.Ops) == 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "ops is required and must be non-empty"})
		return
	}
	if len(req.Ops) > maxStateOps {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("too many ops (max %d)", maxStateOps)})
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	defer tx.Rollback()

	cur, _, err := db.GetSiteStateForUpdateByID(r.Context(), tx, siteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	root, err := stateRootObject(cur)
	if err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "state is not a JSON object; PATCH requires an object root"})
		return
	}

	if err := applyStateOps(root, req.Ops); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	newBytes, err := json.Marshal(root)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if len(newBytes) > maxSiteStateSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "resulting state exceeds size limit"})
		return
	}

	newVersion, err := db.SetSiteStateByID(r.Context(), tx, siteID, newBytes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(newVersion))
	w.WriteHeader(http.StatusOK)
	w.Write(newBytes)
}

// stateRootObject parses the stored state into an object map. A null/empty doc
// becomes {}. A non-object (array/scalar) is an error — PATCH needs an object.
func stateRootObject(cur json.RawMessage) (map[string]any, error) {
	t := strings.TrimSpace(string(cur))
	if t == "" || t == "null" {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal(cur, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

// applyStateOps mutates root by applying ops in order.
func applyStateOps(root map[string]any, ops []stateOp) error {
	for i, op := range ops {
		keys := splitPath(op.Path)
		if len(keys) == 0 {
			return fmt.Errorf("op %d: empty or invalid path", i)
		}

		switch op.Op {
		case "set":
			v, err := decodeValue(op.Value)
			if err != nil {
				return fmt.Errorf("op %d (set): %v", i, err)
			}
			parent, last, err := navigate(root, keys, true)
			if err != nil {
				return fmt.Errorf("op %d (set): %v", i, err)
			}
			parent[last] = v

		case "inc":
			by := 1.0
			if op.By != nil {
				by = *op.By
			}
			parent, last, err := navigate(root, keys, true)
			if err != nil {
				return fmt.Errorf("op %d (inc): %v", i, err)
			}
			cur, err := toFloat(parent[last])
			if err != nil {
				return fmt.Errorf("op %d (inc): %v", i, err)
			}
			parent[last] = cur + by

		case "append":
			v, err := decodeValue(op.Value)
			if err != nil {
				return fmt.Errorf("op %d (append): %v", i, err)
			}
			parent, last, err := navigate(root, keys, true)
			if err != nil {
				return fmt.Errorf("op %d (append): %v", i, err)
			}
			arr, ok := parent[last].([]any)
			if !ok && parent[last] != nil {
				return fmt.Errorf("op %d (append): %q is not an array", i, op.Path)
			}
			parent[last] = append(arr, v)

		case "remove":
			parent, last, err := navigate(root, keys, false)
			if errors.Is(err, errMissingPath) {
				continue // nothing to remove
			}
			if err != nil {
				return fmt.Errorf("op %d (remove): %v", i, err)
			}
			delete(parent, last)

		case "removeWhere":
			parent, last, err := navigate(root, keys, false)
			if errors.Is(err, errMissingPath) {
				continue
			}
			if err != nil {
				return fmt.Errorf("op %d (removeWhere): %v", i, err)
			}
			arr, ok := parent[last].([]any)
			if !ok {
				continue
			}
			kept := make([]any, 0, len(arr))
			for _, el := range arr {
				if m, ok := el.(map[string]any); ok && matchesAll(m, op.Match) {
					continue
				}
				kept = append(kept, el)
			}
			parent[last] = kept

		default:
			return fmt.Errorf("op %d: unknown op %q", i, op.Op)
		}
	}
	return nil
}

// navigate walks all but the last path segment, returning the parent object and
// the final key. With create=true, missing intermediate objects are created.
func navigate(root map[string]any, keys []string, create bool) (map[string]any, string, error) {
	cur := root
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k]
		if !ok {
			if !create {
				return nil, "", errMissingPath
			}
			m := map[string]any{}
			cur[k] = m
			cur = m
			continue
		}
		m, ok := next.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("path segment %q is not an object", k)
		}
		cur = m
	}
	return cur, keys[len(keys)-1], nil
}

func splitPath(p string) []string {
	p = strings.TrimSpace(p)
	if p == "" {
		return nil
	}
	parts := strings.Split(p, ".")
	for _, s := range parts {
		if s == "" {
			return nil // reject empty segments (e.g. "a..b" or leading/trailing dot)
		}
	}
	return parts
}

func decodeValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("missing value")
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("invalid value")
	}
	return v, nil
}

func toFloat(v any) (float64, error) {
	switch n := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return n, nil
	default:
		return 0, fmt.Errorf("existing value is not a number")
	}
}

func matchesAll(el map[string]any, match map[string]any) bool {
	if len(match) == 0 {
		return false // an empty match must not delete everything
	}
	for k, want := range match {
		if !reflect.DeepEqual(el[k], want) {
			return false
		}
	}
	return true
}
