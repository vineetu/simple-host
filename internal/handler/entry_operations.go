package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/legacy"
	"github.com/vsriram/simple-host/internal/model"
)

func (h *SiteHandler) entrySvc(r *http.Request) *legacy.Adapter {
	deps := h.legacyCollectionRequestDeps(r)
	deps.ContentOwnerBrowser = func(id string) bool { return h.ownerBrowserView(r, id) }
	deps.VisitorResolveKey = h.resolveWriterKey
	deps.ContentAuthor = h.withAuthorEmail
	deps.VisitorSessionIdle = h.limits.VisitorSessionIdle
	deps.CollectionStrictSession = func(id string) legacy.CollectionBrowserSession {
		sess, ok := h.strictVisitorSession(r, id)
		return legacy.CollectionBrowserSession{UserID: sess.UserID, ID: sess.ID, OK: ok}
	}
	deps.CollectionPrivateHome = func(id string) (legacy.CollectionPrivateHome, bool, error) {
		info, here, err := h.onOwnDomain(r, id)
		return legacy.CollectionPrivateHome{Host: info.Host, OwnerID: info.OwnerID, IsDomain: info.IsDomain}, here, err
	}
	deps.CollectionAppendLimit = func() (bool, map[string][]string) { return collectionRateLimit(h.appendLimiter, clientIP(r)) }
	deps.CollectionBoardLimit = func(user string) bool { return h.boardLimiter == nil || h.boardLimiter.allow("u:"+user) }
	deps.MaxBytes = h.siteMaxBytes
	deps.BoundHistory = h.boundHistoryContext
	return legacy.New(h.database, h.legacyStorageCutoff, deps)
}

func (h *SiteHandler) entryRequest(r *http.Request) legacy.EntryRequest {
	id, valid := pathID(r, "id")
	version, validVersion := 0, true
	if v := strings.TrimSpace(r.Header.Get("If-Match")); v != "" {
		version, validVersion = parseIfMatch(v)
		validVersion = validVersion && version > 0
	}
	name := strings.TrimSpace(r.PathValue("coll"))
	return legacy.EntryRequest{
		SiteName: strings.TrimSpace(r.PathValue("sitename")), Name: name, Handle: strings.TrimSpace(r.PathValue("handle")), Key: r.Header.Get("X-API-Key"), Method: r.Method, Path: r.URL.Path,
		ID: id, ValidID: valid, ValidName: validCollectionName.MatchString(name), DeclareFirst: h.savedData.DefaultKind == legacyDeclareFirst,
		ContentHost: strings.EqualFold(requestHostName(r), h.contentHost), CSRFValid: r.Header.Get(visitorCSRFHeader) == visitorCSRFValue, VisitorPass: r.Header.Get(visitorPassHeader) != "", Preview: previewReferer(r),
		EntryMaxKB: h.savedData.EntryMaxKB, BoardMax: h.savedData.BoardMax, BoardItemMaxKB: h.savedData.BoardItemMaxKB, MaxItems: h.savedData.EntriesMax, ListMaxBytes: maxCollectionItemSize, MaxBytes: h.siteMaxBytes(), Idle: h.limits.VisitorSessionIdle, Window: time.Duration(h.savedData.WithdrawUndoMinutes) * time.Minute,
		IfVersion: int64(version), ValidIfVersion: validVersion,
		ReadPatch: func(limit int64) ([]byte, error) {
			raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, limit))
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, &legacy.Error{Code: "item_too_large"}
			}
			return raw, err
		},
	}
}

