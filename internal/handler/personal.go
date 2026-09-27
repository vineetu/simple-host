package handler

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 3: Personal (kind "mine"; saved-data redesign, owner
// approved 2026-09-27). One private record per signed-in person per name: a
// habit tracker, saved progress, preferences. It follows the person across
// devices, since it is kept on the server against their sign-in.
//
//   - Only that person reads and writes it, signed in on the site's own
//     address, from a page there (the __Host- cookie, same origin; writes
//     carry the CSRF header): GET, PUT (the whole record, one JSON object),
//     PATCH (the /state ops) and DELETE /v1/sites/{s}/data/{name}.
//   - Simple Host's owner tools never show it: not the site owner's key,
//     connector, owner app or exports, not the operator, not other visitors.
//     The owner sees how many people have a record and their total size
//     (list_data, from 3 people up), and can clear the name for everyone (a
//     clear, restorable for SAVED_DATA_UNDO_DAYS). The site's own pages run
//     in the visitor's browser and can read that visitor's record, so
//     Personal is as private as the site's pages are trustworthy (the copy
//     says so everywhere; code cannot enforce it while the owner writes the
//     pages). Stored private = true, so code that does not know the kinds
//     fails closed to owner-only.
//   - At most SAVED_DATA_PERSONAL_PEOPLE_MAX people per name (409
//     people_full for a new person; people who have a record keep saving).
//   - At most SAVED_DATA_PERSONAL_MAX_KB per person per name. Every change
//     keeps the earlier record for SAVED_DATA_UNDO_DAYS; the person lists and
//     restores their own (GET .../data/{name}/history, POST
//     .../history/{id}/restore).
//   - It goes into the person's own "Download my data" and is erased with
//     their account (it is their row: collection_items.submitted_by).
//
// Each person has one row per name for good (db.SavePersonal writes over a
// deleted one), so a restore never gives anyone two records.

// writePersonalOnly is the answer to anyone asking for someone's Personal
// records who is not that person: the owner's key and connector included.
func writePersonalOnly(w http.ResponseWriter, name string) {
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusForbidden, errorResponse{
		Error: fmt.Sprintf("%q is Personal: each signed-in visitor's own private record, read and changed only by that person on the site (SH.data(%q, 'personal')). "+
			"Simple Host's owner tools never show a person's record: the site owner sees how many people have one and their size (list_data, from 3 people up) and can clear it for everyone. "+
			"The site's own pages run in the visitor's browser and can read that visitor's record", name, name),
		Code: "personal_data",
	})
}

// isPersonalName reports whether name on siteID is declared Personal (500
// written on failure: ok=false).
func (h *SiteHandler) isPersonalName(w http.ResponseWriter, r *http.Request, siteID, name string) (personal, ok bool) {
	set, err := db.GetDataSettings(r.Context(), h.database, siteID, name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return false, false
	}
	return set.Kind == db.KindPersonal, true
}

// refusePersonal writes the personal_data refusal when name is Personal.
// true = the request was answered (refused, or failed).
func (h *SiteHandler) refusePersonal(w http.ResponseWriter, r *http.Request, siteID, name string) bool {
	if name == "" {
		return false
	}
	personal, ok := h.isPersonalName(w, r, siteID, name)
	if !ok {
		return true
	}
	if personal {
		writePersonalOnly(w, name)
		return true
	}
	return false
}

