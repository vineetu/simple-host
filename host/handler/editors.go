package handler

import (
	"net/http"

	"github.com/vsriram/simple-host/host/app/sites"
	"github.com/vsriram/simple-host/host/auth"
)

// Site editors: a person who may publish, update and roll back one site of
// an account without belonging to it (accounts.ActsAsOwner). The account
// itself, or a member of the team with owner=<team>, manages them; an editor
// never reaches these routes (auth.ScopeGate holds editors to EditorAllows).
// Offered where accounts own their own sites, like teams.
func (h *SiteHandler) registerEditors(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	if !h.ed.Identity().PersonalSites {
		return
	}
	mux.Handle("GET /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listSiteEditors))))
	mux.Handle("POST /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.addSiteEditor))))
	mux.Handle("DELETE /v1/sites/{sitename}/editors/{person}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.removeSiteEditor))))
}

func (h *SiteHandler) listSiteEditors(w http.ResponseWriter, r *http.Request) {
	editors, err := sites.ListSiteEditors(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("sitename"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"editors": editors})
}

func (h *SiteHandler) addSiteEditor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Person string `json:"person"`
	}
	if !decodeTeamBody(w, r, &body) {
		return
	}
	user := auth.GetUser(r.Context())
	actor := user.ID
	if user.Member != nil {
		actor = user.Member.PersonID
	}
	if err := sites.AddSiteEditor(r.Context(), h.database, user.ID, actor, r.PathValue("sitename"), body.Person); err != nil {
		writeTeamError(w, err)
		return
	}
	h.listSiteEditors(w, r)
}

func (h *SiteHandler) removeSiteEditor(w http.ResponseWriter, r *http.Request) {
	if err := sites.RemoveSiteEditor(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("sitename"), r.PathValue("person")); err != nil {
		writeTeamError(w, err)
		return
	}
	h.listSiteEditors(w, r)
}