// writeEntrySelection maps preparation errors and the completed owner/board result.
func (h *SiteHandler) writeEntrySelection(w http.ResponseWriter, in legacy.EntryRequest, out legacy.EntryResult, err error, operation string) bool {
	for k, v := range out.Headers {
		w.Header()[k] = v
	}
	var e *legacy.Error
	var stageErr *legacy.EntryStageError
	if errors.As(err, &stageErr) && stageErr.Stage == "preparation" && !errors.As(err, &e) {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return true
	}
	if errors.As(err, &e) {
		switch e.Code {
		case "site_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return true
		case "private_not_found":
			writePrivateNotFound(w)
			return true
		case "personal_item":
			writeWrongKind(w, in.Name, out.Settings.Kind, "each visitor changes their own record with PUT, PATCH or DELETE /v1/sites/"+in.SiteName+"/data/"+in.Name)
			return true
		case "personal_only":
			writePersonalOnly(w, in.Name)
			return true
		case "site_suspended":
			writeSiteSuspended(w, e.Message)
			return true
		case "site_offline":
			writeSiteOffline(w)
			return true
		case "visitor_auth_required":
			writeVisitorAuthRequired(w)
			return true
		}
	}
	if out.Route == "visitor" || out.Route == "" {
		return false
	}
	board := out.Route == "board"
	switch {
	case errors.As(err, &e):
		switch e.Code {
		case "csrf_required":
			if board {
				writeJSON(w, http.StatusForbidden, errorResponse{Error: "missing CSRF header", Code: "csrf_required"})
			} else {
				writeJSON(w, http.StatusForbidden, map[string]string{"code": "csrf_required", "error": "missing CSRF header"})
			}
		case "site_not_found":
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
		case "invalid_api_key":
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid API key", Code: "invalid_api_key"})
		case "owner_only":
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "only the site owner can change page info", Code: "owner_only"})
		case "preview_read_only":
			writePreviewReadOnly(w)
		case "account_suspended":
			writeAccountSuspended(w)
		case "not_allowed_to_save":
			writeNotAllowedToSave(w)
		case "rate_limited":
			tooManyRequests(w)
		case "append_only":
			writeJSON(w, http.StatusConflict, map[string]string{"error": "items in a public list cannot be edited, only deleted; only items in a private list can be edited", "code": "append_only"})
		case "selected_not_found":
			if board {
				writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
			} else {
				writePrivateNotFound(w)
			}
		case "invalid_if_match":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `If-Match is the item's version, e.g. "3"`})
		case "item_too_large":
			signalLegacyBodyTooLarge(w)
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "item too large", Code: "item_too_large"})
		case "selected_invalid_patch":
			msg := `send a JSON object of the fields to change, e.g. {"status": "done"}`
			if board {
				msg = `send a JSON object of the fields to change, e.g. {"done": true}`
			}
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: msg})
		case "invalid_json":
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: `the JSON holds text that cannot be saved: a NUL character (\u0000), half of a surrogate pair (\ud800 alone) or bytes that are not UTF-8. Remove it and send again`, Code: "invalid_json"})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
	case errors.Is(err, model.ErrVersionConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": fmt.Sprintf("someone changed this item since version %d; here it is now (version %d): apply your change to it and send it again", in.IfVersion, out.Item.Version), "code": "version_conflict", "item": boardItemOut(out.Item)})
	case errors.Is(err, sql.ErrNoRows):
		if operation != "update" {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		} else if board {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "no such item", Code: "not_found"})
		} else {
			writePrivateNotFound(w)
		}
	case errors.Is(err, model.ErrLegacyEntryTooLarge):
		msg := "item too large"
		if board {
			msg = fmt.Sprintf("a board item is at most %d KB", in.BoardItemMaxKB)
		}
		writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: msg, Code: "item_too_large"})
	case errors.Is(err, model.ErrLegacyEntryNotObject):
		msg := "this item is not a JSON object, so it has no fields to change; delete it instead"
		if board {
			msg = "this item is not a JSON object, so it has no fields to change; delete it and add a new one"
		}
		writeJSON(w, http.StatusConflict, errorResponse{Error: msg, Code: "not_an_object"})
	case errors.Is(err, model.ErrNameFull):
		h.writeBoardFull(w)
	case errors.Is(err, model.ErrSiteFull):
		h.writeSiteFull(w)
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	case operation == "withdraw":
		if board {
			writeJSON(w, http.StatusOK, map[string]any{"deleted": in.ID, "undo_minutes": h.savedData.WithdrawUndoMinutes})
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	case operation == "undo" && !out.Done:
		writeJSON(w, http.StatusConflict, errorResponse{Error: fmt.Sprintf("only an item you deleted yourself in the last %s can be brought back here; the site owner can restore older ones", storeSpan(in.Window)), Code: "undo_expired"})
	default:
		item := out.Item
		if board {
			item = boardItemOut(item)
		}
		writeJSON(w, http.StatusOK, item)
	}
	return true
}
