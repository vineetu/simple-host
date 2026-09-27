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
//   - Idempotency-Key: a retried append or PATCH by a signed-in writer (the
//     owner's key or connector, or a signed-in visitor) is saved once.
//   - The watch: counts of the uses a later step tightens, per site and day,
//     for SAVED_DATA_WATCH_DAYS (7) before anything is enforced
//     (GET /v1/admin/data-watch, the admin page).
//   - Limits with stable codes: reads per site and address, or per key
//     (rate_limited); list items added per address (rate_limited); a per-site
//     cap on live data that refuses only writes that grow it (site_full).
//   - Delete for good (owner): one Recently deleted item, a list's whole
//     Recently deleted, or a site's history.

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
	h.appendLimiter = newRateLimiter(float64(c.AppendBurst), float64(c.AppendPerMin)/60)
	h.thinLimiter = newRateLimiter(1, 1)
}

// boundHistory keeps a site's history near SAVED_DATA_HISTORY_MAX_MB between
// sweeps: after a write that may have added to it, when sites.history_bytes
// is past the cap (one row read), the site's history is thinned now, at most
// once a second per site, so a flood of writes cannot grow it for a whole
// sweep interval. Never fails the write.
func (h *SiteHandler) boundHistory(r *http.Request, siteID string) {
	capBytes := int64(h.savedData.HistoryMaxMB) << 20
	ctx := context.WithoutCancel(r.Context())
	over, err := db.HistoryOverCap(ctx, h.database, siteID, capBytes)
	if err != nil || !over || (h.thinLimiter != nil && !h.thinLimiter.allow(siteID)) {
		return
	}
	if _, err := db.ThinSiteHistory(ctx, h.database, siteID, capBytes); err != nil {
		log.Printf("saved-data thin site_id=%s: %v", siteID, err)
	}
}

// siteMaxBytes is SAVED_DATA_SITE_MAX_MB in bytes.
func (h *SiteHandler) siteMaxBytes() int64 { return int64(h.savedData.SiteMaxMB) << 20 }

// withAuthorEmail fills in the address of a signed-in writer (the one GET /me
// gives): what the owner sees as "by". The platform admin's moderation shows
// as the operator, never the admin account's address.
func (h *SiteHandler) withAuthorEmail(ctx context.Context, a db.Actor) db.Actor {
	if a.Kind == actorAdmin {
		a.Email = db.OperatorLabel
		return a
	}
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

// siteHasRoom refuses (507 site_full) a write that grows a site's live saved
// data (page data and list items; history and Recently deleted are not
// counted) by growth bytes past SAVED_DATA_SITE_MAX_MB. A write that does not
// grow it always goes through. One row read (sites.data_bytes).
func (h *SiteHandler) siteHasRoom(w http.ResponseWriter, r *http.Request, siteID string, growth int64) bool {
	ok, err := db.HasRoom(r.Context(), h.database, siteID, growth, h.siteMaxBytes())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false
	}
	if !ok {
		h.writeSiteFull(w)
	}
	return ok
}

func (h *SiteHandler) writeSiteFull(w http.ResponseWriter) {
	writeJSON(w, http.StatusInsufficientStorage, errorResponse{
		Error: fmt.Sprintf("this site's saved data is full (%d MB of page data and list items); the owner can delete list items or clear lists to make room", h.savedData.SiteMaxMB),
		Code:  "site_full",
	})
}

// allowRead is the read limit on saved data (state and list GETs):
// SAVED_DATA_READ_PER_SEC with a SAVED_DATA_READ_BURST burst, per account for
// a valid key (the owner's, or a connector's: those share an AI vendor's
// addresses), otherwise per resolved site and address. Called once the site
// is resolved, so nothing the caller sends (a Host header, a name spelling)
// makes a fresh bucket. Writes the 429 when refused.
func (h *SiteHandler) allowRead(w http.ResponseWriter, r *http.Request, siteID string) bool {
	if h.readLimiter == nil || h.readLimiter.allow(h.readBucket(r, siteID)) {
		return true
	}
	tooManyRequests(w)
	return false
}

func (h *SiteHandler) readBucket(r *http.Request, siteID string) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		if u, ok, err := h.resolveWriterKey(r.Context(), key); err == nil && ok {
			return "user:" + u.ID
		}
	}
	return "site:" + siteID + "|" + clientIP(r)
}

