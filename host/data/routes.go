package data

import (
	"net/http"
)

// RegisterStorageRoutes maps the core's one route, the spam check's frame.
// Each feature maps its own (kv.Register, rows.Register, ...).
func (d *Handler) RegisterStorageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/turnstile/frame", d.turnstileFrame)
}
