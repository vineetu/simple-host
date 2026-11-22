package resources

import (
	"errors"
	"io"
	"net/http"

	"github.com/vsriram/simple-host/host/data"

	"github.com/vsriram/simple-host/host/httpx"
)

func ListRoute(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	entries, err := ResourceListJSON(h.StorageSvc(), r.Context(), c)
	if err != nil {
		data.WriteStorageError(w, err, 403)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"resources": entries})
}

func putStorageResource(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	if !data.StorageOwner(w, c) {
		return
	}
	name := r.PathValue("name")
	if !data.ValidResourceName(name) {
		httpx.WriteError(w, 400, "invalid_resource", "invalid resource name")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		diagnostic := httpx.RuleError{Clause: "size", Position: -1, Message: "resource body exceeds 256 KiB"}
		data.WriteStorageError(w, &data.Error{Code: "invalid_rules", Message: diagnostic.Message, RuleError: &diagnostic, RulesErrors: []httpx.RuleError{diagnostic}}, 400)
		return
	}
	if err != nil {
		httpx.WriteError(w, 400, "invalid_request", "invalid JSON body")
		return
	}
	out, created, err := SaveResource(h.StorageFor(c), r.Context(), c, name, body)
	if err != nil {
		data.WriteStorageError(w, err, 400)
		return
	}
	status := 200
	if created {
		status = 201
	}
	httpx.WriteJSON(w, status, out)
}

func deleteStorageResource(h *data.Handler, w http.ResponseWriter, r *http.Request, c data.Call) {
	if !data.StorageOwner(w, c) {
		return
	}
	c, ok := h.StorageResourceFor(w, r, c, "")
	if !ok {
		return
	}
	if err := DeleteStorageResource(h.StorageFor(c), r.Context(), c); err != nil {
		data.WriteStorageError(w, err, 0)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"deleted": true})
}

// Register maps the resource routes behind caller (Access's StorageCaller).
func Register(mux *http.ServeMux, h *data.Handler, caller func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/sites/{sitename}/storage/resources", h.StorageRouteFor(caller, ListRoute))
	mux.Handle("PUT /v1/sites/{sitename}/storage/resources/{name}", h.StorageRouteFor(caller, putStorageResource))
	mux.Handle("DELETE /v1/sites/{sitename}/storage/resources/{name}", h.StorageRouteFor(caller, deleteStorageResource))
}