// allowAppend is the per-address limit on list items added without the
// owner's key (SAVED_DATA_APPEND_PER_MIN, SAVED_DATA_APPEND_BURST). Writes
// the 429 when refused.
func (h *SiteHandler) allowAppend(w http.ResponseWriter, r *http.Request) bool {
	if h.appendLimiter != nil && !h.appendLimiter.allow(clientIP(r)) {
		tooManyRequests(w)
		return false
	}
	return true
}

// ---- Idempotency-Key -----------------------------------------------------------

const maxIdempotencyKey = 255

// idemClaim is a reserved Idempotency-Key for one write.
type idemClaim struct {
	scope []byte
	saved bool
}

// idemBegin makes a write sent with an Idempotency-Key happen once. Only a
// writer with an identity takes part: the owner's key or connector, or a
// signed-in visitor (actor.ID). The key is scoped to the site, the route and
// that identity, never to an address or an unchecked cookie, so two people
// never share an answer. Without an identity, or without the header, nothing
// changes (nil claim). A retry with the same body is answered by replay from
// what was kept (the status and the new version or item id, never the
// response body); the same key with another body is refused (409). handled:
// the answer is written.
func (h *SiteHandler) idemBegin(w http.ResponseWriter, r *http.Request, siteID, route string, actor db.Actor, body []byte, replay func(db.IdempotentResponse)) (*idemClaim, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return nil, false
	}
	if len(key) > maxIdempotencyKey {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "Idempotency-Key is at most 255 characters", Code: "invalid_idempotency_key"})
		return nil, true
	}
	if actor.ID == "" {
		return nil, false
	}
	sum := sha256.Sum256([]byte(siteID + "\x00" + route + "\x00" + actor.ID + "\x00" + key))
	bodySum := sha256.Sum256(body)
	claimed, prev, err := db.ClaimIdempotencyKey(r.Context(), h.database, sum[:], siteID, bodySum[:], 2*time.Minute)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return nil, true
	}
	if claimed {
		return &idemClaim{scope: sum[:]}, false
	}
	if !bytes.Equal(prev.BodyHash, bodySum[:]) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this Idempotency-Key was already used for a different request; use a new key for a new write", Code: "idempotency_key_reused"})
		return nil, true
	}
	if prev.Status == 0 {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "a request with this Idempotency-Key is still being saved; retry in a moment", Code: "idempotency_in_progress"})
		return nil, true
	}
	w.Header().Set("Idempotent-Replayed", "true")
	replay(prev)
	return nil, true
}

// idemSave keeps a successful write's answer for its retries.
func (h *SiteHandler) idemSave(r *http.Request, c *idemClaim, status int, etag string, ref int64) {
	if c == nil {
		return
	}
	c.saved = true
	// Keep it even if the caller has gone: that is the retry case.
	if err := db.SaveIdempotentResponse(context.WithoutCancel(r.Context()), h.database, c.scope, status, etag, ref); err != nil {
		log.Printf("idempotency save: %v", err)
	}
}

// idemEnd (deferred) frees a reservation whose write did not succeed, so a
// retry runs again.
func (h *SiteHandler) idemEnd(r *http.Request, c *idemClaim) {
	if c == nil || c.saved {
		return
	}
	if err := db.ReleaseIdempotencyKey(context.WithoutCancel(r.Context()), h.database, c.scope); err != nil {
		log.Printf("idempotency release: %v", err)
	}
}

// replayItem answers a retried append with the item the first one made.
func (h *SiteHandler) replayItem(w http.ResponseWriter, r *http.Request, siteID, coll string) func(db.IdempotentResponse) {
	return func(prev db.IdempotentResponse) {
		item, err := db.GetCollectionItemByID(r.Context(), h.database, siteID, coll, prev.Ref)
		if err != nil {
			writeJSON(w, prev.Status, map[string]int64{"id": prev.Ref})
			return
		}
		writeJSON(w, prev.Status, item)
	}
}

// ---- keeping history bounded ---------------------------------------------------

