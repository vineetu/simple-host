package handler

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/legacy"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Append-only collections: a second backend type, for lists that grow large or
// take heavy concurrent appends (comments, guestbooks, logs). Unlike the single
// JSON state document, an append is one INSERT (no document rewrite) and reads
// are paginated — so it stays cheap as the list grows. Same Origin gate and
// rate limit as state.

type collectionNameValidator struct{}

func (collectionNameValidator) MatchString(name string) bool { return legacy.ValidCollectionName(name) }

var validCollectionName collectionNameValidator

const (
	maxCollectionItemSize = 64 * 1024
)

// TODO(4.3): consolidate append without moving idempotency release ahead of the response.
func (h *SiteHandler) appendCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	gate := legacy.CollectionGateInput{SiteName: siteName, Collection: coll, KeyWithoutPage: keyWithoutPage(r), DeclareFirst: h.savedData.DefaultKind == config.DefaultKindDeclareFirst}
	result, err := h.legacyCollectionAppendSvc(r).AppendCollection(r.Context(), legacy.AppendCollectionInput{
		Gate: gate, IdempotencyKey: r.Header.Get("Idempotency-Key"), SuccessStatus: http.StatusCreated,
		ActorFacts: legacy.CollectionWriteActor{SiteName: siteName, Key: r.Header.Get("X-API-Key"), Mode: h.writeAuthMode, Cookie: h.sessionCookieFor(r), Preview: previewReferer(r), ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), Canonical: h.personHostsCanonical(), VisitorPass: r.Header.Get(visitorPassHeader) != "", CSRF: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue},
		ReadBody: func(branch string, set db.DataSettings) ([]byte, bool, error) {
			size := legacy.EntryMaxBytes(set, h.savedData.EntryMaxKB, maxCollectionItemSize)
			if branch == "board" {
				size = h.boardItemMax()
			}
			reader := http.MaxBytesReader(nil, r.Body, size)
			if branch == "public" {
				r.Body = reader
			}
			body, err := io.ReadAll(reader)
			var maxErr *http.MaxBytesError
			return body, errors.As(err, &maxErr), err
		},
	})
	for key, values := range result.Headers {
		w.Header()[key] = values
	}
	if result.PrivateCache {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	set := result.Settings
	if result.Branch == "" {
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			switch opErr.Code {
			case "origin_not_allowed":
				writeOriginRefused(w)
			case "append_content":
				writeWrongKind(w, set.Name, set.Kind, "only the owner saves it, as a whole document with PUT /v1/sites/"+siteName+"/data/"+set.Name)
			case "append_personal":
				writeWrongKind(w, set.Name, set.Kind, "each signed-in visitor saves their own record with PUT or PATCH /v1/sites/"+siteName+"/data/"+set.Name+" (SH.data(name, 'personal').set)")
			case "declare_first":
				writeDeclareFirst(w, siteName, set.Name)
			default:
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message})
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	if result.Branch != "public" {
		h.writeSpecialCollection(w, set, result.AppendWriteResult, err, result.Branch == "board")
		return
	}
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		if result.ReplayFound {
			writeJSON(w, result.ReplayStatus, result.Item)
		} else {
			writeJSON(w, result.ReplayStatus, map[string]int64{"id": result.ReplayRef})
		}
		return
	}
	var idemErr *legacy.Error
	if errors.As(err, &idemErr) {
		switch idemErr.Code {
		case "site_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		case "site_suspended":
			writeSiteSuspended(w, idemErr.Message)
			return
		case "site_offline":
			writeSiteOffline(w)
			return
		case "preview_read_only":
			writePreviewReadOnly(w)
			return
		case "use_custom_domain":
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "this site saves on its own domain", "code": "use_custom_domain", "domain": idemErr.Message})
			return
		case "visitor_pass_forbidden":
			storageError(w, http.StatusForbidden, "forbidden", "visitor pass does not cover this site")
			return
		case "csrf_required":
			writeJSON(w, http.StatusForbidden, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{Error: "missing CSRF header", Code: "csrf_required"})
			return
		case "invalid_api_key":
			writeJSON(w, http.StatusUnauthorized, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{Error: "invalid API key", Code: "invalid_api_key"})
			return
		case "account_suspended":
			writeAccountSuspended(w)
			return
		case "not_allowed_to_save":
			writeNotAllowedToSave(w)
			return
		case "visitor_auth_required":
			writeVisitorAuthRequired(w)
			return
		case "rate_limited":
			tooManyRequests(w)
			return
		case "item_too_large":
			signalLegacyBodyTooLarge(w)
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		case "invalid_request_body":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		case "invalid_collection_body":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: idemErr.Message})
			return
		case "invalid_json":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: idemErr.Message, Code: "invalid_json"})
			return
		case "invalid_idempotency_key":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Idempotency-Key is at most 255 characters", Code: "invalid_idempotency_key"})
			return
		case "idempotency_key_reused":
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this Idempotency-Key was already used for a different request; use a new key for a new write", Code: "idempotency_key_reused"})
			return
		case "idempotency_in_progress":
			writeJSON(w, http.StatusConflict, errorResponse{Error: "a request with this Idempotency-Key is still being saved; retry in a moment", Code: "idempotency_in_progress"})
			return
		}
	}
	item, have := result.Item, result.Have
	var quotaErr *legacy.AppendQuotaError
	if errors.As(err, &quotaErr) {
		if quotaErr.Full {
			h.writeSiteFull(w)
		} else {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	item, ok := h.entryResult(w, set, item, have, err)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

// writeSpecialCollection maps private and board adapter results to wire responses.
func (h *SiteHandler) writeSpecialCollection(w http.ResponseWriter, set db.DataSettings, result legacy.AppendWriteResult, err error, board bool) {
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		if result.ReplayFound {
			writeJSON(w, result.ReplayStatus, result.Item)
		} else {
			writeJSON(w, result.ReplayStatus, map[string]int64{"id": result.ReplayRef})
		}
		return
	}
	var opErr *legacy.Error
	if errors.As(err, &opErr) {
		switch opErr.Code {
		case "use_custom_domain":
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "this site saves on its own domain", "code": "use_custom_domain", "domain": opErr.Message})
			return
		case "private_visitor_only":
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this list is private: only visitors signed in on the site's own domain can add to it; API keys cannot", "code": "private_visitor_only"})
			return
		case "private_needs_own_domain":
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this list is private: it takes submissions only on the site's own address, from a signed-in visitor", "code": "private_needs_own_domain"})
			return
		case "private_not_object":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a private list takes one JSON object per item"})
			return
		case "item_too_large":
			signalLegacyBodyTooLarge(w)
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		case "invalid_item":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid item"})
			return
		case "site_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		case "site_suspended":
			writeSiteSuspended(w, opErr.Message)
			return
		case "site_offline":
			writeSiteOffline(w)
			return
		case "preview_read_only":
			writePreviewReadOnly(w)
			return
		case "invalid_api_key":
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key", Code: "invalid_api_key"})
			return
		case "owner_only":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "only the site owner can change page info", Code: "owner_only"})
			return
		case "csrf_required":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
			return
		case "account_suspended":
			writeAccountSuspended(w)
			return
		case "visitor_auth_required":
			writeVisitorAuthRequired(w)
			return
		case "not_allowed_to_save":
			writeNotAllowedToSave(w)
			return
		case "rate_limited":
			tooManyRequests(w)
			return
		case "board_item_too_large":
			signalLegacyBodyTooLarge(w)
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: fmt.Sprintf("a board item is at most %d KB", h.savedData.BoardItemMaxKB), Code: "item_too_large"})
			return
		case "invalid_request_body":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		case "board_not_object":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `a board item is one JSON object, e.g. {"text": "milk", "done": false}`, Code: "not_an_object"})
			return
		case "invalid_json":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message, Code: "invalid_json"})
			return
		case "invalid_idempotency_key":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Idempotency-Key is at most 255 characters", Code: "invalid_idempotency_key"})
			return
		case "idempotency_key_reused":
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this Idempotency-Key was already used for a different request; use a new key for a new write", Code: "idempotency_key_reused"})
			return
		case "idempotency_in_progress":
			writeJSON(w, http.StatusConflict, errorResponse{Error: "a request with this Idempotency-Key is still being saved; retry in a moment", Code: "idempotency_in_progress"})
			return
		}
	}
	var quotaErr *legacy.AppendQuotaError
	if !board && !errors.As(err, &quotaErr) {
		item, ok := h.entryResult(w, set, result.Item, result.Have, err)
		if ok {
			writeJSON(w, http.StatusCreated, item)
		}
		return
	}
	switch {
	case errors.As(err, &quotaErr) && quotaErr.Full:
		h.writeSiteFull(w)
	case errors.Is(err, db.ErrNameFull):
		h.writeBoardFull(w)
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, db.ErrKindChanged):
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q is not a board any more", set.Name), Code: "wrong_kind"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusCreated, result.Item)
	}
}

