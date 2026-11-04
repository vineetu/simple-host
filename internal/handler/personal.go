package handler

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/app/legacy"
	"github.com/vsriram/simple-host/internal/config"
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
	result, err := h.legacyCollectionAppendSvc(r).GetPersonal(r.Context(), legacy.GetPersonalInput{SiteID: siteID, Name: name, OwnerKey: ownerKey, Count: r.URL.Query().Get("count") != "", ContentHost: strings.EqualFold(requestHostName(r), h.contentHost)})
	if err != nil {
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			switch opErr.Code {
			case "personal_data":
				writePersonalOnly(w, name)
				return
			case "visitor_auth_required":
				writeVisitorAuthRequired(w)
				return
			case "account_suspended":
				writeAccountSuspended(w)
				return
			}
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if result.Counted {
		if result.Few {
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": 0, "few": true})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"name": name, "count": result.Count})
		}
		return
	}
	writeJSONETag(w, r, personalBody(name, result.Item, result.Found))
}

// writePersonalSaveResult maps a personal save result to its wire response.
func (h *SiteHandler) writePersonalSaveResult(w http.ResponseWriter, siteID, name string, result legacy.SavePersonalResult, err error) {
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		} else {
			writeJSON(w, result.ReplayStatus, personalBody(name, result.Item, result.Found))
		}
		return
	}
	var idemErr *legacy.Error
	if errors.As(err, &idemErr) {
		switch idemErr.Code {
		case "idempotency_internal":
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
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
	var reply *legacy.PersonalPatchError
	it := result.Item
	switch {
	case errors.As(err, &reply):
		status := http.StatusBadRequest
		if reply.Kind == "too_large" {
			status = http.StatusRequestEntityTooLarge
		} else if reply.Kind == "conflict" {
			status = http.StatusConflict
		}
		writeJSON(w, status, errorResponse{Error: reply.Message, Code: reply.WireCode})
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
		writeJSON(w, http.StatusOK, personalBody(name, it, true))
	}
}

func (h *SiteHandler) personalTooLarge() *patchReply {
	return &patchReply{http.StatusRequestEntityTooLarge, fmt.Sprintf("a personal record is at most %d KB", h.savedData.PersonalMaxKB), "item_too_large"}
}

// putPersonal is PUT .../data/{name} on a Personal name (from putContent):
// the visitor replaces their whole record with one JSON object.
func (h *SiteHandler) putPersonal(w http.ResponseWriter, r *http.Request, siteID, name string) {
	result, err := h.legacyPersonalPutSvc(r).PutPersonal(r.Context(), legacy.PutPersonalInput{
		SiteID: siteID, Name: name, Key: r.Header.Get("X-API-Key"), IdempotencyKey: r.Header.Get("Idempotency-Key"), SuccessStatus: http.StatusOK,
		ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), Preview: previewReferer(r), VisitorPass: r.Header.Get(visitorPassHeader) != "", CSRF: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue,
		ReadBody: func() ([]byte, bool, error) {
			reader := http.MaxBytesReader(nil, r.Body, int64(h.savedData.PersonalMaxKB)<<10)
			body, err := io.ReadAll(reader)
			var maxErr *http.MaxBytesError
			return body, errors.As(err, &maxErr), err
		},
	})
	for key, values := range result.Headers {
		w.Header()[key] = values
	}
	var opErr *legacy.Error
	if errors.As(err, &opErr) {
		switch opErr.Code {
		case "personal_data":
			writePersonalOnly(w, name)
			return
		case "site_offline":
			writeSiteOffline(w)
			return
		case "preview_read_only":
			writePreviewReadOnly(w)
			return
		case "visitor_auth_required":
			writeVisitorAuthRequired(w)
			return
		case "account_suspended":
			writeAccountSuspended(w)
			return
		case "csrf_required":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
			return
		case "not_allowed_to_save":
			writeNotAllowedToSave(w)
			return
		case "rate_limited":
			tooManyRequests(w)
			return
		case "personal_item_too_large":
			signalLegacyBodyTooLarge(w)
			rep := h.personalTooLarge()
			writeJSON(w, rep.status, errorResponse{Error: rep.msg, Code: rep.code})
			return
		case "invalid_request_body":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		case "personal_not_object":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "a personal record is one JSON object, e.g. {\"streak\": 3}", Code: "not_an_object"})
			return
		case "invalid_json":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message, Code: "invalid_json"})
			return
		case "internal":
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
	}
	h.writePersonalSaveResult(w, siteID, name, result, err)
}