// StartSavedDataSweep removes expired history, deleted items past the undo
// window, old idempotency answers (and a site's past
// SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE) and watch counts past
// SAVED_DATA_WATCH_KEEP_DAYS every SAVED_DATA_SWEEP_MINUTES, and thins any
// site's history past SAVED_DATA_HISTORY_MAX_MB.
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
	for _, rl := range []*rateLimiter{h.readLimiter, h.appendLimiter, h.thinLimiter} {
		if rl != nil {
			rl.evictIdle(10 * time.Minute)
		}
	}
	hist, items, err := db.PurgeSavedData(ctx, h.database, h.savedData.UndoDays)
	if err != nil {
		log.Printf("saved-data sweep: %v", err)
		return
	}
	idem, err := db.PurgeIdempotencyKeys(ctx, h.database, time.Duration(h.savedData.IdempotencyHours)*time.Hour, h.savedData.IdempotencyMaxPerSite)
	if err != nil {
		log.Printf("saved-data sweep (idempotency): %v", err)
	}
	watch, err := db.PurgeDataWatch(ctx, h.database, h.savedData.WatchKeepDays)
	if err != nil {
		log.Printf("saved-data sweep (watch): %v", err)
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
	if hist+items+idem+thinned+watch > 0 {
		log.Printf("saved-data sweep: history=%d deleted_items=%d idempotency=%d thinned=%d watch=%d", hist, items, idem, thinned, watch)
	}
}

// ---- owner routes: history, Recently deleted, restore, delete for good ----------

const (
	defaultHistoryPage = 50
	maxHistoryPage     = 200
)

// pageLimit reads ?limit.
func pageLimit(r *http.Request) int {
	limit := defaultHistoryPage
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxHistoryPage {
		limit = maxHistoryPage
	}
	return limit
}

// pageArgs reads ?limit and ?before (a change id).
func pageArgs(r *http.Request) (int, int64) {
	var before int64
	if v, err := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64); err == nil && v > 0 {
		before = v
	}
	return pageLimit(r), before
}

// deletedCursor is Recently deleted's `next`/`before`: "<deleted_at in unix
// microseconds>_<id>". Anything else starts from the newest.
func deletedCursor(v string) db.DeletedCursor {
	at, id, ok := strings.Cut(v, "_")
	if !ok {
		return db.DeletedCursor{}
	}
	us, err1 := strconv.ParseInt(at, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if err1 != nil || err2 != nil || n <= 0 {
		return db.DeletedCursor{}
	}
	return db.DeletedCursor{At: time.UnixMicro(us), ID: n}
}

func deletedNext(it db.DeletedItem) string {
	return strconv.FormatInt(it.DeletedAt.UnixMicro(), 10) + "_" + strconv.FormatInt(it.ID, 10)
}

// ownedDataSite resolves {sitename} (with {handle} when the route names one)
// for the signed-in owner, or the admin. With {coll} it also checks the list
// name, and keeps private lists off the shared host as every other owner route
// does. Writes the answer on failure.
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
	var err error
	if handle := strings.TrimSpace(r.PathValue("handle")); handle != "" {
		// Named exactly: the owner's own handle, or (the admin) anyone's.
		var owner db.User
		if owner, err = db.GetUserByHandleOrAlias(r.Context(), h.database, handle); err == nil {
			if owner.ID != user.ID && !user.IsAdmin {
				err = sql.ErrNoRows
			} else {
				var site db.Site
				if site, err = db.GetSiteByUser(r.Context(), h.database, owner.ID, siteName); err == nil {
					siteID = site.ID
				}
			}
		}
	} else {
		siteID, err = h.ownedSiteID(r, user, siteName)
	}
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
	e, err := db.ReadHistoryEntry(r.Context(), h.database, siteID, kind, coll, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return
	}
	if err != nil {
		log.Printf("saved-data history read site_id=%s id=%d: %v", siteID, id, err)
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
	state, ver, err := db.RestoreStateVersion(r.Context(), h.database, siteID, id, h.ownerActor(r.Context(), user, siteID), h.siteMaxBytes())
	h.boundHistory(r, siteID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
	case errors.Is(err, db.ErrNoEarlierValue):
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error(), Code: "nothing_to_restore"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		log.Printf("saved-data restore site_id=%s id=%d: %v", siteID, id, err)
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
	item, err := db.RestoreItemVersion(r.Context(), h.database, siteID, coll, id, h.ownerActor(r.Context(), user, siteID), h.siteMaxBytes())
	h.boundHistory(r, siteID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
	case errors.Is(err, db.ErrNoEarlierValue):
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error(), Code: "nothing_to_restore"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "collection": coll, "restored": id, "item": item})
	}
}

