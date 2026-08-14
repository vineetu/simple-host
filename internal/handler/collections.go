package handler

import (
	"bytes"
	"crypto/subtle"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
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

	// Resolve name -> site_id once; collection ops key by id.
	siteID, err := h.resolveSiteID(r, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !h.visitorWriteOK(w, r, siteID, siteName, writeRouteCollectionPost, coll) {
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

	item, err := db.AppendCollectionItemByID(r.Context(), h.database, siteID, coll, json.RawMessage(body))
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
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !validCollectionName.MatchString(coll) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid collection name"})
		return
	}
	// Owner (or admin) API key is an alternative to the Origin gate so the
	// dashboard can read a collection. POST still goes through collectionGate
	// only — this branch is GET-only. When the key matches, resolve the
	// site by owner (not the legacy oldest-name lookup) so two same-named
	// sites cannot leak each other's rows.
	var siteID string
	if id, ok := h.ownerSiteIDFromKey(r, siteName); ok {
		siteID = id
	} else if !h.collectionGate(w, r, siteName, coll) {
		return
	} else {
		var err error
		siteID, err = h.resolveSiteID(r, siteName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
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

	items, err := db.ListCollectionItemsByID(r.Context(), h.database, siteID, coll, limit, before)
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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SH-CSRF")
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

// ownerSiteIDFromKey returns the site id if r carries an X-API-Key that
// belongs to the owner of this specific site, or to the admin. A valid
// key for a different user is not enough. Missing/invalid keys return
// ok=false so the caller can fall through to the Origin gate — they
// must not 401 here.
func (h *SiteHandler) ownerSiteIDFromKey(r *http.Request, siteName string) (string, bool) {
	if siteName == "" {
		return "", false
	}
	apiKey := r.Header.Get("X-API-Key")
	if apiKey == "" {
		return "", false
	}

	var user *db.User
	if h.adminAPIKey != "" && subtle.ConstantTimeCompare([]byte(apiKey), []byte(h.adminAPIKey)) == 1 {
		user = &db.User{IsAdmin: true}
	} else {
		u, err := db.GetUserByAPIKey(r.Context(), h.database, apiKey)
		if err != nil {
			return "", false
		}
		user = &u
	}
	id, err := h.ownedSiteID(r, user, siteName)
	if err != nil {
		return "", false
	}
	return id, true
}

// ownedSiteID resolves the site the caller is allowed to inspect. Regular
// users must own it (user_id + name). Admins may look up any site by name
// (oldest same-named owner, matching GetSiteIDByName).
func (h *SiteHandler) ownedSiteID(r *http.Request, user *db.User, siteName string) (string, error) {
	if user == nil {
		return "", sql.ErrNoRows
	}
	if user.ID != "" {
		site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
		if err == nil {
			return site.ID, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if user.IsAdmin {
		return db.GetSiteIDByName(r.Context(), h.database, siteName)
	}
	return "", sql.ErrNoRows
}

// listSiteCollections is GET /v1/sites/{sitename}/collections — owner-only
// inventory of what the site has saved.
func (h *SiteHandler) listSiteCollections(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	siteID, err := h.ownedSiteID(r, user, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	summaries, err := db.ListCollectionSummariesByID(r.Context(), h.database, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"collections": summaries})
}

// exportCollectionCSV is GET /v1/sites/{sitename}/collections/{coll}/export.csv
// — owner-only, unbounded stream of the whole collection.
func (h *SiteHandler) exportCollectionCSV(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return
	}
	if !validCollectionName.MatchString(coll) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid collection name"})
		return
	}
	siteID, err := h.ownedSiteID(r, user, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	exists, err := db.CollectionExistsByID(r.Context(), h.database, siteID, coll)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !exists {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "collection not found"})
		return
	}

	keys, err := db.ListCollectionKeysByID(r.Context(), h.database, siteID, coll)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	filename := fmt.Sprintf("%s-%s-%s.csv", siteName, coll, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	header := make([]string, 0, 2+len(keys))
	header = append(header, "id", "created_at")
	header = append(header, keys...)
	if err := cw.Write(header); err != nil {
		return
	}

	n := 0
	err = db.ForEachCollectionItemByID(r.Context(), h.database, siteID, coll, func(it db.CollectionItem) error {
		row := make([]string, 2+len(keys))
		row[0] = strconv.FormatInt(it.ID, 10)
		row[1] = it.CreatedAt.UTC().Format(time.RFC3339)
		fields := jsonObjectFields(it.Data)
		for i, k := range keys {
			row[2+i] = jsonCSVCell(fields[k])
		}
		if err := cw.Write(row); err != nil {
			return err
		}
		n++
		if n%256 == 0 {
			cw.Flush()
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		return nil
	})
	cw.Flush()
	if err != nil {
		// Headers already sent; nothing left to report to the client.
		return
	}
}

// jsonObjectFields decodes a JSON object into raw per-key values. Non-objects
// yield an empty map so the CSV row is just id + created_at.
func jsonObjectFields(data json.RawMessage) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]json.RawMessage{}
	}
	return m
}

// jsonCSVCell renders one JSON value as a CSV cell: objects/arrays as compact
// JSON, null as empty, strings unquoted, numbers and booleans plainly.
func jsonCSVCell(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			return s
		}
	case '{', '[':
		var v any
		if err := json.Unmarshal(raw, &v); err == nil {
			b, err := json.Marshal(v)
			if err == nil {
				return string(b)
			}
		}
	}
	return string(raw)
}
