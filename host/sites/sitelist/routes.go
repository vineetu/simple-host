package sitelist

import (
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// routes are the site list's handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts GET /v1/sites.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	mux.Handle("GET /v1/sites", noticeMiddleware(authMiddleware(http.HandlerFunc(routes{h}.listSites))))
}

// listSites serves GET /v1/sites. Every caller, the admin included, sees only their own sites by default;
// ?all=1 is an admin-only switch to every account's sites.
func (h routes) listSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	// ?all=1 widens an admin's own list to every account's sites. Silently
	// narrowing a non-admin's all=1 to their own sites would be a lie about
	// what was returned; refuse it instead (same rule as /v1/analytics/sites).
	allAccounts := r.URL.Query().Get("all") == "1"
	if allAccounts && !user.IsAdmin {
		httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "admin only"})
		return
	}
	h.NoteOwnerBrowser(r, user)

	rows, err := Service{Service: h.SitesSvc()}.ListSites(r.Context(), SiteListInput{User: user, All: allAccounts, Params: r.URL.Query(), Views: h.TrafficOn})
	var siteErr *sites.Error
	switch {
	case errors.As(err, &siteErr) && siteErr.Code == "measure_failed":
		httpx.WriteJSON(w, 500, httpx.ErrorResponse{Error: siteErr.Message})
		return
	case errors.As(err, &siteErr):
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	response := make([]sitesroute.SiteResponse, 0, len(rows))
	for _, row := range rows {
		resp := h.SiteResponseFor(row.Site, row.FileBytes)
		resp.Views, resp.Pinned, resp.Order, resp.Keep = row.Views, row.Pinned, row.Order, row.Keep
		resp.Owner, resp.AccessRole = row.Owner, row.Access
		if row.Site.Name == r.URL.Query().Get("files") {
			resp.Files = sitesroute.LiveVersionFiles(r.Context(), h.Disk, row.Site)
		}
		response = append(response, resp)
	}
	httpx.WriteJSON(w, http.StatusOK, response)
}