// watchItemSize counts a list item larger than SAVED_DATA_WATCH_ITEM_KB.
func (h *SiteHandler) watchItemSize(ctx context.Context, siteID string, size int) {
	h.legacySvc().WatchItemSize(ctx, siteID, size, h.savedData.WatchItemKB, watchItemLarge)
}

func (h *SiteHandler) listCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	originless := noBrowserOrigin(r)
	in := legacy.CollectionListSiteInput{OwnerKeyInput: legacy.OwnerKeyInput{SiteName: siteName, APIKey: r.Header.Get("X-API-Key"), Handle: r.PathValue("handle")}, Collection: coll, Originless: originless}
	in.ScopedSiteID, in.Scoped = r.Context().Value(adminSiteKey{}).(string)
	page, err := h.legacyCollectionReadSvc(r).ReadCollection(r.Context(), legacy.ReadCollectionInput{
		CollectionListSiteInput: in,
		CollectionReadViewInput: legacy.CollectionReadViewInput{
			SharedHost: strings.EqualFold(requestHostName(r), h.contentHost), Originless: originless,
			VisitorPass: r.Header.Get(visitorPassHeader) != "", HasStrictCookie: strictVisitorCookie(r) != ""},
		Limit: r.URL.Query().Get("limit"), Before: r.URL.Query().Get("before"),
	})
	for key, values := range page.Headers {
		w.Header()[key] = values
	}
	if page.Private {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	if page.Author {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "X-API-Key")
	}
	if errors.Is(err, legacy.ErrPersonalList) {
		writePersonalOnly(w, coll)
		return
	}
	if err != nil {
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			switch opErr.Code {
			case "site_not_found":
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
				return
			case "site_suspended":
				writeSiteSuspended(w, opErr.Message)
				return
			case "site_offline":
				writeSiteOffline(w)
				return
			case "origin_not_allowed":
				writeOriginRefused(w)
				return
			case "rate_limited":
				tooManyRequests(w)
				return
			case "site_name_required", "invalid_collection":
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message})
				return
			case "private_not_found":
				writePrivateNotFound(w)
				return
			}
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	set, private, items, next := page.Settings, page.Private, page.Items, page.Next
	board := set.Kind == db.KindBoard
	resp := map[string]any{"items": items, "next": next}
	if private {
		resp["private"] = true
	}
	if board {
		// A page polls a board with If-None-Match: 304 while nothing changed.
		resp["kind"] = db.KindBoard
		writeJSONETag(w, r, resp)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *SiteHandler) optionsCollection(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("sitename"))
	allowed := name != "" && h.authorizeStateOrigin(w, r, name)
	if err := h.legacySvc().CollectionOptions(r.Context(), legacy.CollectionOptionsInput{SiteName: name, OriginAllowed: allowed}); err != nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-SH-CSRF, Idempotency-Key, If-None-Match")
	w.Header().Set("Access-Control-Max-Age", "600")
	w.WriteHeader(http.StatusNoContent)
}

