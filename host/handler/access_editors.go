package handler

import (
	accounts "github.com/vsriram/simple-host/host/access"
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
)

func (h *AccessHandler) listSiteEditors(w http.ResponseWriter, r *http.Request) {
	editors, err := accounts.ListSiteEditors(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("sitename"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"editors": editors})
}

func (h *AccessHandler) addSiteEditor(w http.ResponseWriter, r *http.Request) {
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
	if err := accounts.AddSiteEditor(r.Context(), h.database, user.ID, actor, r.PathValue("sitename"), body.Person); err != nil {
		writeTeamError(w, err)
		return
	}
	h.listSiteEditors(w, r)
}

func (h *AccessHandler) removeSiteEditor(w http.ResponseWriter, r *http.Request) {
	if err := accounts.RemoveSiteEditor(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("sitename"), r.PathValue("person")); err != nil {
		writeTeamError(w, err)
		return
	}
	h.listSiteEditors(w, r)
}