// listDeletedItems is GET /v1/sites/{s}/collections/{c}/deleted: the list's
// Recently deleted, most recently deleted first. `next` is an opaque cursor
// for ?before.
func (h *SiteHandler) listDeletedItems(w http.ResponseWriter, r *http.Request) {
	_, siteID, siteName, coll, ok := h.ownedDataSite(w, r)
	if !ok {
		return
	}
	limit := pageLimit(r)
	items, err := db.ListDeletedItems(r.Context(), h.database, siteID, coll, limit, deletedCursor(r.URL.Query().Get("before")))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	var next *string
	if len(items) == limit && limit > 0 {
		n := deletedNext(items[len(items)-1])
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
	n, err := db.UndeleteItems(r.Context(), h.database, siteID, coll, id, h.ownerActor(r.Context(), user, siteID), h.siteMaxBytes())
	if errors.Is(err, db.ErrSiteFull) {
		h.writeSiteFull(w)
		return
	}
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

// confirmBody reads {"confirm": "<want>"}; on a mismatch it writes the 400
// naming what to send and returns false.
func confirmBody(w http.ResponseWriter, r *http.Request, want, what string) bool {
	var req struct {
		Confirm string `json:"confirm"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Confirm != want {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "to " + what + ", send {\"confirm\": \"" + want + "\"}",
			"code":  "confirm_required",
		})
		return false
	}
	return true
}

// purgeDeletedItems is DELETE .../collections/{c}/deleted/{id} (one item of
// Recently deleted) and DELETE .../collections/{c}/deleted with
// {"confirm": "<c>"} (all of it): gone for good, with their history. Live
// items are never touched. Owner (or admin) only, and logged.
func (h *SiteHandler) purgeDeletedItems(w http.ResponseWriter, r *http.Request) {
	user, siteID, siteName, coll, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	if h.adminNeedsHandle(w, r, user, siteID) {
		return
	}
	var id int64
	if r.PathValue("id") != "" {
		var valid bool
		if id, valid = pathID(r, "id"); !valid {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such deleted item", Code: "not_found"})
			return
		}
	} else if !confirmBody(w, r, coll, "delete everything in this list's Recently deleted for good") {
		return
	}
	n, err := db.PurgeDeletedItems(r.Context(), h.database, siteID, coll, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if id > 0 && n == 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such deleted item", Code: "not_found"})
		return
	}
	log.Printf("data_purge user_id=%s admin=%t site_id=%s what=deleted_items collection=%s id=%d n=%d", user.ID, user.IsAdmin, siteID, coll, id, n)
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "collection": coll, "deleted_for_good": n})
}

// adminNeedsHandle refuses (409 handle_required) a delete for good by the
// admin that names another account's site by its bare name: bare names are
// not unique across accounts, so the admin says whose site it is with
// /v1/u/{handle}/sites/{name}/…. The admin's own sites and the named form go
// through. Writes the refusal.
func (h *SiteHandler) adminNeedsHandle(w http.ResponseWriter, r *http.Request, user *db.User, siteID string) bool {
	if !user.IsAdmin || strings.TrimSpace(r.PathValue("handle")) != "" {
		return false
	}
	if owner, _, err := db.GetSiteWriteGate(r.Context(), h.database, siteID); err == nil && owner == user.ID {
		return false
	}
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: "name the site's owner for a delete for good: use /v1/u/{handle}/sites/{sitename}/…",
		Code:  "handle_required",
	})
	return true
}

// clearDataHistory is DELETE /v1/sites/{s}/history {"confirm": "<s>"}: every
// earlier version of the site's saved data and list items goes for good. The
// data itself and Recently deleted stay. Owner (or admin) only, and logged.
func (h *SiteHandler) clearDataHistory(w http.ResponseWriter, r *http.Request) {
	user, siteID, siteName, _, ok := h.ownedDataSite(w, r)
	if !ok || h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	if h.adminNeedsHandle(w, r, user, siteID) {
		return
	}
	if !confirmBody(w, r, siteName, "clear this site's history for good") {
		return
	}
	n, err := db.ClearSiteHistory(r.Context(), h.database, siteID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	log.Printf("data_purge user_id=%s admin=%t site_id=%s what=history n=%d", user.ID, user.IsAdmin, siteID, n)
	writeJSON(w, http.StatusOK, map[string]any{"site": siteName, "cleared": n})
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
