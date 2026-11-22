package content

import (
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Register adds the content routes.
func Register(mux *http.ServeMux, c *core.Hack, authMW func(http.Handler) http.Handler) {
	h := &handler{c}
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/icon", http.HandlerFunc(h.getEventIcon))
	mux.Handle("PUT /v1/hack/events/{slug}/icon", wrap(h.putEventIcon))
	mux.Handle("DELETE /v1/hack/events/{slug}/icon", wrap(h.deleteEventIcon))
	mux.Handle("PUT /v1/hack/events/{slug}/announcements/{announcement}", wrap(h.changeAnnouncement))
	mux.Handle("DELETE /v1/hack/events/{slug}/announcements/{announcement}", wrap(h.changeAnnouncement))
	mux.Handle("GET /v1/hack/events/{slug}/content", wrap(h.getContent))
	mux.Handle("PUT /v1/hack/events/{slug}/content", wrap(h.putContent))
	mux.Handle("GET /v1/hack/events/{slug}/announcements", wrap(h.getAnnouncements))
	mux.Handle("POST /v1/hack/events/{slug}/announcements", wrap(h.postAnnouncement))
	mux.Handle("POST /v1/hack/events/{slug}/announcements/preview", wrap(h.previewAnnouncement))
}

type handler struct{ *core.Hack }
