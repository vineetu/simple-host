package handler

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/config"
	db "github.com/vsriram/simple-host/internal/db"
)

// Saved data, step 4: Shared board (kind "board"; saved-data redesign, owner
// approved 2026-09-27). A list a group keeps together: a shopping list, a
// kanban, a sign-up grid.
//
//   - Anyone who can open the site reads it (who added each item stays the
//     owner's to see), as a public list.
//   - Signed-in visitors allowed to save (the site's who-may-save setting)
//     add items (POST .../data/{name}), and change (PATCH) and delete (DELETE
//     .../data/{name}/items/{id}) any item, one at a time. A change may name
//     the item's version (If-Match); a stale one is refused with 409
//     version_conflict and the current item, so two people never overwrite
//     each other unseen. Whoever deleted an item can bring it back for
//     SAVED_DATA_WITHDRAW_UNDO_MINUTES; the owner restores anything from
//     History and Recently deleted for SAVED_DATA_UNDO_DAYS.
//   - Only the owner empties or replaces the whole board (the list's clear,
//     with its typed confirmation); nobody else has a route that touches more
//     than one item.
//   - Items are JSON objects of at most SAVED_DATA_BOARD_ITEM_MAX_KB, at most
//     SAVED_DATA_BOARD_MAX live per name (the owner's restores too); visitor
//     adds, changes and deletes share the per-address write rate
//     (SAVED_DATA_APPEND_PER_MIN) and, per signed-in person whatever their
//     address, SAVED_DATA_BOARD_WRITES_PER_MIN.
//   - The owner's "Restore all" on a board names a window (within_minutes):
//     after vandalism it brings back what went since then, not what people
//     deleted on purpose before.
//   - No live feed (decided later): a page polls GET with If-None-Match and
//     gets 304 while nothing changed.

// boardWriter is who may add to, change or delete from a board: the owner
// (key, connector, or signed in on the site, as for Page info), or a visitor
// signed in on the site's own address with the CSRF header who is allowed to
// save here, within the per-address write rate. Writes the refusal.
func (h *SiteHandler) boardWriter(w http.ResponseWriter, r *http.Request, siteID string) (db.Actor, bool) {
	if r.Header.Get("X-API-Key") != "" || h.ownerBrowserView(r, siteID) {
		return h.contentWriter(w, r, siteID)
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
	if !h.personSaverOK(w, r, siteID, sess.UserID) || !h.allowAppend(w, r) {
		return db.Actor{}, false
	}
	if h.boardLimiter != nil && !h.boardLimiter.allow("u:"+sess.UserID) {
		tooManyRequests(w)
		return db.Actor{}, false
	}
	return h.withAuthorEmail(r.Context(), db.Actor{ID: sess.UserID, Kind: actorVisitor}), true
}

func (h *SiteHandler) boardItemMax() int64 { return int64(h.savedData.BoardItemMaxKB) << 10 }

func (h *SiteHandler) writeBoardFull(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, errorResponse{
		Error: fmt.Sprintf("this board is full (%d items); delete some to make room", h.savedData.BoardMax),
		Code:  "list_full",
	})
}

// boardItemOut is an item as a board answers it: its version, and never who
// added it (that is the owner's, in the owner's reads).
func boardItemOut(it db.CollectionItem) db.CollectionItem {
	it.Data = withoutSubmitter(it.Data)
	it.By = ""
	return it
}

// appendBoard is POST .../data/{name} (or .../collections/{name}) on a board:
// one new item. Answers after writing.
func (h *SiteHandler) appendBoard(w http.ResponseWriter, r *http.Request, siteID string, set db.DataSettings) {
	w.Header().Set("Cache-Control", "private, no-store")
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	actor, ok := h.boardWriter(w, r, siteID)
	if !ok {
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.boardItemMax()))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: fmt.Sprintf("a board item is at most %d KB", h.savedData.BoardItemMaxKB), Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	var obj map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&obj); err != nil || obj == nil || dec.More() {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `a board item is one JSON object, e.g. {"text": "milk", "done": false}`, Code: "not_an_object"})
		return
	}
	if !storableJSON(w, raw) {
		return
	}
	body := json.RawMessage(bytes.TrimSpace(raw))
	// The server's own stamp keys are never the visitor's to set.
	stamped := false
	for _, k := range privateStampKeys {
		if _, ok := obj[k]; ok {
			delete(obj, k)
			stamped = true
		}
	}
	if stamped {
		if body, err = json.Marshal(obj); err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
	}
	claim, handled := h.idemBegin(w, r, siteID, "POST collections/"+set.Name, actor, body, h.replayItem(w, r, siteID, set.Name))
	if handled {
		return
	}
	defer h.idemEnd(r, claim)
	if !h.siteHasRoom(w, r, siteID, int64(len(body))) {
		return
	}
	it, _, err := db.AppendEntry(r.Context(), h.database, siteID, set.Name, db.KindBoard, set.Private, body, actor, false, h.savedData.BoardMax)
	switch {
	case errors.Is(err, db.ErrNameFull):
		h.writeBoardFull(w)
		return
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, db.ErrKindChanged):
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q is not a board any more", set.Name), Code: "wrong_kind"})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	it.Version = 1
	h.watchItemSize(r.Context(), siteID, len(body))
	h.idemSave(r, claim, http.StatusCreated, "", it.ID)
	writeJSON(w, http.StatusCreated, boardItemOut(it))
}