// patchData is PATCH /v1/sites/{s}/data/{name}: the visitor changes their
// Personal record with the /state ops (set, inc, append, remove,
// removeWhere), starting from {} when they have none.
func (h *SiteHandler) patchData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	req := h.personalRouteRequest(r)
	var maxBytes int64
	result, err := h.legacyPersonalPatchSvc(r, &maxBytes).PatchPersonal(r.Context(), legacy.PatchPersonalRequest{
		DeletePersonalRequest: req, IdempotencyKey: r.Header.Get("Idempotency-Key"), SuccessStatus: http.StatusOK,
		ReadBody: func() ([]byte, bool, error) {
			maxBytes = int64(h.savedData.PersonalMaxKB) << 10
			reader := http.MaxBytesReader(nil, r.Body, maxBytes+4096)
			body, err := io.ReadAll(reader)
			var maxErr *http.MaxBytesError
			return body, errors.As(err, &maxErr), err
		},
	})
	for key, values := range result.DeletePersonalResult.Headers {
		w.Header()[key] = values
	}
	if err != nil {
		if h.writePersonalRequestError(w, req.SiteName, req.Name, result.Kind, err) {
			return
		}
		var opErr *legacy.Error
		if errors.As(err, &opErr) {
			switch opErr.Code {
			case "patch_body_limit":
				signalLegacyBodyTooLarge(w)
				writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large", Code: "item_too_large"})
				return
			case "patch_body_too_large":
				writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large", Code: "item_too_large"})
				return
			case "invalid_json":
				writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message, Code: "invalid_json"})
				return
			case "internal":
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
		}
	}
	h.writePersonalSaveResult(w, result.SiteID, req.Name, result.SavePersonalResult, err)
}

// deleteData is DELETE /v1/sites/{s}/data/{name}: the visitor deletes their
// own Personal record. They can bring it back from their history for
// SAVED_DATA_UNDO_DAYS; their account's deletion erases it for good.
func (h *SiteHandler) deleteData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	siteName, name := strings.TrimSpace(r.PathValue("sitename")), strings.TrimSpace(r.PathValue("coll"))
	result, err := h.legacyPersonalPutSvc(r).DeletePersonalRequest(r.Context(), legacy.DeletePersonalRequest{
		SiteName: siteName, Name: name, DeclareFirst: h.savedData.DefaultKind == config.DefaultKindDeclareFirst,
		ActorFacts: legacy.CollectionWriteActor{Key: r.Header.Get("X-API-Key"), Preview: previewReferer(r), ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), CSRF: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue},
	})
	for key, values := range result.Headers {
		w.Header()[key] = values
	}
	if err != nil {
		if !h.writePersonalRequestError(w, siteName, name, result.Kind, err) {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	if !result.Found {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "you have no record here", Code: "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true, "restorable_days": h.savedData.UndoDays})
}

// writePersonalRequestError maps shared personal target and visitor refusals.
func (h *SiteHandler) writePersonalRequestError(w http.ResponseWriter, siteName, name, kind string, err error) bool {
	var opErr *legacy.Error
	if !errors.As(err, &opErr) {
		return false
	}
	switch opErr.Code {
	case "site_name_required", "invalid_collection":
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: opErr.Message})
		return true
	case "origin_not_allowed":
		writeOriginRefused(w)
		return true
	case "site_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		return true
	case "site_suspended":
		writeSiteSuspended(w, opErr.Message)
		return true
	case "site_offline":
		writeSiteOffline(w)
		return true
	case "personal_wrong_kind":
		switch kind {
		case db.KindContent:
			writeWrongKind(w, name, kind, "the owner replaces it whole with PUT /v1/sites/"+siteName+"/data/"+name)
		case db.KindEntries, db.KindBoard:
			writeWrongKind(w, name, kind, "change or delete one item with PATCH or DELETE /v1/sites/"+siteName+"/data/"+name+"/items/{id}; the owner empties the whole list with DELETE /v1/sites/"+siteName+"/collections/"+name)
		default:
			writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("%q is not Personal: the site owner declares it first with PUT /v1/sites/%s/data/%s/kind and {\"kind\": \"mine\"}", name, siteName, name), Code: "declare_first"})
		}
		return true
	case "personal_data":
		writePersonalOnly(w, name)
		return true
	case "preview_read_only":
		writePreviewReadOnly(w)
		return true
	case "visitor_auth_required":
		writeVisitorAuthRequired(w)
		return true
	case "account_suspended":
		writeAccountSuspended(w)
		return true
	case "csrf_required":
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
		return true
	case "rate_limited":
		tooManyRequests(w)
		return true
	}
	return false
}

