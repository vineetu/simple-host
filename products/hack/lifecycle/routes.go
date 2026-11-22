package lifecycle

import (
	"context"
	"net/http"
	"sync"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Handler serves an event's life: create, settings, stages, delete, the
// platform's moderation and the archive cleanup.
type Handler struct {
	*core.Hack
	// NamePeer reports whether the peer instance holds a name; nil = no peer.
	NamePeer func(ctx context.Context, name string) (bool, error)
	// InstanceUsage returns the bytes team sites use on this instance; nil = 0.
	InstanceUsage func(ctx context.Context) (int64, error)

	eventCleanupOnce   sync.Once
	eventCleanupWorker *eventCleanupOwner
}

// Register adds the lifecycle routes.
func (h *Handler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/navigation", wrap(func(w http.ResponseWriter, r *http.Request) { httpx.WriteJSON(w, http.StatusOK, events.Navigation()) }))
	mux.Handle("GET /v1/hack/events", wrap(h.listEvents))
	mux.Handle("POST /v1/hack/events", wrap(h.createEvent))
	mux.Handle("GET /v1/hack/names/{slug}", wrap(h.checkName))
	mux.Handle("GET /v1/hack/events/{slug}", wrap(h.getEvent))
	mux.Handle("PATCH /v1/hack/events/{slug}", wrap(h.patchEvent))
	mux.Handle("POST /v1/hack/events/{slug}/stage", wrap(h.setStage))
	mux.Handle("POST /v1/hack/events/{slug}/codes/{kind}", wrap(h.regenCode))
	mux.Handle("DELETE /v1/hack/events/{slug}", wrap(h.deleteEvent))
	mux.Handle("GET /v1/admin/hack/events", wrap(h.adminListEvents))
	mux.Handle("POST /v1/admin/hack/events/{slug}/takedown", wrap(h.adminTakeDown))
	mux.Handle("POST /v1/admin/hack/events/{slug}/keep-sites", wrap(h.adminKeepSites))
	mux.Handle("POST /v1/admin/hack/events/{slug}/restore", wrap(h.adminRestore))
	mux.Handle("DELETE /v1/admin/hack/events/{slug}", wrap(h.adminDelete))
}
