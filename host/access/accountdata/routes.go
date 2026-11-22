package accountdata

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/edition"
	"github.com/vsriram/simple-host/host/email"
	"github.com/vsriram/simple-host/host/ops"
	"github.com/vsriram/simple-host/host/sites"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
)

// deleteAccount handles DELETE /v1/admin/users/{id}: the same immediate,
// final erasure as DELETE /v1/me (account_data.go), started by the operator.
// A suspended account can be deleted here; an admin account cannot.
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	if !auth.Admin(w, r) {
		return
	}
	// Account ids are lowercase uuids (users.id::text); the site locks are
	// keyed by that exact string.
	id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
	_, err := h.Cutoff.ErasePerson(r.Context(), accounts.DeleteAccountInput{ID: id, Operator: true})
	if errors.Is(err, ops.ErrFilesLeft) {
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: err.Error()})
		return
	}
	if auth.WriteLifecycleFailure(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Handler serves a person's download of all their data and the erasure of
// an account, by the person or the operator. Cutoff is the workflow that
// erases (host/ops); Sites, SiteFiles and SiteArchive read the account's
// sites for the export.
type Handler struct {
	Database    *sql.DB
	Hosts       *sites.Hosts
	Limits      edition.Limits
	Mailer      email.Sender
	Sites       *sites.Service
	SiteFiles   *sites.Service
	SiteArchive *ops.SiteArchive
	Cutoff      ops.Cutoff
}

// Register mounts the routes; siteOpLimiter and exportLimiter are the site
// routes' own, shared with them.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler, siteOpLimiter, exportLimiter *httpx.RateLimiter) {
	mux.Handle("DELETE /v1/admin/users/{id}", authMiddleware(http.HandlerFunc(h.deleteAccount)))
	mux.Handle("GET /v1/me/export.zip", authMiddleware(httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(h.exportMe))))
	// The address before the download became a .zip; serves the same zip.
	mux.Handle("GET /v1/me/export.tar.gz", authMiddleware(httpx.RateLimitByIP(exportLimiter, http.HandlerFunc(h.exportMe))))
	mux.Handle("DELETE /v1/me", authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(h.deleteMe))))
}
