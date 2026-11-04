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
	"regexp"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/legacy"
	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// Append-only collections: a second backend type, for lists that grow large or
// take heavy concurrent appends (comments, guestbooks, logs). Unlike the single
// JSON state document, an append is one INSERT (no document rewrite) and reads
// are paginated — so it stays cheap as the list grows. Same Origin gate and
// rate limit as state.

var validCollectionName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

const (
	maxCollectionItemSize = 64 * 1024
)

func (h *SiteHandler) appendCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	// The site's own pages (Origin), or an agent with the owner's key (as on
	// the /data routes): the write path checks the key is the owner's.
	if keyWithoutPage(r) {
		if siteName == "" || !validCollectionName.MatchString(coll) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid site or collection name"})
			return
		}
	} else if !h.collectionGate(w, r, siteName, coll) {
		return
	}

	// Resolve name -> site_id once; collection ops key by id.
	siteID, err := h.resolveWriteSiteID(r, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	// The name's kind (kinds.go): Page info takes no items; a name nobody
	// declared is Shared (or, with SAVED_DATA_DEFAULT_KIND=declare_first on a
	// site made after the kinds, takes nothing).
	set, ok := h.dataSettings(w, r, siteID, coll)
	if !ok || !h.appendAllowed(w, set, siteName) {
		return
	}
	// A Shared board has its own write rule (board.go).
	if set.Kind == db.KindBoard {
		h.appendBoard(w, r, siteID, set)
		return
	}
	// A private list has its own, stricter write rule (privatecollections.go);
	// it never falls back to the public-write gate below.
	if set.Private {
		h.appendPrivate(w, r, siteID, siteName, set)
		return
	}
	actor, ok := h.visitorWriteOK(w, r, siteID, siteName, writeRouteCollectionPost, coll)
	if !ok {
		return
	}
	// Declared Submissions come from a signed-in visitor, always: one per
	// person and the block list need someone to count and to block. (A
	// Shared name keeps taking what the site's write mode lets through.)
	if set.Kind == db.KindEntries && actor.Kind == actorAnonymous {
		writeVisitorAuthRequired(w)
		return
	}
	// Items added without the owner's key: SAVED_DATA_APPEND_PER_MIN per address.
	if isVisitorActor(actor) && !h.allowAppend(w, r) {
		return
	}
	actor = h.withAuthorEmail(r.Context(), actor)

	r.Body = http.MaxBytesReader(w, r.Body, h.entryMaxBytes(set))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if len(body) == 0 || !json.Valid(body) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "body must be a JSON value"})
		return
	}
	if !storableJSON(w, body) {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(key) > maxIdempotencyKey {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Idempotency-Key is at most 255 characters", Code: "invalid_idempotency_key"})
		return
	}
	// Who sent it is kept with the item (the owner sees it; public reads
	// never do). Nothing in the item itself changes.
	result, err := h.legacySvc().AppendCollectionOnce(r.Context(), legacy.AppendInput{
		SiteID: siteID, Settings: set, Body: body, Actor: actor, Key: key,
		Visitor: isVisitorActor(actor), SuccessStatus: http.StatusCreated,
	})
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
	item, ok = h.entryResult(w, set, item, have, err)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

// watchItemSize counts a list item larger than SAVED_DATA_WATCH_ITEM_KB.
func (h *SiteHandler) watchItemSize(ctx context.Context, siteID string, size int) {
	h.legacySvc().WatchItemSize(ctx, siteID, size, h.savedData.WatchItemKB, watchItemLarge)
}

func (h *SiteHandler) listCollection(w http.ResponseWriter, r *http.Request) {
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	coll := strings.TrimSpace(r.PathValue("coll"))
	if err := h.legacySvc().AuthorizeCollectionGate(r.Context(), legacy.CollectionGateInput{SiteName: siteName, Collection: coll, OriginAllowed: true}); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	// Owner (or admin) API key is an alternative to the Origin gate so the
	// dashboard can read a collection (a keyed POST with no page skips the
	// gate too, in appendCollection). When the key matches, resolve the
	// site by owner (not the legacy oldest-name lookup) so two same-named
	// sites cannot leak each other's rows.
	var siteID string
	ownerKey := false
	// A read naming no page (no Origin, no Referer) skips the Origin gate:
	// public lists are public. Private lists still refuse it below.
	originless := noBrowserOrigin(r)
	if id, ok := h.ownerSiteIDFromKey(r, siteName); ok {
		siteID, ownerKey = id, true
	} else if !originless && !h.collectionGate(w, r, siteName, coll) {
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
	if adminSiteMismatch(r, siteID) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return
	}
	if !h.allowRead(w, r, siteID) {
		return
	}
	// A taken-down site serves nothing to the public; its owner's key (and the
	// admin's) still reads, so the owner can take their data with them.
	if !ownerKey && (h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID)) {
		return
	}

	// Private list: the owner's key reads everything (except through the
	// shared host, where private lists do not exist); a browser needs the
	// owner's or a submitter's visitor session on the site's own domain.
	set, err := h.legacySvc().ListCollectionSettings(r.Context(), siteID, coll)
	// Personal records are read only by their own person, one each, with
	// GET .../data/{name}: never as a list, by anyone (the owner included).
	if errors.Is(err, legacy.ErrPersonalList) {
		writePersonalOnly(w, coll)
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	private := set.Private
	if private {
		w.Header().Set("Cache-Control", "private, no-store")
		if ownerKey {
			if strings.EqualFold(requestHostName(r), h.contentHost) {
				writePrivateNotFound(w)
				return
			}
		} else if originless {
			writePrivateNotFound(w)
			return
		} else if !h.ownerBrowserRead(w, r, siteID) {
			return
		}
	}

	// Who sent each item: the owner's key (or the admin's), or the owner
	// signed in on the site's own address, only.
	withAuthor := ownerKey || h.ownerBrowserView(r, siteID)
	if withAuthor {
		// The answer carries who sent each item: never kept by a shared
		// cache or served to anyone else.
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Add("Vary", "Cookie")
		w.Header().Add("Vary", "X-API-Key")
	}
	items, next, err := h.legacySvc().ListCollection(r.Context(), legacy.CollectionListInput{SiteID: siteID, Collection: coll, Limit: r.URL.Query().Get("limit"), Before: r.URL.Query().Get("before"), WithAuthor: withAuthor, Settings: set})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
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
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	originAllowed := siteName != "" && h.authorizeStateOrigin(w, r, siteName)
	if err := h.legacySvc().CollectionOptions(r.Context(), legacy.CollectionOptionsInput{SiteName: r.PathValue("sitename"), OriginAllowed: originAllowed}); err != nil {
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
	// Origin authorization sets CORS headers, so retain its original position
	// after the name checks while the adapter makes the authorization decision.
	originAllowed := false
	if siteName != "" && validCollectionName.MatchString(coll) {
		originAllowed = h.authorizeStateOrigin(w, r, siteName)
	}
	err := h.legacySvc().AuthorizeCollectionGate(r.Context(), legacy.CollectionGateInput{SiteName: siteName, Collection: coll, OriginAllowed: originAllowed})
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
	in := legacy.OwnedSiteInput{SiteName: r.PathValue("sitename")}
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
	err = h.legacySvc().ForEachCollectionItem(r.Context(), siteID, coll, func(it db.CollectionItem) error {
		row := legacy.CollectionCSVRow(it, keys, csvSafe)
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
