package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strings"

	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/model"
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
	mux.Handle("GET /v1/admin/sites/{id}/collections/{coll}", authMiddleware(h.asSiteOwner(h.listCollection)))
	mux.Handle("GET /v1/admin/sites/{id}/collections/{coll}/export.csv", authMiddleware(h.asSiteOwner(h.exportCollectionCSV)))
	mux.Handle("POST /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.bindDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}/domain", authMiddleware(h.asSiteOwner(h.deleteDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}", authMiddleware(h.asSiteOwner(h.deleteSite)))
	// Moderation of a site behind a passcode: remove the passcode, or see a
	// version through a preview link, without knowing the passcode (the
	// admin never reads it). Logged like every call here.
	mux.Handle("DELETE /v1/admin/sites/{id}/lock", authMiddleware(h.asSiteOwner(h.deleteSitePasscode)))
	mux.Handle("POST /v1/admin/sites/{id}/versions/{version}/preview-link", authMiddleware(h.asSiteOwner(h.createPreviewLink)))
	mux.Handle("DELETE /v1/admin/sites/{id}/versions/{version}", authMiddleware(h.asSiteOwner(h.discardVersion)))
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
		site, owner, err := h.sitesSvc().AdminSiteTarget(r.Context(), id, r.Pattern == "DELETE /v1/admin/sites/{id}")
		if e, ok := err.(*appsites.Error); ok && e.Code == "legacy_site_name" {
			writeJSON(w, http.StatusConflict, errorResponse{Error: e.Error(), Code: e.Code})
			return
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
		owner.KeyHash, owner.KeyScope, owner.KeyExpiresAt, owner.IsAdmin = "", "", nil, false
		log.Printf("admin_site_action %s site_id=%s name=%s owner=%s by=%s", r.Pattern, site.ID, site.Name, site.UserID, admin.ID)
		// The handler looks the site up again by owner and name; the id rides
		// along so a rename or a new site under the old name meanwhile is
		// refused (siteForCaller, adminSiteMismatch) instead of acted on.
		ctx := context.WithValue(auth.WithUser(r.Context(), &owner), adminSiteKey{}, site.ID)
		r = r.WithContext(ctx)
		r.SetPathValue("sitename", site.Name)
		if owner.Handle.Valid {
			r.SetPathValue("handle", owner.Handle.String) // listCollection names the site exactly
		}
		next(w, r)
	})
}

type adminSiteKey struct{}

// adminSiteMismatch reports a request from asSiteOwner whose site, found again
// by owner and name, is not the one the admin named by id.
func adminSiteMismatch(r *http.Request, siteID string) bool {
	want, ok := r.Context().Value(adminSiteKey{}).(string)
	return ok && want != siteID
}

// siteForCaller is db.GetSiteByUser for the owner routes asSiteOwner runs:
// sql.ErrNoRows when the site found is not the one the admin named.
func (h *SiteHandler) siteForCaller(r *http.Request, userID, siteName string) (model.Site, error) {
	site, err := h.sitesSvc().GetSiteByUser(r.Context(), userID, siteName)
	if err == nil && adminSiteMismatch(r, site.ID) {
		return model.Site{}, sql.ErrNoRows
	}
	return site, err
}
