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

func (h *SiteHandler) appendCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	// The site's own pages (Origin), or an agent with the owner's key (as on
	// the /data routes): the write path checks the key is the owner's.
	// Resolve name -> site_id once; collection ops key by id.
	gate := legacy.CollectionGateInput{SiteName: siteName, Collection: coll, KeyWithoutPage: keyWithoutPage(r), DeclareFirst: h.savedData.DefaultKind == config.DefaultKindDeclareFirst}
	deps := h.legacyCollectionRequestDeps(r, func(name string) bool {
		return h.authorizeStateOrigin(w, r, name)
	})
	deps.MaxEntries, deps.WatchItemKB, deps.MaxBytes = h.savedData.EntriesMax, h.savedData.WatchItemKB, h.siteMaxBytes()
	deps.NewListMetric, deps.LargeItemMetric = watchNewList, watchItemLarge
	deps.CollectionJSONStorable = jsonStorable
	deps.CollectionAppendBoard = func(siteID string, set db.DataSettings) {
		h.appendBoard(w, r, siteID, set)
	}
	deps.CollectionAppendPrivate = func(siteID string, set db.DataSettings) {
		h.appendPrivate(w, r, siteID, siteName, set)
	}
	deps.CollectionAppendActor = func(siteID string, set db.DataSettings) (db.Actor, bool) {
		actor, ok := h.visitorWriteOK(w, r, siteID, siteName, writeRouteCollectionPost, coll)
		if !ok {
			return db.Actor{}, false
		}
		return actor, true
	}
	deps.CollectionAppendPrepare = func(siteID string, set db.DataSettings, actor db.Actor) (legacy.AppendInput, bool) {
		// Items added without the owner's key: SAVED_DATA_APPEND_PER_MIN per address.
		if isVisitorActor(actor) && !h.allowAppend(w, r) {
			return legacy.AppendInput{}, false
		}
		actor = h.withAuthorEmail(r.Context(), actor)

		r.Body = http.MaxBytesReader(w, r.Body, h.entryMaxBytes(set))
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
				return legacy.AppendInput{}, false
			}
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return legacy.AppendInput{}, false
		}
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		return legacy.AppendInput{SiteID: siteID, Settings: set, Body: body, Actor: actor, Key: key, SuccessStatus: http.StatusCreated}, true
	}
	deps.CollectionAppendRespond = func(set db.DataSettings, result legacy.AppendResult, err error) {
		var opErr *legacy.Error
		if errors.As(err, &opErr) && opErr.Code == "invalid_idempotency_key" {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message, Code: opErr.Code})
			return
		}
		var claimErr *legacy.ClaimError
		if errors.As(err, &claimErr) {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		switch result.State {
		case "reused":
			writeJSON(w, http.StatusConflict, errorResponse{Error: "this Idempotency-Key was already used for a different request; use a new key for a new write", Code: "idempotency_key_reused"})
			return
		case "pending":
			writeJSON(w, http.StatusConflict, errorResponse{Error: "a request with this Idempotency-Key is still being saved; retry in a moment", Code: "idempotency_in_progress"})
			return
		case "replay":
			w.Header().Set("Idempotent-Replayed", "true")
			if result.ReplayFound {
				writeJSON(w, result.Previous.Status, result.Item)
			} else {
				writeJSON(w, result.Previous.Status, map[string]int64{"id": result.Previous.Ref})
			}
			return
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
	set, err := legacy.New(h.database, h.legacyStorageCutoff, deps).AppendCollection(r.Context(), gate)
	if err != nil {
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			switch opErr.Code {
			case "invalid_json":
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message, Code: opErr.Code})
			case "visitor_auth_required":
				writeVisitorAuthRequired(w)
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
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

}

// watchItemSize counts a list item larger than SAVED_DATA_WATCH_ITEM_KB.
func (h *SiteHandler) watchItemSize(ctx context.Context, siteID string, size int) {
	h.legacySvc().WatchItemSize(ctx, siteID, size, h.savedData.WatchItemKB, watchItemLarge)
}

func (h *SiteHandler) listCollection(w http.ResponseWriter, r *http.Request) {
	coll := strings.TrimSpace(r.PathValue("coll"))
	// Owner (or admin) API key is an alternative to the Origin gate so the
	// dashboard can read a collection (a keyed POST with no page skips the
	// gate too, in appendCollection). When the key matches, resolve the
	// site by owner (not the legacy oldest-name lookup) so two same-named
	// sites cannot leak each other's rows.
	// A read naming no page (no Origin, no Referer) skips the Origin gate:
	// public lists are public. Private lists still refuse it below.
	originless := noBrowserOrigin(r)
	in := legacy.CollectionListSiteInput{OwnerKeyInput: legacy.OwnerKeyInput{SiteName: r.PathValue("sitename"), APIKey: r.Header.Get("X-API-Key"), Handle: r.PathValue("handle")}, Collection: r.PathValue("coll"), Originless: originless}
	in.ScopedSiteID, in.Scoped = r.Context().Value(adminSiteKey{}).(string)
	rateHeaders := make(http.Header)
	result, err := h.legacyCollectionReadSvc(w, r, rateHeaders).ReadCollection(r.Context(), legacy.CollectionPageRequest{
		CollectionListSiteInput: in, Limit: r.URL.Query().Get("limit"), Before: r.URL.Query().Get("before"),
		SharedHost: strings.EqualFold(requestHostName(r), h.contentHost),
	})
	for name, values := range rateHeaders {
		w.Header()[name] = values
	}
	if result.PrivateCache || result.AuthorView {
		w.Header().Set("Cache-Control", "private, no-store")
	}
	if result.AuthorView {
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "X-API-Key")
	}
	if err != nil {
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			switch opErr.Code {
			case "read_limited":
				tooManyRequests(w)
				return
			case "private_not_found":
				writePrivateNotFound(w)
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
			case "site_name_required", "invalid_collection":
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message})
				return
			}
		}
		if errors.Is(err, legacy.ErrPersonalList) {
			writePersonalOnly(w, coll)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	set, items, next := result.Settings, result.Items, result.Next
	private := set.Private

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
