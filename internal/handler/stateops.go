package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"reflect"
	"strconv"
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
	By    json.RawMessage `json:"by,omitempty"`
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

	// Resolve name -> site_id once; all subsequent state ops key by id.
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	actor, ok := h.visitorWriteOK(w, r, siteID, siteName, writeRouteStatePatch, "")
	if !ok {
		return
	}
	actor = h.withAuthorEmail(r.Context(), actor)

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSiteStateSize))
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON body"})
		return
	}
	var req struct {
		Ops []stateOp `json:"ops"`
	}
	// UseNumber: numbers keep their exact digits (a large id or a precise
	// amount is not rounded through float64).
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
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
	// A retry answers with the document as it is now and its version.
	claim, handled := h.idemBegin(w, r, siteID, "PATCH state", actor, body, func(prev db.IdempotentResponse) {
		state, ver, err := db.GetSiteStateByID(r.Context(), h.database, siteID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", stateETag(ver))
		w.WriteHeader(prev.Status)
		w.Write(state)
	})
	if handled {
		return
	}
	defer h.idemEnd(r, claim)

	// The row is locked while the ops apply, so concurrent PATCHes
	// serialize; what changed goes to the site's history (undo).
	var reply *patchReply
	patch, newVersion, err := h.patchState(r, siteID, actor, req.Ops)
	switch {
	case errors.As(err, &reply):
		writeJSON(w, reply.status, errorResponse{Error: reply.msg, Code: reply.code})
		return
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
		return
	case err != nil:
		log.Printf("state patch site_id=%s: %v", siteID, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if isVisitorActor(actor) {
		h.watchVisitorOps(r.Context(), siteID, req.Ops)
	}
	h.idemSave(r, claim, http.StatusOK, stateETag(newVersion), int64(newVersion))

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", stateETag(newVersion))
	w.WriteHeader(http.StatusOK)
	w.Write(patch.Next)
}

// patchReply is a PATCH refused for what its ops ask (written as is).
type patchReply struct {
	status    int
	msg, code string
}

func (e *patchReply) Error() string { return e.msg }

// patchState applies ops to the document in one locked transaction.
func (h *SiteHandler) patchState(r *http.Request, siteID string, actor db.Actor, ops []stateOp) (db.StatePatch, int, error) {
	var out db.StatePatch
	ver, err := db.PatchSiteState(r.Context(), h.database, siteID, actor, h.siteMaxBytes(), h.savedData.SnapshotEvery, func(cur json.RawMessage) (db.StatePatch, error) {
		root, err := stateRootObject(cur)
		if err != nil {
			return db.StatePatch{}, &patchReply{http.StatusConflict, "state is not a JSON object; PATCH requires an object root", "not_an_object"}
		}
		// Before is decoded separately: the ops change root in place.
		before, err := decodeJSON(cur)
		if err != nil {
			return db.StatePatch{}, err
		}
		if err := applyStateOps(root, ops); err != nil {
			return db.StatePatch{}, &patchReply{http.StatusBadRequest, err.Error(), ""}
		}
		next, err := json.Marshal(root)
		if err != nil {
			return db.StatePatch{}, err
		}
		if len(next) > maxSiteStateSize {
			return db.StatePatch{}, &patchReply{http.StatusRequestEntityTooLarge, "resulting state exceeds size limit", "item_too_large"}
		}
		out = db.StatePatch{Before: before, After: root, Next: next}
		return out, nil
	})
	return out, ver, err
}

// decodeJSON decodes a stored document keeping exact numbers.
func decodeJSON(raw json.RawMessage) (any, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	err := dec.Decode(&v)
	return v, err
}

// stateRootObject parses the stored state into an object map. A null/empty doc
// becomes {}. A non-object (array/scalar) is an error — PATCH needs an object.
func stateRootObject(cur json.RawMessage) (map[string]any, error) {
	t := strings.TrimSpace(string(cur))
	if t == "" || t == "null" {
		return map[string]any{}, nil
	}
	var root map[string]any
	dec := json.NewDecoder(bytes.NewReader(cur))
	dec.UseNumber()
	if err := dec.Decode(&root); err != nil {
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
			by, err := incBy(op.By)
			if err != nil {
				return fmt.Errorf("op %d (inc): %v", i, err)
			}
			parent, last, err := navigate(root, keys, true)
			if err != nil {
				return fmt.Errorf("op %d (inc): %v", i, err)
			}
			sum, err := addNumbers(parent[last], by)
			if err != nil {
				return fmt.Errorf("op %d (inc): %v", i, err)
			}
			parent[last] = sum

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
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid value")
	}
	return v, nil
}

// incBy is an inc's amount: 1 when it is missing or null, otherwise a JSON
// number, kept as written.
func incBy(raw json.RawMessage) (json.Number, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || string(t) == "null" {
		return "1", nil
	}
	if t[0] == '"' {
		return "", fmt.Errorf("by must be a number")
	}
	n := json.Number(t)
	if _, err := n.Float64(); err != nil {
		return "", fmt.Errorf("by must be a number")
	}
	return n, nil
}

// addNumbers is inc: whole numbers add exactly (a count past 2^53 stays
// right); anything else adds as before, in float64. A missing value is 0.
func addNumbers(cur any, by json.Number) (any, error) {
	var n json.Number
	switch v := cur.(type) {
	case nil:
		n = "0"
	case json.Number:
		n = v
	case float64:
		n = json.Number(strconv.FormatFloat(v, 'g', -1, 64))
	default:
		return nil, fmt.Errorf("existing value is not a number")
	}
	if a, err := strconv.ParseInt(string(n), 10, 64); err == nil {
		if b, err := strconv.ParseInt(string(by), 10, 64); err == nil {
			if s := a + b; (b >= 0) == (s >= a) { // no overflow
				return json.Number(strconv.FormatInt(s, 10)), nil
			}
		}
	}
	a, err := n.Float64()
	if err != nil {
		return nil, fmt.Errorf("existing value is not a number")
	}
	b, _ := by.Float64()
	if s := a + b; !math.IsInf(s, 0) && !math.IsNaN(s) {
		return s, nil
	}
	return nil, fmt.Errorf("the result is too large a number")
}

func matchesAll(el map[string]any, match map[string]any) bool {
	if len(match) == 0 {
		return false // an empty match must not delete everything
	}
	for k, want := range match {
		if !jsonEqual(el[k], want) {
			return false
		}
	}
	return true
}

// jsonEqual compares two decoded JSON values, numbers by value (1 == 1.0),
// as matching did when every number was a float64.
func jsonEqual(a, b any) bool {
	an, aNum := asFloat(a)
	bn, bNum := asFloat(b)
	if aNum || bNum {
		if !aNum || !bNum {
			return false
		}
		// Two whole numbers compare exactly, at any size: a 20-digit id
		// never matches its neighbour through float64 rounding.
		ai, aok := new(big.Int).SetString(numberText(a), 10)
		bi, bok := new(big.Int).SetString(numberText(b), 10)
		if aok && bok {
			return ai.Cmp(bi) == 0
		}
		return an == bn
	}
	switch av := a.(type) {
	case map[string]any:
		bv, ok := b.(map[string]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for k, v := range av {
			w, ok := bv[k]
			if !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		bv, ok := b.([]any)
		if !ok || len(av) != len(bv) {
			return false
		}
		for i := range av {
			if !jsonEqual(av[i], bv[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(a, b)
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	}
	return 0, false
}

func numberText(v any) string {
	if n, ok := v.(json.Number); ok {
		return string(n)
	}
	return ""
}

// watchVisitorOps counts a visitor's PATCH ops by type, and increments
// larger than SAVED_DATA_WATCH_INC_MAX, for the watch.
func (h *SiteHandler) watchVisitorOps(ctx context.Context, siteID string, ops []stateOp) {
	counts := map[string]int64{}
	for _, op := range ops {
		counts[watchVisitorOp+op.Op]++
		if op.Op == "inc" {
			if by, err := incBy(op.By); err == nil {
				if f, err := by.Float64(); err == nil && math.Abs(f) > float64(h.savedData.WatchIncMax) {
					counts[watchIncLarge]++
				}
			}
		}
	}
	for m, n := range counts {
		h.watch(ctx, siteID, m, n)
	}
}