// personalRouteRequest captures facts shared by personal history routes.
func (h *SiteHandler) personalRouteRequest(r *http.Request) legacy.DeletePersonalRequest {
	return legacy.DeletePersonalRequest{SiteName: strings.TrimSpace(r.PathValue("sitename")), Name: strings.TrimSpace(r.PathValue("coll")), DeclareFirst: h.savedData.DefaultKind == config.DefaultKindDeclareFirst,
		ActorFacts: legacy.CollectionWriteActor{Key: r.Header.Get("X-API-Key"), Preview: previewReferer(r), ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), VisitorPass: r.Header.Get(visitorPassHeader) != "", CSRF: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue}}
}

// personalHistory is GET .../data/{name}/history: the changes to the
// visitor's own record, newest first, without their values.
func (h *SiteHandler) personalHistory(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	req := h.personalRouteRequest(r)
	limit, before := pageArgs(r)
	result, err := h.legacyPersonalPutSvc(r).ReadPersonalHistory(r.Context(), legacy.PersonalHistoryRequest{DeletePersonalRequest: req, Limit: limit, Before: before})
	for key, values := range result.Headers {
		w.Header()[key] = values
	}
	if err != nil {
		if !h.writePersonalRequestError(w, req.SiteName, req.Name, result.Kind, err) {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	entries := result.Entries
	next := historyNext(entries, limit)
	for i := range entries {
		entries[i] = ownHistoryView(entries[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": req.Name, "history": entries, "next": next, "undo_days": h.savedData.UndoDays})
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

// personalHistoryEntry is GET .../data/{name}/history/{id}: one change to the
// visitor's own record, with the record from before it.
func (h *SiteHandler) personalHistoryEntry(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	req := h.personalRouteRequest(r)
	id, valid := pathID(r, "id")
	result, err := h.legacyPersonalPutSvc(r).ReadPersonalHistoryEntry(r.Context(), legacy.PersonalHistoryRequest{DeletePersonalRequest: req, ChangeID: id, ValidID: valid})
	for key, values := range result.Headers {
		w.Header()[key] = values
	}
	if err != nil {
		if h.writePersonalRequestError(w, req.SiteName, req.Name, result.Kind, err) {
			return
		}
		var opErr *legacy.Error
		if errors.As(err, &opErr) && opErr.Code == "history_not_found" {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
		} else {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	e := result.Entry
	if e.Value == nil {
		e.Value = json.RawMessage("null")
	}
	writeJSON(w, http.StatusOK, ownHistoryView(e))
}

// restorePersonal is POST .../data/{name}/history/{id}/restore: the visitor
// puts their record back as it was before that change (a delete or clear
// brings it back). A restore is itself a change.
func (h *SiteHandler) restorePersonal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	req := h.personalRouteRequest(r)
	id, valid := pathID(r, "id")
	result, err := h.legacyPersonalPutSvc(r).RestorePersonalRequest(r.Context(), legacy.PersonalHistoryRequest{DeletePersonalRequest: req, ChangeID: id, ValidID: valid})
	for key, values := range result.Headers {
		w.Header()[key] = values
	}
	if err != nil && h.writePersonalRequestError(w, req.SiteName, req.Name, result.Kind, err) {
		return
	}
	var opErr *legacy.Error
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.As(err, &opErr) && opErr.Code == "history_not_found":
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such change", Code: "not_found"})
	case errors.Is(err, db.ErrNoEarlierValue):
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error(), Code: "nothing_to_restore"})
	case errors.Is(err, db.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		out := personalBody(req.Name, result.Item, true)
		out["restored"] = result.RestoredID
		writeJSON(w, http.StatusOK, out)
	}
}
