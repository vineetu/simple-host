package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// maxStatePathDepth is the maximum number of URL segments in /state/{path...}.
// Generous for a JSON document; cheap insurance against a pathological path.
const maxStatePathDepth = 32

var (
	errEmptyStatePath = errors.New("path is required")
	errEmptySegment   = errors.New("empty path segment")
	errPathTooDeep    = errors.New("path exceeds 32 segments")
)

// splitURLPath turns the {path...} remainder of /state/{path...} into the
// segment list Postgres path operators (and the in-process walk) take.
// Empty segments (//, leading/trailing slash) are rejected. Dotted ops
// still use splitPath.
func splitURLPath(p string) ([]string, error) {
	if p == "" {
		return nil, errEmptyStatePath
	}
	parts := strings.Split(p, "/")
	if len(parts) > maxStatePathDepth {
		return nil, errPathTooDeep
	}
	for _, s := range parts {
		if s == "" {
			return nil, errEmptySegment
		}
	}
	return parts, nil
}

// parseArrayIndex reports whether s is a canonical non-negative integer
// (no sign, no leading zeros except "0"). Those segments index an existing
// array; they never create one.
func parseArrayIndex(s string) (int, bool) {
	if s == "" || s[0] == '+' || s[0] == '-' {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	if strconv.Itoa(n) != s {
		return 0, false
	}
	return n, true
}

func parseStateJSON(cur json.RawMessage) (any, error) {
	t := strings.TrimSpace(string(cur))
	if t == "" || t == "null" {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(cur, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func marshalStateJSON(v any) ([]byte, error) {
	if v == nil {
		return []byte("null"), nil
	}
	return json.Marshal(v)
}

// getAtPath walks keys through root. A missing segment, a non-index into an
// array, or an out-of-range index yields nil — the HTTP layer serializes
// that as JSON null with 200.
func getAtPath(root any, keys []string) any {
	cur := root
	for _, key := range keys {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[key]
		case []any:
			idx, ok := parseArrayIndex(key)
			if !ok || idx >= len(node) {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
	}
	return cur
}

// setAtPath replaces the subtree at keys with value. Missing intermediate
// objects are created. An integer segment never creates an array: if the
// parent is missing (or not an array), the call fails.
func setAtPath(root any, keys []string, value any) (any, error) {
	if len(keys) == 0 {
		return value, nil
	}
	return setAt(root, keys, value)
}

func setAt(cur any, keys []string, value any) (any, error) {
	key := keys[0]
	last := len(keys) == 1
	idx, isIdx := parseArrayIndex(key)

	switch node := cur.(type) {
	case []any:
		if !isIdx {
			return nil, fmt.Errorf("path segment %q is not an array index", key)
		}
		if idx >= len(node) {
			return nil, fmt.Errorf("array index %d is out of range", idx)
		}
		if last {
			node[idx] = value
			return node, nil
		}
		next, err := setAt(node[idx], keys[1:], value)
		if err != nil {
			return nil, err
		}
		node[idx] = next
		return node, nil

	case map[string]any:
		if last {
			node[key] = value
			return node, nil
		}
		child, ok := node[key]
		if !ok {
			child = nil
		}
		next, err := setAt(child, keys[1:], value)
		if err != nil {
			return nil, err
		}
		node[key] = next
		return node, nil

	case nil:
		if isIdx {
			return nil, fmt.Errorf("cannot write array index %q: parent does not exist (writes never create arrays; create the array first)", key)
		}
		m := map[string]any{}
		if last {
			m[key] = value
			return m, nil
		}
		next, err := setAt(nil, keys[1:], value)
		if err != nil {
			return nil, err
		}
		m[key] = next
		return m, nil

	default:
		return nil, fmt.Errorf("path segment %q is not an object", key)
	}
}

// deleteAtPath removes the subtree at keys. A missing path is a no-op.
// An integer segment on an existing array drops that element (gap closed).
func deleteAtPath(root any, keys []string) (any, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	return deleteAt(root, keys)
}

func deleteAt(cur any, keys []string) (any, error) {
	key := keys[0]
	last := len(keys) == 1
	idx, isIdx := parseArrayIndex(key)

	switch node := cur.(type) {
	case []any:
		if !isIdx || idx >= len(node) {
			return node, nil
		}
		if last {
			return append(node[:idx:idx], node[idx+1:]...), nil
		}
		next, err := deleteAt(node[idx], keys[1:])
		if err != nil {
			return nil, err
		}
		node[idx] = next
		return node, nil

	case map[string]any:
		if last {
			delete(node, key)
			return node, nil
		}
		child, ok := node[key]
		if !ok {
			return node, nil
		}
		next, err := deleteAt(child, keys[1:])
		if err != nil {
			return nil, err
		}
		node[key] = next
		return node, nil

	default:
		return cur, nil
	}
}

func (h *SiteHandler) optionsSiteStateAtPath(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, If-Match, If-None-Match, X-SH-CSRF")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// gateStatePath runs the shared read-side gates (origin, view-lock, site
// lookup) and parses {path...}. Writes call visitorWriteOK after this.
func (h *SiteHandler) gateStatePath(w http.ResponseWriter, r *http.Request) (siteName, siteID string, keys []string, ok bool) {
	siteName = strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return "", "", nil, false
	}
	var err error
	keys, err = splitURLPath(r.PathValue("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return "", "", nil, false
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return "", "", nil, false
	}
	if !h.viewSessionOK(r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this site is private — view it first to unlock its data"})
		return "", "", nil, false
	}
	siteID, err = h.resolveSiteID(r, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return "", "", nil, false
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return "", "", nil, false
	}
	return siteName, siteID, keys, true
}

func (h *SiteHandler) getSiteStateAtPath(w http.ResponseWriter, r *http.Request) {
	_, siteID, keys, ok := h.gateStatePath(w, r)
	if !ok {
		return
	}

	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" {
		if expected, parsed := parseIfMatch(inm); parsed {
			ver, err := db.GetSiteStateVersionByID(r.Context(), h.database, siteID)
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return
			}
			if err == nil && ver == expected {
				w.Header().Set("ETag", stateETag(ver))
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
	}

	state, version, err := db.GetSiteStateByID(r.Context(), h.database, siteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	root, err := parseStateJSON(state)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	body, err := marshalStateJSON(getAtPath(root, keys))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(version))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (h *SiteHandler) putSiteStateAtPath(w http.ResponseWriter, r *http.Request) {
	siteName, siteID, keys, ok := h.gateStatePath(w, r)
	if !ok {
		return
	}
	if !h.visitorWriteOK(w, r, siteID, siteName, writeRouteStatePutPath, "") {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSiteStateSize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json"})
		return
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid json"})
		return
	}

	newVersion, err := h.applyStatePathWrite(w, r, siteID, func(root any) (any, error) {
		return setAtPath(root, keys, value)
	})
	if err != nil {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(newVersion))
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func (h *SiteHandler) deleteSiteStateAtPath(w http.ResponseWriter, r *http.Request) {
	siteName, siteID, keys, ok := h.gateStatePath(w, r)
	if !ok {
		return
	}
	if !h.visitorWriteOK(w, r, siteID, siteName, writeRouteStateDeletePath, "") {
		return
	}

	newVersion, err := h.applyStatePathWrite(w, r, siteID, func(root any) (any, error) {
		return deleteAtPath(root, keys)
	})
	if err != nil {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(newVersion))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("null"))
}

// applyStatePathWrite locks the site row, optionally CAS-checks If-Match
// against the document version, applies mutate, enforces the size cap, and
// writes the whole document back. The error is already written to w when
// non-nil.
func (h *SiteHandler) applyStatePathWrite(w http.ResponseWriter, r *http.Request, siteID string, mutate func(any) (any, error)) (int, error) {
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return 0, err
	}
	defer tx.Rollback()

	cur, version, err := db.GetSiteStateForUpdateByID(r.Context(), tx, siteID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return 0, err
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return 0, err
	}

	if ifMatch := r.Header.Get("If-Match"); strings.TrimSpace(ifMatch) != "" {
		expected, parsed := parseIfMatch(ifMatch)
		if !parsed {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid If-Match header"})
			return 0, errors.New("invalid If-Match header")
		}
		if version != expected {
			w.Header().Set("ETag", stateETag(version))
			writeJSON(w, http.StatusPreconditionFailed, errorResponse{Error: "state version conflict — re-read and retry"})
			return 0, db.ErrStateVersionConflict
		}
	}

	root, err := parseStateJSON(cur)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return 0, err
	}
	newRoot, err := mutate(root)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return 0, err
	}
	newBytes, err := marshalStateJSON(newRoot)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return 0, err
	}
	if len(newBytes) > maxSiteStateSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "resulting state exceeds size limit"})
		return 0, errors.New("resulting state exceeds size limit")
	}

	newVersion, err := db.SetSiteStateByID(r.Context(), tx, siteID, newBytes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return 0, err
	}
	return newVersion, nil
}
