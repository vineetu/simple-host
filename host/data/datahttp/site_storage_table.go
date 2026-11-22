package datahttp

import (
	"io"
	"net/http"

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
	req := appstorage.RowsRequest{Call: c, Action: rowActions[r.Method], Table: r.PathValue("table"), ID: r.PathValue("id"), For: r.URL.Query().Get("for"), Query: r.URL.Query()}
	if req.Action == "read" {
		h.storageRowsRead(w, r, req)
		return
	}
	h.storageRowsWrite(w, r, req)
}

func (h *DataHandler) storageRowsRead(w http.ResponseWriter, r *http.Request, req appstorage.RowsRequest) {
	result, err := h.StorageFor(req.Call).ReadRows(r.Context(), req)
	if storageAnswered(w, result.Authorization, err, 400) {
		return
	}
	httpx.WriteJSON(w, 200, result.Body)
}

// storageRowsWrite has an add, edit or delete authorized before the
// idempotency boundary, so a replay never skips the gates, then runs it
// inside with the request's column values.
func (h *DataHandler) storageRowsWrite(w http.ResponseWriter, r *http.Request, req appstorage.RowsRequest) {
	svc := h.StorageFor(req.Call)
	authorized, err := svc.AuthorizeRowWrite(r.Context(), req)
	if storageAnswered(w, authorized.Authorization, err, 400) {
		return
	}
	h.CreateIdempotency.WrapAccount(authorized.Account, req.Call.SiteID, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Method != http.MethodDelete {
			var err error
			if body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10)); err != nil {
				httpx.WriteError(w, 400, "invalid_rows", "send a JSON object of column values")
				return
			}
		}
		result, err := svc.WriteRow(r.Context(), authorized, body)
		if storageAnswered(w, result.Authorization, err, 400) {
			return
		}
		httpx.WriteJSON(w, 200, result.Body)
	})).ServeHTTP(w, r)
}