// updateBoardItem is PATCH .../data/{name}/items/{id} on a board: the fields
// sent are merged in (a null removes one). If-Match: "<version>" makes it
// conditional: 409 version_conflict with the current item when it changed.
func (h *SiteHandler) updateBoardItem(w http.ResponseWriter, r *http.Request, siteID string, set db.DataSettings) {
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	actor, ok := h.boardWriter(w, r, siteID)
	if !ok {
		return
	}
	id, valid := pathID(r, "id")
	if !valid {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
		return
	}
	var ifVersion int64
	if v := strings.TrimSpace(r.Header.Get("If-Match")); v != "" {
		n, ok := parseIfMatch(v)
		if !ok || n <= 0 {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `If-Match is the item's version, e.g. "3"`})
			return
		}
		ifVersion = int64(n)
	}
	maxBytes := h.boardItemMax()
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
	var patch map[string]json.RawMessage
	if err == nil {
		dec := json.NewDecoder(bytes.NewReader(raw))
		if err = dec.Decode(&patch); err == nil && (patch == nil || dec.More()) {
			err = errors.New("not one object")
		}
	}
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send a JSON object of the fields to change, e.g. {"done": true}`})
		return
	}
	if !storableJSON(w, raw) {
		return
	}
	errTooLarge := errors.New("too large")
	errNotObject := errors.New("not an object")
	it, err := db.UpdateItemVersioned(r.Context(), h.database, siteID, set.Name, id, ifVersion, actor, h.siteMaxBytes(), func(old json.RawMessage) (json.RawMessage, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(old, &fields); err != nil || fields == nil {
			return nil, errNotObject
		}
		for k, v := range patch {
			if slices.Contains(privateStampKeys, k) {
				continue
			}
			if string(bytes.TrimSpace(v)) == "null" {
				delete(fields, k)
				continue
			}
			fields[k] = v
		}
		next, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		if int64(len(next)) > maxBytes && len(next) > len(old) {
			return nil, errTooLarge
		}
		return next, nil
	})
	h.boundHistory(r, siteID)
	switch {
	case errors.Is(err, db.ErrVersionConflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("someone changed this item since version %d; here it is now (version %d): apply your change to it and send it again", ifVersion, it.Version),
			"code":  "version_conflict", "item": boardItemOut(it),
		})
	case errors.Is(err, sql.ErrNoRows):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
	case errors.Is(err, errTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: fmt.Sprintf("a board item is at most %d KB", h.savedData.BoardItemMaxKB), Code: "item_too_large"})
	case errors.Is(err, errNotObject):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "this item is not a JSON object, so it has no fields to change; delete it and add a new one", Code: "not_an_object"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, boardItemOut(it))
	}
}

// deleteBoardItem is DELETE .../data/{name}/items/{id} on a board by a
// visitor (the owner goes to the list's owner delete): the item moves to
// Recently deleted; whoever deleted it can undo that for
// SAVED_DATA_WITHDRAW_UNDO_MINUTES (POST .../items/{id}/undo).
func (h *SiteHandler) deleteBoardItem(w http.ResponseWriter, r *http.Request, siteID string, set db.DataSettings) {
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	actor, ok := h.boardWriter(w, r, siteID)
	if !ok {
		return
	}
	id, valid := pathID(r, "id")
	if !valid {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
		return
	}
	found, err := db.SoftDeleteItem(r.Context(), h.database, siteID, set.Name, id, actor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if !found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id, "undo_minutes": h.savedData.WithdrawUndoMinutes})
}

// undoBoardDelete is POST .../data/{name}/items/{id}/undo on a board: the
// person who deleted the item brings it back within
// SAVED_DATA_WITHDRAW_UNDO_MINUTES (the board's cap still holds).
func (h *SiteHandler) undoBoardDelete(w http.ResponseWriter, r *http.Request, siteID string, set db.DataSettings) {
	if h.refuseSuspendedSiteID(w, r, siteID) {
		return
	}
	actor, ok := h.boardWriter(w, r, siteID)
	if !ok {
		return
	}
	id, valid := pathID(r, "id")
	if !valid {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
		return
	}
	window := time.Duration(h.savedData.WithdrawUndoMinutes) * time.Minute
	it, done, err := db.UndoOwnWithdrawal(r.Context(), h.database, siteID, set.Name, id, actor.ID, window, true, false, h.savedData.BoardMax, actor, h.siteMaxBytes(), true)
	switch {
	case errors.Is(err, db.ErrNameFull):
		h.writeBoardFull(w)
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	case !done:
		writeJSON(w, http.StatusConflict, errorResponse{
			Error: fmt.Sprintf("only an item you deleted yourself in the last %s can be brought back here; the site owner can restore older ones", config.Span(window)),
			Code:  "undo_expired",
		})
	default:
		writeJSON(w, http.StatusOK, boardItemOut(it))
	}
}
