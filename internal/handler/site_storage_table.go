package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

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
			result, err = h.storageRequestSvc(r, c).TableGet(r.Context(), &call, appstorage.TableInput{Table: table, ID: rowID})
		} else {
			result, err = h.storageRequestSvc(r, c).TableList(r.Context(), &call, appstorage.TableInput{Table: table, Query: r.URL.Query()})
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
		deps := h.storageRequestDeps(r, c)
		deps.TableWriteRequest = func(ac appstorage.Call, auth *appstorage.Error, run func(map[string]json.RawMessage) (appstorage.TableResult, error)) {
			if auth != nil {
				writeStorageRefusals(w, auth.Refusals)
			}
			account := c.accountID
			if !c.owner {
				account = c.visitorID
			}
			h.createIdempotency.wrapAccount(account, c.siteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if action == "delete" {
					result, err := run(nil)
					if err != nil {
						writeTableError(w, err.(*appstorage.Error))
						return
					}
					after = result.Notify
					writeJSON(w, 200, result.Body)
					return
				}
				body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
				if err != nil {
					storageError(w, 400, "invalid_rows", "send a JSON object of column values")
					return
				}
				if action == "add" && ac.CheckedPost && !storagePostLimits(w, body, h.limits) {
					return
				}
				var values map[string]json.RawMessage
				dec := json.NewDecoder(bytes.NewReader(body))
				if dec.Decode(&values) != nil || values == nil || len(values) > 100 || dec.Decode(new(any)) != io.EOF || action == "edit" && len(values) == 0 {
					storageError(w, 400, "invalid_rows", "send a JSON object of column values")
					return
				}
				for _, raw := range values {
					if !storageScalar(raw) {
						storageError(w, 400, "invalid_rows", "values are strings, numbers, true, false, or null")
						return
					}
				}
				result, err := run(values)
				if err != nil {
					writeTableError(w, err.(*appstorage.Error))
					return
				}
				after = result.Notify
				writeJSON(w, 200, result.Body)
			})).ServeHTTP(w, r)
		}
		svc := appstorage.New(h.database, deps)
		in := appstorage.TableInput{Table: table, ID: rowID}
		var result appstorage.TableResult
		var err error
		if action == "add" {
			result, err = svc.TableAdd(r.Context(), &call, in)
		} else if action == "edit" {
			result, err = svc.TableEdit(r.Context(), &call, in)
		} else {
			result, err = svc.TableRemove(r.Context(), &call, in)
		}
		if auth := result.Authorization; auth != nil {
			if len(auth.Refusals) == 0 {
				storageError(w, 403, auth.Code, auth.Message)
			} else {
				writeStorageRefusals(w, auth.Refusals)
			}
			return
		}
		if err != nil {
			writeTableError(w, err.(*appstorage.Error))
		}
		return
	}

}

// writeTableError maps table workflow errors to their existing HTTP responses.
func writeTableError(w http.ResponseWriter, e *appstorage.Error) {
	status := http.StatusBadRequest
	switch e.Code {
	case "storage_busy":
		storageBusy(w)
		return
	case "forbidden":
		status = http.StatusForbidden
	case "row_not_found", "invalid_reference":
		status = http.StatusNotFound
	case "row_conflict", "resource_changed":
		status = http.StatusConflict
	case "site_full":
		status = http.StatusInsufficientStorage
	case "internal_error":
		status = http.StatusInternalServerError
	}
	storageError(w, status, e.Code, e.Message)
}
