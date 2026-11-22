package notices

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the notices routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/preferences", wrap(h.getHackPreferences))
	mux.Handle("PATCH /v1/hack/preferences", wrap(h.patchHackPreferences))
	mux.Handle("GET /v1/hack/todos", wrap(h.getTodos))
	mux.Handle("POST /v1/hack/events/{slug}/notices/seen", wrap(h.markNoticesSeen))
	mux.Handle("GET /v1/hack/events/{slug}/reminders", wrap(h.getReminderSettings))
	mux.Handle("PUT /v1/hack/events/{slug}/reminders", wrap(h.putReminderSettings))
}

type handler struct{ *core.Hack }