// collectionGate applies the shared checks: valid name and Origin gate. Writes
// the error response and returns false when blocked.
func (h *SiteHandler) collectionGate(w http.ResponseWriter, r *http.Request, siteName, coll string) bool {
	return h.authorizeCollectionGate(w, r, legacy.CollectionGateInput{SiteName: siteName, Collection: coll})
}

func (h *SiteHandler) authorizeCollectionGate(w http.ResponseWriter, r *http.Request, in legacy.CollectionGateInput) bool {
	// Origin authorization sets CORS headers and must follow name validation.
	if in.SiteName != "" && legacy.ValidCollectionName(in.Collection) && !in.KeyWithoutPage {
		in.OriginAllowed = h.authorizeStateOrigin(w, r, in.SiteName)
	}
	err := h.legacySvc().AuthorizeCollectionGate(r.Context(), in)
	if err != nil {
		opErr := err.(*legacy.Error)
		if opErr.Code == "origin_not_allowed" {
			writeOriginRefused(w)
		} else {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message})
		}
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
	in := legacy.OwnerKeyInput{SiteName: siteName, APIKey: r.Header.Get("X-API-Key"), Handle: r.PathValue("handle")}
	in.ScopedSiteID, in.Scoped = r.Context().Value(adminSiteKey{}).(string)
	return h.legacyRequestSvc(r).OwnerSiteFromKey(r.Context(), in)
}

