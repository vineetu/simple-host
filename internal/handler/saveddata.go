package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 1: the safety floor (saved-data redesign, owner approved
// 2026-09-27; INTENT). Nothing here changes what a page may do:
//
//   - History and undo: every change to a site's saved-data document and every
//     edit, delete or restore of a list item keeps the value from before it
//     for SAVED_DATA_UNDO_DAYS (30). Deleting an item or clearing a list keeps
//     the items as Recently deleted for as long. The owner lists and restores
//     (owner app, the API below, the connector).
//   - Authors: every write records who made it; only the owner sees it.
//   - Idempotency-Key: a retried append or PATCH is saved once.
//   - The watch: counts of the uses a later step tightens, per site and day,
//     for SAVED_DATA_WATCH_DAYS (7) before anything is enforced
//     (GET /v1/admin/data-watch, the admin page).
//   - Limits with stable codes: reads per address (rate_limited), a per-site
//     total including history (site_full).

// Who wrote (db.Actor.Kind).
const (
	actorOwner     = "owner"
	actorAdmin     = "admin"
	actorVisitor   = "visitor"
	actorAnonymous = "anonymous"
)

// Watch metrics: counts only, never content.
const (
	watchPutByVisitor = "put_by_visitor"    // a whole-document replace not by the owner
	watchPutNotObject = "put_not_object"    // a document replaced by something other than an object
	watchVisitorOp    = "visitor_op_"       // + set, inc, append, remove, removeWhere
	watchIncLarge     = "visitor_inc_large" // a visitor inc beyond SAVED_DATA_WATCH_INC_MAX
	watchNewList      = "new_list_name"     // a visitor started a list name nobody used before
	watchItemLarge    = "item_large"        // a list item beyond SAVED_DATA_WATCH_ITEM_KB
)

func isVisitorActor(a db.Actor) bool { return a.Kind == actorVisitor || a.Kind == actorAnonymous }

// SetSavedData applies the SAVED_DATA_* knobs. Call before serving.
func (h *SiteHandler) SetSavedData(c config.SavedData) {
	h.savedData = c
	h.readLimiter = newRateLimiter(float64(c.ReadBurst), float64(c.ReadPerSec))
}

// withAuthorEmail fills in the address of a signed-in writer (the one GET /me
// gives): what the owner sees as "by".
func (h *SiteHandler) withAuthorEmail(ctx context.Context, a db.Actor) db.Actor {
	if a.ID != "" && a.Email == "" {
		if e, err := visitorEmail(ctx, h.database, a.ID); err == nil {
			a.Email = e
		}
	}
	return a
}

// ownerActor is the signed-in caller of an owner route acting on siteID.
func (h *SiteHandler) ownerActor(ctx context.Context, user *db.User, siteID string) db.Actor {
	a := db.Actor{ID: user.ID, Kind: actorOwner}
	if user.IsAdmin {
		if owner, _, err := db.GetSiteWriteGate(ctx, h.database, siteID); err == nil && owner != user.ID {
			a.Kind = actorAdmin
		}
	}
	return h.withAuthorEmail(ctx, a)
}

// managerActor is who passed privateManager: a key's account (owner or
// admin), else the site's owner signed in on the site.
func (h *SiteHandler) managerActor(r *http.Request, siteID string) db.Actor {
	owner, _, _ := db.GetSiteWriteGate(r.Context(), h.database, siteID)
	a := db.Actor{ID: owner, Kind: actorOwner}
	if key := r.Header.Get("X-API-Key"); key != "" {
		if u, ok, err := h.resolveWriterKey(r.Context(), key); err == nil && ok {
			a.ID = u.ID
			if u.IsAdmin && u.ID != owner {
				a.Kind = actorAdmin
			}
		}
	}
	return h.withAuthorEmail(r.Context(), a)
}

// watch counts metric n times for siteID today. Never fails a request.
func (h *SiteHandler) watch(ctx context.Context, siteID, metric string, n int64) {
	if n <= 0 {
		return
	}
	if err := db.BumpDataWatch(ctx, h.database, siteID, metric, n); err != nil {
		log.Printf("data_watch site_id=%s metric=%s: %v", siteID, metric, err)
	}
}

// siteHasRoom refuses (507 site_full) a write that would take a site's saved
// data, history included, past SAVED_DATA_SITE_MAX_MB. History is thinned
// first, so only data the owner keeps can fill a site.
func (h *SiteHandler) siteHasRoom(w http.ResponseWriter, r *http.Request, siteID string, adding int) bool {
	limit := int64(h.savedData.SiteMaxMB) << 20
	used, err := db.SiteDataBytes(r.Context(), h.database, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if used+int64(adding) <= limit {
		return true
	}
	if _, err := db.ThinSiteHistory(r.Context(), h.database, siteID, int64(h.savedData.HistoryMaxMB)<<20); err != nil {
		log.Printf("saved-data thin site_id=%s: %v", siteID, err)
	}
	if used, err = db.SiteDataBytes(r.Context(), h.database, siteID); err == nil && used+int64(adding) <= limit {
		return true
	}
	writeJSON(w, http.StatusInsufficientStorage, errorResponse{
		Error: fmt.Sprintf("this site's saved data is full (%d MB, history included); the owner can clear lists or old data", h.savedData.SiteMaxMB),
		Code:  "site_full",
	})
	return false
}

// limitReads is the per-address read limit on saved data (state and list
// GETs): SAVED_DATA_READ_PER_SEC with a SAVED_DATA_READ_BURST burst.
func (h *SiteHandler) limitReads(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.readLimiter != nil && !h.readLimiter.allow(clientIP(r)) {
			tooManyRequests(w)
			return
		}
		next(w, r)
	})
}

