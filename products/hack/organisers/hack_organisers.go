package organisers

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	core "github.com/vsriram/simple-host/products/hack/core"
)

func (h *handler) createOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	code, expires, err := h.Events.CreateOrganiserInvite(r.Context(), a.Event.ID, a.User.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 201, map[string]any{"url": h.Links.PublicBaseURL + "/organiser/" + code, "expires_at": core.RFC3339Time(expires)})
}

func (h *handler) revokeOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	if err := h.Events.RevokeOrganiserInvite(r.Context(), a.Event.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(204)
}

func (h *handler) getOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	caller, accountName := "", ""
	if u := auth.GetUser(r.Context()); u != nil {
		caller, accountName = u.ID, strings.TrimSpace(u.DisplayName.String)
	}
	if h.Codes.Limited(w, r, caller) {
		return
	}
	ev, err := h.Events.OrganiserInvite(r.Context(), r.PathValue("code"))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 200, map[string]any{"slug": ev.Slug, "title": ev.Title, "icon_url": h.Links.IconURL(ev), "stage": ev.Stage, "organiser_name": ev.OrganiserName, "organisation": ev.Organisation, "role": "organiser", "account_name": accountName, "joinable": ev.Stage != "archived", "coc_default": core.DefaultCoC, "coc_text": ev.CocText})
}

func (h *handler) acceptOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	if h.Codes.Limited(w, r, user.ID) {
		return
	}
	var req struct {
		AcceptCoC   bool   `json:"accept_coc"`
		DisplayName string `json:"display_name"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if !req.AcceptCoC {
		httpx.WriteError(w, 400, "coc_required", "you must accept the code of conduct")
		return
	}
	name := req.DisplayName
	ev, member, err := h.Events.AcceptOrganiserInvite(r.Context(), r.PathValue("code"), user.ID, name)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, h.EventView(r.Context(), ev, member, true))
}

func (h *handler) removeOrganiser(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(id) {
		core.WriteEventNotFound(w)
		return
	}
	if err := h.Events.RemoveOrganiser(r.Context(), a.Event, a.User.ID, id); err != nil {
		core.WriteError(w, err)
		return
	}
	w.WriteHeader(204)
}