// ownedSiteID resolves the site the caller is allowed to inspect. Regular
// users must own it (user_id + name). Admins may look up any site by name
// (oldest same-named owner, matching GetSiteIDByName).
func (h *SiteHandler) ownedSiteID(r *http.Request, user *db.User, siteName string) (string, error) {
	in := legacy.OwnedSiteInput{SiteName: siteName}
	if user != nil {
		in.SignedIn, in.UserID, in.Admin = true, user.ID, user.IsAdmin
	}
	in.ScopedSiteID, in.Scoped = r.Context().Value(adminSiteKey{}).(string)
	return h.legacySvc().OwnedSiteID(r.Context(), in)
}

// listSiteCollections is GET /v1/sites/{sitename}/collections — owner-only
// inventory of what the site has saved.
func (h *SiteHandler) listSiteCollections(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	in := legacy.ListSiteCollectionsInput{SiteName: r.PathValue("sitename")}
	if user != nil {
		in.SignedIn, in.UserID, in.Admin = true, user.ID, user.IsAdmin
	}
	in.ScopedSiteID, in.Scoped = r.Context().Value(adminSiteKey{}).(string)
	summaries, err := h.legacySvc().ListSiteCollections(r.Context(), in)
	if err != nil {
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			status := http.StatusBadRequest
			switch opErr.Code {
			case "unauthorized":
				status = http.StatusUnauthorized
			case "site_not_found":
				status = http.StatusNotFound
			}
			writeJSON(w, status, errorResponse{Error: opErr.Message})
		} else {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
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
	if strings.EqualFold(requestHostName(r), h.contentHost) {
		if private, err := h.legacySvc().IsCollectionPrivate(r.Context(), siteID, coll); err != nil || private {
			writePrivateNotFound(w)
			return
		}
	}
	if h.refusePersonal(w, r, siteID, coll) {
		return
	}
	keys, exists, err := h.legacySvc().CollectionExportKeys(r.Context(), siteID, coll)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !exists {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "collection not found"})
		return
	}

	filename := fmt.Sprintf("%s-%s-%s.csv", siteName, coll, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)

	cw := csv.NewWriter(w)
	header := legacy.CollectionCSVHeader(keys, csvSafe)
	if err := cw.Write(header); err != nil {
		return
	}

	n := 0
	err = h.legacySvc().ForEachCollectionCSVRow(r.Context(), siteID, coll, keys, csvSafe, func(row []string) error {
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

// withoutSubmitter drops the _submitted_by stamp from an item that has one.
// Anything else (and anything that is not a JSON object) comes back as is.
func withoutSubmitter(data json.RawMessage) json.RawMessage {
	return legacy.WithoutSubmitter(data)
}

// jsonCSVCell retains the existing helper for callers and tests.
func jsonCSVCell(raw json.RawMessage) string {
	return legacy.JSONCSVCell(raw, csvSafe)
}

// csvSafe moved to internal/httpx (httpx.CSVSafe); see the alias in
// internal/handler/httpx_forward.go.