// writeJSONETag answers v with a weak ETag of its bytes, and 304 when the
// request's If-None-Match already names it (pages poll a board or a record
// cheaply with it).
func writeJSONETag(w http.ResponseWriter, r *http.Request, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `W/"` + hex.EncodeToString(sum[:12]) + `"`
	w.Header().Set("ETag", etag)
	for _, t := range strings.Split(r.Header.Get("If-None-Match"), ",") {
		if t = strings.TrimSpace(t); t == etag || t == "*" || "W/"+t == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func personalBody(name string, it db.CollectionItem, found bool) map[string]any {
	out := map[string]any{"name": name, "kind": db.KindPersonal, "data": nil, "version": 0}
	if found {
		out["data"] = it.Data
		out["version"] = it.Version
	}
	return out
}

// getPersonal is GET .../data/{name} on a Personal name: the signed-in
// visitor's own record ({"data": null} before they save one). The owner's key
// gets ?count=1 (how many people have one) and nothing else.
func (h *SiteHandler) getPersonal(w http.ResponseWriter, r *http.Request, siteID, name string, ownerKey bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	if ownerKey || h.ownerBrowserView(r, siteID) {
		if r.URL.Query().Get("count") != "" {
			n, err := db.CountLiveItems(r.Context(), h.database, siteID, name)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			// One or two people: "fewer than 3", never the number.
			if n > 0 && n < db.PersonalFewMax {
				writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": 0, "few": true})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": n})
			return
		}
		if ownerKey {
			writePersonalOnly(w, name)
			return
		}
	}
	sess, ok := h.visitorOnSite(w, r, siteID, false)
	if !ok {
		return
	}
	it, found, err := db.GetPersonal(r.Context(), h.database, siteID, name, sess.UserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSONETag(w, r, personalBody(name, it, found))
}

// personalSite resolves {sitename}/{coll} for the Personal routes that only a
// Personal name answers (PATCH, DELETE, history): the Origin gate, the site, a
// Personal name. Writes the refusal.
func (h *SiteHandler) personalSite(w http.ResponseWriter, r *http.Request) (siteID, siteName, name string, ok bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName = strings.TrimSpace(r.PathValue("sitename"))
	name = strings.TrimSpace(r.PathValue("coll"))
	if !h.collectionGate(w, r, siteName, name) {
		return "", "", "", false
	}
	var err error
	if siteID, err = h.resolveSiteID(r, siteName); err != nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return "", "", "", false
	}
	if h.refuseSuspendedSiteID(w, r, siteID) || h.refuseOffline(w, r, siteID) {
		return "", "", "", false
	}
	set, ok := h.dataSettings(w, r, siteID, name)
	if !ok {
		return "", "", "", false
	}
	switch set.Kind {
	case db.KindPersonal:
		return siteID, siteName, name, true
	case db.KindContent:
		writeWrongKind(w, name, set.Kind, "the owner replaces it whole with PUT /v1/sites/"+siteName+"/data/"+name)
	case db.KindEntries, db.KindBoard:
		writeWrongKind(w, name, set.Kind, "change or delete one item with PATCH or DELETE /v1/sites/"+siteName+"/data/"+name+"/items/{id}; the owner empties the whole list with DELETE /v1/sites/"+siteName+"/collections/"+name)
	default:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("%q is not Personal: the site owner declares it first with PUT /v1/sites/%s/data/%s/kind and {\"kind\": \"mine\"} (connector: declare_data)", name, siteName, name),
			Code:  "declare_first",
		})
	}
	return "", "", "", false
}

// personalWriter is the signed-in visitor writing their own record: on the
// site's own address, with the CSRF header, allowed to save here (the owner
// always is), within the per-address write rate. A key never writes anyone's
// record. Writes the refusal.
func (h *SiteHandler) personalWriter(w http.ResponseWriter, r *http.Request, siteID, name string, needSaver bool) (db.Actor, bool) {
	if r.Header.Get("X-API-Key") != "" {
		writePersonalOnly(w, name)
		return db.Actor{}, false
	}
	if h.refuseOffline(w, r, siteID) {
		return db.Actor{}, false
	}
	if previewReferer(r) {
		writePreviewReadOnly(w)
		return db.Actor{}, false
	}
	sess, ok := h.visitorOnSite(w, r, siteID, true)
	if !ok {
		return db.Actor{}, false
	}
	if needSaver && !h.personSaverOK(w, r, siteID, sess.UserID) {
		return db.Actor{}, false
	}
	if !h.allowAppend(w, r) {
		return db.Actor{}, false
	}
	return h.withAuthorEmail(r.Context(), db.Actor{ID: sess.UserID, Kind: actorVisitor}), true
}

// savePersonal writes the visitor's record with next (from their live
// record, nil when none) and answers it. body is what idempotency hashes.
func (h *SiteHandler) savePersonal(w http.ResponseWriter, r *http.Request, siteID, name, route string, actor db.Actor, body []byte, next func(cur json.RawMessage) (json.RawMessage, error)) {
	claim, handled := h.idemBegin(w, r, siteID, route, actor, body, func(prev db.IdempotentResponse) {
		it, found, err := db.GetPersonal(r.Context(), h.database, siteID, name, actor.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		writeJSON(w, prev.Status, personalBody(name, it, found))
	})
	if handled {
		return
	}
	defer h.idemEnd(r, claim)
	it, err := db.SavePersonal(r.Context(), h.database, siteID, name, actor, h.siteMaxBytes(), h.savedData.PersonalPeopleMax, next)
	h.boundHistory(r, siteID)
	var reply *patchReply
	switch {
	case errors.As(err, &reply):
		writeJSON(w, reply.status, errorResponse{Error: reply.msg, Code: reply.code})
	case errors.Is(err, sql.ErrNoRows):
		// The name stopped being Personal while this ran.
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q is not Personal any more", name), Code: "wrong_kind"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case errors.Is(err, db.ErrPeopleFull):
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("%q already keeps a record for %d people, the most one Personal name holds, so it takes no one new; ask the site owner", name, h.savedData.PersonalPeopleMax),
			Code:  "people_full",
		})
	case err != nil:
		log.Printf("personal save site_id=%s name=%s: %v", siteID, name, err)
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		h.idemSave(r, claim, http.StatusOK, "", it.ID)
		writeJSON(w, http.StatusOK, personalBody(name, it, true))
	}
}

