package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// Append-only collections: a second backend type, for lists that grow large or
// take heavy concurrent appends (comments, guestbooks, logs). Unlike the single
// JSON state document, an append is one INSERT (no document rewrite) and reads
// are paginated — so it stays cheap as the list grows. Same Origin gate +
// view-lock + rate limit as state.

var validCollectionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

const (
	maxCollectionItemSize = 64 * 1024
	defaultCollectionPage = 50
	maxCollectionPage     = 200
)

func (h *SiteHandler) appendCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if !h.collectionGate(w, r, siteName, coll) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCollectionItemSize)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if len(body) == 0 || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "body must be a JSON value"})
		return
	}

	item, err := db.AppendCollectionItem(r.Context(), h.database, siteName, coll, json.RawMessage(body))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (h *SiteHandler) listCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if !h.collectionGate(w, r, siteName, coll) {
		return
	}

	limit := defaultCollectionPage
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxCollectionPage {
		limit = maxCollectionPage
	}
	var before int64
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil && v > 0 {
		before = v
	}

	items, err := db.ListCollectionItems(r.Context(), h.database, siteName, coll, limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// next cursor: if we filled the page, the oldest id is the cursor for the next page.
	var next *int64
	if len(items) == limit {
		n := items[len(items)-1].ID
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next": next})
}

func (h *SiteHandler) optionsCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" || !h.authorizeStateOrigin(w, r, siteName) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// collectionGate applies the shared checks: valid name, Origin gate, and the
// view-lock (so a private page's collections are private too). Writes the error
// response and returns false when blocked.
func (h *SiteHandler) collectionGate(w http.ResponseWriter, r *http.Request, siteName, coll string) bool {
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return false
	}
	if !validCollectionName.MatchString(coll) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid collection name"})
		return false
	}
	if !h.authorizeStateOrigin(w, r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "forbidden"})
		return false
	}
	if !h.viewSessionOK(r, siteName) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "this site is private — view it first to unlock its data"})
		return false
	}
	return true
}
