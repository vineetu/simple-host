package teams

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	core "github.com/vsriram/simple-host/products/hack/core"
)

func (h *handler) renameTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.OrganiserWrite(w, r, h.Members)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	name, ok := checkLine(w, events.StripInvisible(req.Name), "team_name", 1, 80)
	if !ok {
		return
	}
	slug := r.PathValue("team")
	err := h.Teams.RenameEventTeamLookup(r.Context(), a.Event.ID, slug, name)
	if errors.Is(err, sql.ErrNoRows) {
		core.WriteEventNotFound(w)
		return
	}
	if events.UniqueError(err) {
		httpx.WriteError(w, 409, "team_name_taken", "another team has that name")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"name": name, "slug": slug})
}