// ---- Idempotency-Key -----------------------------------------------------------

const maxIdempotencyKey = 255

// idempotent makes a write sent with an Idempotency-Key header happen once:
// the first successful answer is kept for SAVED_DATA_IDEMPOTENCY_HOURS and
// replayed to a retry (with Idempotent-Replayed: true). The key is scoped to
// the route, the host and the caller's credential, so two callers never share
// an answer. Without the header nothing changes.
func (h *SiteHandler) idempotent(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > maxIdempotencyKey {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Idempotency-Key is at most 255 characters", Code: "invalid_idempotency_key"})
			return
		}
		cred := r.Header.Get("X-API-Key")
		if cred == "" {
			cred = visitorCookieValue(r)
		}
		if cred == "" {
			cred = "ip:" + clientIP(r)
		}
		sum := sha256.Sum256([]byte(r.Method + "\x00" + requestHostName(r) + "\x00" + r.URL.Path + "\x00" + cred + "\x00" + key))
		scope := sum[:]
		claimed, prev, err := db.ClaimIdempotencyKey(r.Context(), h.database, scope, 2*time.Minute)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if !claimed {
			if prev.Status == 0 {
				writeJSON(w, http.StatusConflict, errorResponse{Error: "a request with this Idempotency-Key is still being saved; retry in a moment", Code: "idempotency_in_progress"})
				return
			}
			if prev.ContentType != "" {
				w.Header().Set("Content-Type", prev.ContentType)
			}
			if prev.ETag != "" {
				w.Header().Set("ETag", prev.ETag)
			}
			w.Header().Set("Idempotent-Replayed", "true")
			w.WriteHeader(prev.Status)
			_, _ = w.Write(prev.Body)
			return
		}
		rec := &idemRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		// Keep the answer even if the caller has gone: that is the retry case.
		ctx := context.WithoutCancel(r.Context())
		if rec.status >= 200 && rec.status < 300 {
			if err := db.SaveIdempotentResponse(ctx, h.database, scope, db.IdempotentResponse{
				Status: rec.status, ContentType: rec.Header().Get("Content-Type"), ETag: rec.Header().Get("ETag"), Body: rec.body.Bytes(),
			}); err != nil {
				log.Printf("idempotency save: %v", err)
			}
			return
		}
		if err := db.ReleaseIdempotencyKey(ctx, h.database, scope); err != nil {
			log.Printf("idempotency release: %v", err)
		}
	})
}

type idemRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (r *idemRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *idemRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// ---- keeping history bounded ---------------------------------------------------

// StartSavedDataSweep removes expired history, deleted items past the undo
// window and old idempotency answers every SAVED_DATA_SWEEP_MINUTES, and
// thins any site's history past SAVED_DATA_HISTORY_MAX_MB.
func (h *SiteHandler) StartSavedDataSweep(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Duration(h.savedData.SweepMinutes) * time.Minute)
		defer t.Stop()
		for {
			h.sweepSavedData(ctx)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
}

func (h *SiteHandler) sweepSavedData(ctx context.Context) {
	hist, items, err := db.PurgeSavedData(ctx, h.database, h.savedData.UndoDays)
	if err != nil {
		log.Printf("saved-data sweep: %v", err)
		return
	}
	idem, err := db.PurgeIdempotencyKeys(ctx, h.database, time.Duration(h.savedData.IdempotencyHours)*time.Hour)
	if err != nil {
		log.Printf("saved-data sweep (idempotency): %v", err)
	}
	capBytes := int64(h.savedData.HistoryMaxMB) << 20
	sites, err := db.SitesOverHistoryCap(ctx, h.database, capBytes)
	if err != nil {
		log.Printf("saved-data sweep (cap): %v", err)
	}
	var thinned int64
	for _, id := range sites {
		n, err := db.ThinSiteHistory(ctx, h.database, id, capBytes)
		if err != nil {
			log.Printf("saved-data thin site_id=%s: %v", id, err)
			continue
		}
		thinned += n
	}
	if hist+items+idem+thinned > 0 {
		log.Printf("saved-data sweep: history=%d deleted_items=%d idempotency=%d thinned=%d", hist, items, idem, thinned)
	}
}

// ---- owner routes: history, Recently deleted, restore ----------------------------

const (
	defaultHistoryPage = 50
	maxHistoryPage     = 200
)

// pageArgs reads ?limit and ?before.
func pageArgs(r *http.Request) (int, int64) {
	limit := defaultHistoryPage
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxHistoryPage {
		limit = maxHistoryPage
	}
	var before int64
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil && v > 0 {
		before = v
	}
	return limit, before
}

// ownedDataSite resolves {sitename} for the signed-in owner (or the admin).
// With {coll} it also checks the list name, and keeps private lists off the
// shared host as every other owner route does. Writes the answer on failure.
func (h *SiteHandler) ownedDataSite(w http.ResponseWriter, r *http.Request) (user *db.User, siteID, siteName, coll string, ok bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	user = auth.GetUser(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
		return nil, "", "", "", false
	}
	siteName = strings.TrimSpace(r.PathValue("sitename"))
	coll = strings.TrimSpace(r.PathValue("coll"))
	if siteName == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site name is required"})
		return nil, "", "", "", false
	}
	if r.PathValue("coll") != "" && !validCollectionName.MatchString(coll) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid collection name"})
		return nil, "", "", "", false
	}
	siteID, err := h.ownedSiteID(r, user, siteName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return nil, "", "", "", false
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return nil, "", "", "", false
	}
	if coll != "" && strings.EqualFold(requestHostName(r), h.contentHost) {
		if private, err := db.IsCollectionPrivate(r.Context(), h.database, siteID, coll); err != nil || private {
			writePrivateNotFound(w)
			return nil, "", "", "", false
		}
	}
	return user, siteID, siteName, coll, true
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func historyNext(entries []db.HistoryEntry, limit int) *int64 {
	if len(entries) < limit || len(entries) == 0 {
		return nil
	}
	n := entries[len(entries)-1].ID
	return &n
}

