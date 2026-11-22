package sitesroute

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
)

// The admin page's per-site actions (Versions, Make active, Data, Connect or
// disconnect a domain, Delete) on anyone's site. The owner routes behind them
// look a site up by the caller's own account and a bare name, so the admin's
// key only ever reached the admin's own sites that way. These routes name the
// site by id and run the very same owner handler as that site's owner: the
// same checks, refusals (a taken-down site stays as it is) and effects (a
// deleted site goes to the owner's Recently deleted). Admin only; anyone
// else gets 404. Each call is logged with the admin who made it.

var SiteIDShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (h *Handler) AsSiteOwner(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.Admin(w, r) {
			return
		}
		admin := auth.GetUser(r.Context())
		id := strings.ToLower(strings.TrimSpace(r.PathValue("id")))
		if !SiteIDShape.MatchString(id) {
			httpx.WriteError(w, http.StatusNotFound, "site_not_found", "site not found")
			return
		}
		site, owner, err := h.SitesSvc().AdminSiteTarget(r.Context(), id, r.Pattern == "DELETE /v1/admin/sites/{id}")
		if e, ok := err.(*appsites.Error); ok {
			httpx.WriteError(w, httpx.RefusalStatus(e.Code, http.StatusConflict), e.Code, e.Message)
			return
		}
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		// The owner as the owner routes see them: no key of theirs, and never
		// the admin's powers.
		owner.KeyHash, owner.KeyScope, owner.KeyExpiresAt, owner.IsAdmin = "", "", nil, false
		log.Printf("admin_site_action %s site_id=%s name=%s owner=%s by=%s", r.Pattern, site.ID, site.Name, site.UserID, admin.ID)
		// The handler looks the site up again by owner and name; the id rides
		// along so a rename or a new site under the old name meanwhile is
		// refused (SiteForCaller, AdminSiteMismatch) instead of acted on.
		ctx := context.WithValue(auth.WithUser(r.Context(), &owner), web.AdminSiteKey{}, site.ID)
		r = r.WithContext(ctx)
		r.SetPathValue("sitename", site.Name)
		if owner.Handle.Valid {
			r.SetPathValue("handle", owner.Handle.String) // listCollection names the site exactly
		}
		next(w, r)
	})
}

// SiteForCaller is db.GetSiteByUser for the owner routes asSiteOwner runs:
// sql.ErrNoRows when the site found is not the one the admin named.
func (h *Handler) SiteForCaller(r *http.Request, userID, siteName string) (model.Site, error) {
	site, err := h.SitesSvc().GetSiteByUser(r.Context(), userID, siteName)
	if err == nil && web.AdminSiteMismatch(r, site.ID) {
		return model.Site{}, sql.ErrNoRows
	}
	return site, err
}
