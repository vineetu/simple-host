package exports

import (
	"crypto/rand"
	"net/http"

	"github.com/vsriram/simple-host/products/hack/core"
)

// Handler serves the exports; its key seals the archive download links.
type Handler struct {
	*core.Hack
	archiveLinkKey []byte
}

// New makes the exports handler with a fresh archive link key.
func New(c *core.Hack) *Handler {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("hack archive links: no randomness: " + err.Error())
	}
	return &Handler{Hack: c, archiveLinkKey: key}
}

// Register adds the exports routes.
func (h *Handler) Register(mux *http.ServeMux, authMW func(http.Handler) http.Handler) {
	wrap := func(fn http.HandlerFunc) http.Handler { return authMW(fn) }
	mux.Handle("GET /v1/hack/events/{slug}/export/participants.csv", wrap(h.exportAdministrationCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/teams.csv", wrap(h.exportAdministrationCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/entries.csv", wrap(h.exportAdministrationCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/projects.tar.gz", wrap(h.exportProjects))
	mux.Handle("GET /v1/hack/events/{slug}/team/export.tar.gz", wrap(h.exportOwnTeam))
	mux.Handle("POST /v1/hack/events/{slug}/export/projects-link", wrap(h.createHackArchiveLink))
	mux.Handle("POST /v1/hack/events/{slug}/export/own-team-link", wrap(h.createHackArchiveLink))
	mux.Handle("GET /v1/hack/archive", http.HandlerFunc(h.downloadHackArchive))
	mux.Handle("GET /v1/hack/events/{slug}/usage", wrap(h.eventUsage))
}