func (h *SiteHandler) personalTooLarge() *patchReply {
	return &patchReply{http.StatusRequestEntityTooLarge, fmt.Sprintf("a personal record is at most %d KB", h.savedData.PersonalMaxKB), "item_too_large"}
}

// putPersonal is PUT .../data/{name} on a Personal name (from putContent):
// the visitor replaces their whole record with one JSON object.
func (h *SiteHandler) putPersonal(w http.ResponseWriter, r *http.Request, siteID, name string) {
	actor, ok := h.personalWriter(w, r, siteID, name, true)
	if !ok {
		return
	}
	maxBytes := int64(h.savedData.PersonalMaxKB) << 10
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			rep := h.personalTooLarge()
			writeJSON(w, rep.status, errorResponse{Error: rep.msg, Code: rep.code})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a personal record is one JSON object, e.g. {\"streak\": 3}", Code: "not_an_object"})
		return
	}
	doc := json.RawMessage(bytes.TrimSpace(raw))
	h.savePersonal(w, r, siteID, name, "PUT data/"+name, actor, raw, func(json.RawMessage) (json.RawMessage, error) { return doc, nil })
}

// patchData is PATCH /v1/sites/{s}/data/{name}: the visitor changes their
// Personal record with the /state ops (set, inc, append, remove,
// removeWhere), starting from {} when they have none.
func (h *SiteHandler) patchData(w http.ResponseWriter, r *http.Request) {
	siteID, _, name, ok := h.personalSite(w, r)
	if !ok {
		return
	}
	actor, ok := h.personalWriter(w, r, siteID, name, true)
	if !ok {
		return
	}
	maxBytes := int64(h.savedData.PersonalMaxKB) << 10
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes+4096))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large", Code: "item_too_large"})
		return
	}
	var req struct {
		Ops []stateOp `json:"ops"`
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		err = dec.Decode(&req.Ops)
	} else {
		err = dec.Decode(&req)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"ops": [{"op": "set", "path": "theme", "value": "dark"}]}`})
		return
	}
	if len(req.Ops) == 0 || len(req.Ops) > maxStateOps {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: fmt.Sprintf("ops is required: 1 to %d of them", maxStateOps)})
		return
	}
	h.savePersonal(w, r, siteID, name, "PATCH data/"+name, actor, body, func(cur json.RawMessage) (json.RawMessage, error) {
		root, err := stateRootObject(cur)
		if err != nil {
			return nil, &patchReply{http.StatusConflict, "this record is not a JSON object; replace it whole with PUT", "not_an_object"}
		}
		if err := applyStateOps(root, req.Ops); err != nil {
			return nil, &patchReply{http.StatusBadRequest, err.Error(), ""}
		}
		next, err := json.Marshal(root)
		if err != nil {
			return nil, err
		}
		if int64(len(next)) > maxBytes && len(next) > len(cur) {
			return nil, h.personalTooLarge()
		}
		return next, nil
	})
}

// deleteData is DELETE /v1/sites/{s}/data/{name}: the visitor deletes their
// own Personal record. They can bring it back from their history for
// SAVED_DATA_UNDO_DAYS; their account's deletion erases it for good.
func (h *SiteHandler) deleteData(w http.ResponseWriter, r *http.Request) {
	siteID, _, name, ok := h.personalSite(w, r)
	if !ok {
		return
	}
	// A blocked person can still delete what is theirs.
	actor, ok := h.personalWriter(w, r, siteID, name, false)
	if !ok {
		return
	}
	it, found, err := db.GetPersonal(r.Context(), h.database, siteID, name, actor.ID)
	if err == nil && found {
		found, err = db.SoftDeleteItem(r.Context(), h.database, siteID, name, it.ID, actor)
	}
	// The owner's clear took it: the person's delete still counts, so the
	// owner's Restore (only what a clear took) leaves it deleted.
	if err == nil && !found {
		found, err = db.ForgetClearedPersonal(r.Context(), h.database, siteID, name, actor)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "you have no record here", Code: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true, "restorable_days": h.savedData.UndoDays})
}

// personalHistoryTarget is the visitor's own record for the history routes:
// its item id (0 when they never saved one).
func (h *SiteHandler) personalHistoryTarget(w http.ResponseWriter, r *http.Request, write bool) (siteID, name string, itemID int64, actor db.Actor, ok bool) {
	siteID, _, name, ok = h.personalSite(w, r)
	if !ok {
		return "", "", 0, actor, false
	}
	if write {
		if actor, ok = h.personalWriter(w, r, siteID, name, true); !ok {
			return "", "", 0, actor, false
		}
	} else {
		if !h.allowRead(w, r, siteID) {
			return "", "", 0, actor, false
		}
		sess, ok := h.visitorOnSite(w, r, siteID, false)
		if !ok {
			return "", "", 0, actor, false
		}
		actor = db.Actor{ID: sess.UserID, Kind: actorVisitor}
	}
	itemID, err := db.PersonalItemID(r.Context(), h.database, siteID, name, actor.ID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return "", "", 0, actor, false
	}
	return siteID, name, itemID, actor, true
}

// personalHistory is GET .../data/{name}/history: the changes to the
// visitor's own record, newest first, without their values.
func (h *SiteHandler) personalHistory(w http.ResponseWriter, r *http.Request) {
	siteID, name, itemID, _, ok := h.personalHistoryTarget(w, r, false)
	if !ok {
		return
	}
	limit, before := pageArgs(r)
	entries := []db.HistoryEntry{}
	if itemID != 0 {
		var err error
		if entries, err = db.ListItemHistory(r.Context(), h.database, siteID, name, itemID, limit, before); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
	}
	next := historyNext(entries, limit)
	for i := range entries {
		entries[i] = ownHistoryView(entries[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "history": entries, "next": next, "undo_days": h.savedData.UndoDays})
}

// ownHistoryView is a change as its visitor sees it: who made it only when it
// was them (a clear by the site owner or the operator names nobody; by_kind
// says which).
func ownHistoryView(e db.HistoryEntry) db.HistoryEntry {
	if e.ByKind != actorVisitor {
		e.By = ""
	}
	return e
}

// ownHistoryEntry reads change id of name when it belongs to itemID.
func (h *SiteHandler) ownHistoryEntry(w http.ResponseWriter, r *http.Request, siteID, name string, itemID int64) (db.HistoryEntry, bool) {
	id, valid := pathID(r, "id")
	if !valid || itemID == 0 {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return db.HistoryEntry{}, false
	}
	e, err := db.ReadHistoryEntry(r.Context(), h.database, siteID, db.HistoryList, name, id)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return e, false
	}
	if err != nil || e.ItemID == nil || *e.ItemID != itemID {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		return e, false
	}
	return e, true
}

// personalHistoryEntry is GET .../data/{name}/history/{id}: one change to the
// visitor's own record, with the record from before it.
func (h *SiteHandler) personalHistoryEntry(w http.ResponseWriter, r *http.Request) {
	siteID, name, itemID, _, ok := h.personalHistoryTarget(w, r, false)
	if !ok {
		return
	}
	e, ok := h.ownHistoryEntry(w, r, siteID, name, itemID)
	if !ok {
		return
	}
	if e.Value == nil {
		e.Value = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, ownHistoryView(e))
}

// restorePersonal is POST .../data/{name}/history/{id}/restore: the visitor
// puts their record back as it was before that change (a delete or clear
// brings it back). A restore is itself a change.
func (h *SiteHandler) restorePersonal(w http.ResponseWriter, r *http.Request) {
	siteID, name, itemID, actor, ok := h.personalHistoryTarget(w, r, true)
	if !ok {
		return
	}
	e, ok := h.ownHistoryEntry(w, r, siteID, name, itemID)
	if !ok {
		return
	}
	it, err := db.RestoreItemVersion(r.Context(), h.database, siteID, name, e.ID, actor, h.siteMaxBytes(), 0)
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
		out := personalBody(name, it, true)
		out["restored"] = e.ID
		writeJSON(w, http.StatusOK, out)
	}
}
