package datahttp

import (
	"io"
	"net/http"
	"strings"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
)

// The SQLite table routes. Visitors send an action and values, never SQL;
// the server builds every statement from the table's rules:
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

var rowActions = map[string]string{http.MethodGet: "read", http.MethodHead: "read", http.MethodPost: "add", http.MethodPatch: "edit", http.MethodDelete: "delete"}

func (h *DataHandler) StorageRows(w http.ResponseWriter, r *http.Request, c StorageCall) {
	c, ok := h.storageResourceFor(w, r, c, "sqlite")
	if !ok {
		return
	}
	action := rowActions[r.Method]
	in := appstorage.TableInput{Call: c, Table: r.PathValue("table")}
	var err error
	if in.ID, err = appstorage.RowRequest(action, in.Table, r.PathValue("id")); err != nil {
		WriteStorageError(w, err, http.StatusMethodNotAllowed)
		return
	}
	if action == "read" {
		h.storageRowsRead(w, r, in)
		return
	}
	h.storageRowsWrite(w, r, in, action)
}

func (h *DataHandler) storageRowsRead(w http.ResponseWriter, r *http.Request, in appstorage.TableInput) {
	svc := h.StorageFor(in.Call)
	var result appstorage.TableResult
	var err error
	if in.ID != 0 {
		result, err = svc.TableGet(r.Context(), in)
	} else {
		in.Query = r.URL.Query()
		result, err = svc.TableList(r.Context(), in)
	}
	if storageAnswered(w, result.Authorization, err, 400) {
		return
	}
	httpx.WriteJSON(w, 200, result.Body)
}

// storageRowsWrite authorizes an add, edit or delete before the idempotency
// boundary, so a replay never skips the gates, then runs it inside.
func (h *DataHandler) storageRowsWrite(w http.ResponseWriter, r *http.Request, in appstorage.TableInput, action string) {
	svc := h.StorageFor(in.Call)
	if action == "add" && !h.storageRowsFor(w, r, svc, &in) {
		return
	}
	in.Authorized = svc.AuthorizeTableWrite(r.Context(), in, action)
	if storageRefused(w, in.Authorized.Authorization) || !in.Authorized.Allowed() {
		return
	}
	account := in.Call.AccountID
	if !in.Call.Owner {
		account = in.Call.VisitorID
	}
	var result appstorage.TableResult
	h.CreateIdempotency.WrapAccount(account, in.Call.SiteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result = h.storageRowsApply(w, r, svc, in, action)
	})).ServeHTTP(w, r)
	if result.Event != nil {
		svc.Emit(*result.Event)
	}
}

// storageRowsFor resolves ?for= on an add: the owner adds the row on behalf
// of one signed-in visitor (a visitor_id or the email they signed in with).
// ResolveOwnerFor refuses anyone but the owner, and a target who never
// signed in on this site. Named viewers and this email resolution are not a
// Simple Hack concept, so the product refuses for= altogether.
func (h *DataHandler) storageRowsFor(w http.ResponseWriter, r *http.Request, svc *appstorage.Service, in *appstorage.TableInput) bool {
	ref := strings.TrimSpace(r.URL.Query().Get("for"))
	if ref == "" {
		return true
	}
	if h.storageBindings != nil {
		httpx.WriteError(w, 404, "for_not_available", "for is not available on Simple Hack")
		return false
	}
	resolved, err := svc.ResolveOwnerFor(r.Context(), in.Call, ref)
	if err != nil {
		WriteStorageError(w, err, 400)
		return false
	}
	in.Call.OwnerFor = resolved
	return true
}

// storageRowsApply reads the values and runs the write; it answers and
// returns the result whose Event is sent once the response is written.
func (h *DataHandler) storageRowsApply(w http.ResponseWriter, r *http.Request, svc *appstorage.Service, in appstorage.TableInput, action string) appstorage.TableResult {
	if action != "delete" {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			httpx.WriteError(w, 400, "invalid_rows", "send a JSON object of column values")
			return appstorage.TableResult{}
		}
		if action == "add" && in.Authorized.CheckedPost() {
			if err := appstorage.CheckPostLimits(body, h.Limits.SpamCheckedMaxBytes, h.Limits.SpamCheckedMaxLinks); err != nil {
				WriteStorageError(w, err, 400)
				return appstorage.TableResult{}
			}
		}
		if in.Values, err = appstorage.DecodeRowValues(body, action); err != nil {
			WriteStorageError(w, err, 400)
			return appstorage.TableResult{}
		}
	}
	result, err := applyRowWrite(r, svc, in, action)
	if err != nil {
		WriteStorageError(w, err, 400)
		return appstorage.TableResult{}
	}
	httpx.WriteJSON(w, 200, result.Body)
	return result
}

func applyRowWrite(r *http.Request, svc *appstorage.Service, in appstorage.TableInput, action string) (appstorage.TableResult, error) {
	switch action {
	case "add":
		return svc.TableAdd(r.Context(), in)
	case "edit":
		return svc.TableEdit(r.Context(), in)
	}
	return svc.TableRemove(r.Context(), in)
}
