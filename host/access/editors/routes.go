package editors

import (
	"database/sql"
	"net/http"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
)

// Register mounts the site-editor routes. An editor never reaches them
// (auth.ScopeGate holds editors to EditorAllows).
func Register(mux *http.ServeMux, db *sql.DB, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h := handler{db}
	mux.Handle("GET /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.list))))
	mux.Handle("POST /v1/sites/{sitename}/editors", noticeMiddleware(authMiddleware(http.HandlerFunc(h.add))))
	mux.Handle("DELETE /v1/sites/{sitename}/editors/{person}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.remove))))
}

type handler struct{ db *sql.DB }

func (h handler) list(w http.ResponseWriter, r *http.Request) {
	editors, err := ListSiteEditors(r.Context(), h.db, auth.GetUser(r.Context()).ID, r.PathValue("sitename"))
	if err != nil {
		access.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"editors": editors})
}

func (h handler) add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Person string `json:"person"`
	}
	if !access.DecodeTeamBody(w, r, &body) {
		return
	}
	user := auth.GetUser(r.Context())
	actor := user.ID
	if user.Member != nil {
		actor = user.Member.PersonID
	}
	if err := AddSiteEditor(r.Context(), h.db, user.ID, actor, r.PathValue("sitename"), body.Person); err != nil {
		access.WriteTeamError(w, err)
		return
	}
	h.list(w, r)
}

func (h handler) remove(w http.ResponseWriter, r *http.Request) {
	if err := RemoveSiteEditor(r.Context(), h.db, auth.GetUser(r.Context()).ID, r.PathValue("sitename"), r.PathValue("person")); err != nil {
		access.WriteTeamError(w, err)
		return
	}
	h.list(w, r)
}
