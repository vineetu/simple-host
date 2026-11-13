package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	appstorage "github.com/vsriram/simple-host/internal/app/storage"
)

// The SQLite table routes. Visitors send an action and values, never SQL;
// the server builds every statement from the table's matrix
// (site_storage_access.go):
//
//	GET    .../tables/{t}/rows            read (list): equality filters where.{col}=, order, desc, limit, after
//	GET    .../tables/{t}/rows/{id}       read (one row)
//	POST   .../tables/{t}/rows            add
//	PATCH  .../tables/{t}/rows/{id}       edit
//	DELETE .../tables/{t}/rows/{id}       delete
//
// When an action's value is own, the statement itself carries
// visitor_id = caller, so another person's row answers 404 like a missing
// one. id, visitor_id, created_at and updated_at belong to the server.

func (h *SiteHandler) storageRows(w http.ResponseWriter, r *http.Request) {
	var after func()
	defer func() {
		if after != nil {
			after()
		}
	}()
	c, ok := h.storageResourceFor(w, r, "sqlite")
	if !ok {
		return
	}
	table := r.PathValue("table")
	idRaw := r.PathValue("id")
	action := map[string]string{http.MethodGet: "read", http.MethodHead: "read", http.MethodPost: "add", http.MethodPatch: "edit", http.MethodDelete: "delete"}[r.Method]
	if action == "" || (action == "edit" || action == "delete") && idRaw == "" {
		storageError(w, 405, "method_not_allowed", "method not allowed")
		return
	}
	var rowID int64
	if idRaw != "" {
		n, err := strconv.ParseInt(idRaw, 10, 64)
		if err != nil || n < 1 || action == "add" {
			storageError(w, 400, "invalid_rows", "row id is a positive whole number")
			return
		}
		rowID = n
	}
	if !storageTableNameRE.MatchString(table) {
		storageError(w, 400, "invalid_rows", "invalid row request")
		return
	}
	if action == "read" {
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		var result appstorage.TableResult
		var err error
		if rowID != 0 {
			result, err = h.storageRequestSvc(r, c).TableGet(r.Context(), appstorage.TableInput{Call: call, Table: table, ID: rowID})
		} else {
			result, err = h.storageRequestSvc(r, c).TableList(r.Context(), appstorage.TableInput{Call: call, Table: table, Query: r.URL.Query()})
		}
		if auth := result.Authorization; auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, 403, auth.Code, auth.Message)
				return
			}
			writeStorageRefusals(w, auth.Refusals)
			if !auth.Allowed {
				return
			}
		}
		if err != nil {
			writeTableError(w, err.(*appstorage.Error))
			return
		}
		writeJSON(w, 200, result.Body)
		return
	}
	if action == "add" || action == "edit" || action == "delete" {
		call := h.storageServiceCall(c)
		call.CSRFOK = r.Header.Get("X-SH-CSRF") == "1"
		svc := h.storageRequestSvc(r, c)
		// for (add only): the owner adds this row on behalf of one signed-in
		// visitor instead of themselves. A visitor_id or the email they
		// signed in with; ResolveOwnerFor refuses anyone but the owner, and
		// refuses a target who has never signed in on this site (clear code
		// visitor_unknown, no pre-sign-in email binding).
		if action == "add" {
			if forRef := strings.TrimSpace(r.URL.Query().Get("for")); forRef != "" {
				// Host only, the same policy as the people pick list and
				// storage_visitor_emails's email direction: named viewers
				// and this canonical-email resolution are not a Simple
				// Hack concept.
				if _, event := r.Context().Value(eventStorageContextKey{}).(eventStorageScope); event || h.ed.Storage().EventOwners {
					storageError(w, 404, "for_not_available", "for is not available on Simple Hack")
					return
				}
				resolved, err := svc.ResolveOwnerFor(r.Context(), call, forRef)
				if err != nil {
					writeTableError(w, err.(*appstorage.Error))
					return
				}
				call.OwnerFor = resolved
			}
		}
		in := appstorage.TableInput{Call: call, Table: table, ID: rowID}
		in.Authorized = svc.AuthorizeTableWrite(r.Context(), in, action)
		auth := in.Authorized.Authorization
		if auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, 403, auth.Code, auth.Message)
				return
			}
			writeStorageRefusals(w, auth.Refusals)
		}
		if !in.Authorized.Allowed() {
			return
		}
		account := c.accountID
		if !c.owner {
			account = c.visitorID
		}
		h.createIdempotency.wrapAccount(account, c.siteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if action != "delete" {
				body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
				if err != nil {
					storageError(w, 400, "invalid_rows", "send a JSON object of column values")
					return
				}
				if action == "add" && in.Authorized.CheckedPost() && !storagePostLimits(w, body, h.limits) {
					return
				}
				dec := json.NewDecoder(bytes.NewReader(body))
				if dec.Decode(&in.Values) != nil || in.Values == nil || len(in.Values) > 100 || dec.Decode(new(any)) != io.EOF || action == "edit" && len(in.Values) == 0 {
					storageError(w, 400, "invalid_rows", "send a JSON object of column values")
					return
				}
				for _, raw := range in.Values {
					if !storageScalar(raw) {
						storageError(w, 400, "invalid_rows", "values are strings, numbers, true, false, or null")
						return
					}
				}
			}
			var result appstorage.TableResult
			var err error
			switch action {
			case "add":
				result, err = svc.TableAdd(r.Context(), in)
			case "edit":
				result, err = svc.TableEdit(r.Context(), in)
			default:
				result, err = svc.TableRemove(r.Context(), in)
			}
			if err != nil {
				writeTableError(w, err.(*appstorage.Error))
				return
			}
			after = result.Notify
			writeJSON(w, 200, result.Body)
		})).ServeHTTP(w, r)
		return
	}

}

// writeTableError maps table workflow errors to their existing HTTP responses.
func writeTableError(w http.ResponseWriter, e *appstorage.Error) {
	status := http.StatusBadRequest
	switch e.Code {
	case "storage_busy":
		if e.NoRetryAfter {
			storageError(w, 503, e.Code, e.Message)
			return
		}
		storageBusy(w)
		return
	case "forbidden":
		status = http.StatusForbidden
	case "row_not_found", "invalid_reference", "visitor_unknown":
		status = http.StatusNotFound
	case "row_conflict", "resource_changed", "written_by_blocked", "written_by_tampered":
		status = http.StatusConflict
	case "site_full":
		status = http.StatusInsufficientStorage
	case "query_too_expensive":
		status = http.StatusUnprocessableEntity
	case "internal_error", "storage_failed":
		status = http.StatusInternalServerError
	}
	storageError(w, status, e.Code, e.Message)
}
