package handler

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/internal/auth"
	db "github.com/vsriram/simple-host/internal/db"
)

// The admin page's per-site actions (Versions, Make active, Data, Connect or
// disconnect a domain, Delete) on anyone's site. The owner routes behind them
// look a site up by the caller's own account and a bare name, so the admin's
// key only ever reached the admin's own sites that way. These routes name the
// site by id and run the very same owner handler as that site's owner: the
// same checks, refusals (a taken-down site stays as it is) and effects (a
// deleted site goes to the owner's Recently deleted). Admin only; anyone
// else gets 404. Each call is logged with the admin who made it.

var siteIDShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (h *SiteHandler) registerAdminSiteActions(mux *http.ServeMux, authMiddleware func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/admin/sites/{id}/versions", authMiddleware(h.asSiteOwner(h.listVersions)))
	mux.Handle("PUT /v1/admin/sites/{id}/active-version", authMiddleware(h.asSiteOwner(h.setActiveVersion)))
	mux.Handle("GET /v1/admin/sites/{id}/collections", authMiddleware(h.asSiteOwner(h.listSiteCollections)))
	mux.Handle("GET /v1/admin/sites/{id}/collections/{coll}/export.csv", authMiddleware(h.asSiteOwner(h.exportCollectionCSV)))
	mux.Handle("POST /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.bindDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.deleteDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}", authMiddleware(h.asSiteOwner(h.deleteSite)))
}

// asSiteOwner runs next for the admin as the owner of the site {id}, with
// {sitename} set to that site's name.
func (h *SiteHandler) asSiteOwner(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !accountAdmin(w, r) {
			return
		}
		admin := auth.GetUser(r.Context())
		id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
		if !siteIDShape.MatchString(id) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		site, err := db.GetSiteByID(r.Context(), h.database, id)
		if err == nil && site.Deleted {
			err = sql.ErrNoRows
		}
		var owner db.User
		if err == nil {
			owner, err = db.GetUserByID(r.Context(), h.database, site.UserID)
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, errorResponse{Error: "site not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		// The owner as the owner routes see them: no key of theirs, and never
		// the admin's powers.
		owner.KeyHash, owner.KeyScope, owner.KeyExpiresAt = "", "", nil
		log.Printf("admin_site_action %s site_id=%s name=%s owner=%s by=%s", r.Pattern, site.ID, site.Name, site.UserID, admin.ID)
		r = r.WithContext(auth.WithUser(r.Context(), &owner))
		r.SetPathValue("sitename", site.Name)
		next(w, r)
	})
}
