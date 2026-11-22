package rows

import (
	"io"
	"net/http"

	"github.com/vsriram/simple-host/host/data"

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

func StorageRows(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	c, ok := h.StorageResourceFor(w, r, c, "sqlite")
	if !ok {
		return
	}
	req := RowsRequest{Call: c, Action: rowActions[r.Method], Table: r.PathValue("table"), ID: r.PathValue("id"), For: r.URL.Query().Get("for"), Query: r.URL.Query()}
	if req.Action == "read" {
		storageRowsRead(h, w, r, req)
		return
	}
	storageRowsWrite(h, w, r, req)
}

func storageRowsRead(h *data.Handler, w http.ResponseWriter, r *http.Request, req RowsRequest) {
	result, err := ReadRows(h.StorageFor(req.Call), r.Context(), req)
	if data.StorageAnswered(w, result.Authorization, err, 400) {
		return
	}
	httpx.WriteJSON(w, 200, result.Body)
}

// storageRowsWrite has an add, edit or delete authorized before the
// idempotency boundary, so a replay never skips the gates, then runs it
// inside with the request's column values.
func storageRowsWrite(h *data.Handler, w http.ResponseWriter, r *http.Request, req RowsRequest) {
	svc := h.StorageFor(req.Call)
	authorized, err := AuthorizeRowWrite(svc, r.Context(), req)
	if data.StorageAnswered(w, authorized.Authorization, err, 400) {
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
		result, err := WriteRow(svc, r.Context(), authorized, body)
		if data.StorageAnswered(w, result.Authorization, err, 400) {
			return
		}
		httpx.WriteJSON(w, 200, result.Body)
	})).ServeHTTP(w, r)
}

// Register maps the table row routes behind caller (Access's StorageCaller).
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("POST /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows", h.StorageRouteFor(caller, StorageRows))
	mux.Handle("GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows", h.StorageRouteFor(caller, StorageRows))
	mux.Handle("GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", h.StorageRouteFor(caller, StorageRows))
	mux.Handle("PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", h.StorageRouteFor(caller, StorageRows))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}", h.StorageRouteFor(caller, StorageRows))
}