// listDataHistory is GET /v1/sites/{s}/state/history and
// GET /v1/sites/{s}/collections/{c}/history: changes newest first, without
// their values.
func (h *SiteHandler) listDataHistory(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, coll, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	kind := db.HistoryState
	if coll != "" {
		kind = db.HistoryList
	}
	limit, before := pageArgs(r)
	entries, err := db.ListHistory(r.Context(), h.database, siteID, kind, coll, limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := map[string]any{"site": siteName, "history": entries, "next": historyNext(entries, limit), "undo_days": h.savedData.UndoDays}
	if coll != "" {
		resp["collection"] = coll
	}
	writeJSON(w, http.StatusOK, resp)
}

// getDataHistory is GET .../history/{id}: one change with the value from
// before it.
func (h *SiteHandler) getDataHistory(w http.ResponseWriter, r *http.Request) {
	_, siteID, _, coll, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	id, valid := pathID(r, "id")
	if !valid {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return
	}
	kind := db.HistoryState
	if coll != "" {
		kind = db.HistoryList
	}
	e, err := db.GetHistoryEntry(r.Context(), h.database, siteID, kind, coll, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if e.Value == nil {
		e.Value = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, e)
}

// restoreStateHistory is POST /v1/sites/{s}/state/history/{id}/restore: the
// document goes back to how it was before that change. A new change itself.
func (h *SiteHandler) restoreStateHistory(w http.ResponseWriter, r *http.Request) {
	user, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	id, valid := pathID(r, "id")
	if !valid {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return
	}
	state, ver, err := db.RestoreStateVersion(r.Context(), h.database, siteID, id, h.ownerActor(r.Context(), user, siteID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
	case errors.Is(err, db.ErrNoEarlierValue):
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error(), Code: "nothing_to_restore"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		w.Header().Set("ETag", stateETag(ver))
		writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "restored": id, "version": ver, "state": state})
	}
}

// restoreListHistory is POST /v1/sites/{s}/collections/{c}/history/{id}/restore:
// undo one list change (a delete or clear brings the item back; an edit puts
// its earlier fields back).
func (h *SiteHandler) restoreListHistory(w http.ResponseWriter, r *http.Request) {
	user, siteID, siteName, coll, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	id, valid := pathID(r, "id")
	if !valid {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return
	}
	item, err := db.RestoreItemVersion(r.Context(), h.database, siteID, coll, id, h.ownerActor(r.Context(), user, siteID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
	case errors.Is(err, db.ErrNoEarlierValue):
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error(), Code: "nothing_to_restore"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "collection": coll, "restored": id, "item": item})
	}
}

// listDeletedItems is GET /v1/sites/{s}/collections/{c}/deleted: the list's
// Recently deleted, most recently deleted first.
func (h *SiteHandler) listDeletedItems(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, coll, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	limit, before := pageArgs(r)
	items, err := db.ListDeletedItems(r.Context(), h.database, siteID, coll, limit, before)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	var next *int64
	if len(items) == limit && limit > 0 {
		n := items[len(items)-1].ID
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "collection": coll, "items": items, "next": next, "undo_days": h.savedData.UndoDays})
}

// restoreDeletedItem is POST .../collections/{c}/items/{id}/restore (one item)
// and POST .../collections/{c}/deleted/restore {"all": true} (every item in
// the list's Recently deleted, e.g. after a clear).
func (h *SiteHandler) restoreDeletedItem(w http.ResponseWriter, r *http.Request) {
	user, siteID, siteName, coll, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	var id int64
	if r.PathValue("id") != "" {
		var valid bool
		if id, valid = pathID(r, "id"); !valid {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such deleted item", Code: "not_found"})
			return
		}
	} else {
		var req struct {
			All bool `json:"all"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || !req.All {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `to bring back every deleted item, send {"all": true}`, Code: "confirm_required"})
			return
		}
	}
	n, err := db.UndeleteItems(r.Context(), h.database, siteID, coll, id, h.ownerActor(r.Context(), user, siteID))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if id > 0 && n == 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such deleted item", Code: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "collection": coll, "restored": n})
}

// adminDataWatch is GET /v1/admin/data-watch?days=N: per site, how often each
// use the later tightening affects happened over the last N days
// (SAVED_DATA_WATCH_DAYS by default), and since when the watch has counted.
func (h *SiteHandler) adminDataWatch(w http.ResponseWriter, r *http.Request) {
	days := h.savedData.WatchDays
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 90 {
		days = v
	}
	sites, first, err := db.ListDataWatch(r.Context(), h.database, days)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	resp := map[string]any{
		"days": days, "watch_days": h.savedData.WatchDays, "sites": sites,
		"thresholds": map[string]int{"inc_max": h.savedData.WatchIncMax, "item_kb": h.savedData.WatchItemKB},
	}
	if !first.IsZero() {
		resp["since"] = first.UTC().Format("2006-01-02")
		resp["days_counted"] = int(time.Since(first).Hours()/24) + 1
	}
	writeJSON(w, http.StatusOK, resp)
}
