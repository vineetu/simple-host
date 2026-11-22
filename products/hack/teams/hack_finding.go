package teams

import (
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
)

func (h *handler) listOpenTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant", "organiser")
	if !ok {
		return
	}
	directory, err := h.Teams.ListOpenTeams(r.Context(), a.Event, a.User.ID, a.Member)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	max := a.Event.TeamSizeMax
	teams := []any{}
	for _, t := range directory.Teams {
		teams = append(teams, map[string]any{"slug": t.Slug, "name": t.Name, "members": t.Members, "member_count": len(t.Members), "team_size_max": max, "full": len(t.Members) >= max, "track": t.Track, "looking_for": t.LookingFor})
	}
	solo := []any{}
	for _, p := range directory.Solo {
		solo = append(solo, map[string]any{"user_id": p.UserID, "display_name": p.DisplayName, "intro": p.Intro})
	}
	pending := []any{}
	for _, p := range directory.Pending {
		pending = append(pending, p)
	}
	out := map[string]any{"enabled": a.Event.TeamFinding, "teams": teams, "solo": solo, "pending": pending, "looking": directory.Looking, "intro": directory.Intro}
	if t := directory.Team; t != nil {
		out["team"] = map[string]any{"looking_for": t.LookingFor, "locked": t.Locked, "admin": t.Admin}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// setTeamFinding is the deprecated PUT .../team/finding (replaced by PATCH .../team): admin only, writes only looking_for, ignores open.
func (h *handler) setTeamFinding(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		LookingFor string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if err := h.SetTeamFinding(r.Context(), a.Event, a.Member, a.User.ID, req.LookingFor); err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "deprecated": "use PATCH /v1/hack/events/{slug}/team"})
}

func (h *handler) setLooking(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		Looking bool   `json:"looking"`
		Intro   string `json:"intro"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if err := h.SetLooking(r.Context(), a.Event, a.Member, a.User.ID, req.Looking, req.Intro); err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// createTeamRequest asks to join {team}, or, on the route without one,
// invites body.user_id to the caller's team.
func (h *handler) createTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	team := r.PathValue("team")
	if team == "" && !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	state, err := h.CreateTeamRequest(r.Context(), a.Event, a.Member, a.User.ID, team, req.UserID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	status := http.StatusOK
	if state == "created" {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"ok": true, "state": state})
}

func (h *handler) resolveTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	action := r.PathValue("action")
	if r.Method == http.MethodDelete {
		action = "withdraw"
	}
	if err := h.ResolveTeamRequest(r.Context(), a.Event, a.User.ID, r.PathValue("id"), action); err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) fillTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Apply bool `json:"apply"`
		Size  *int `json:"size"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	plan, skipped, size, err := h.FillTeams(r.Context(), a.Event.ID, a.User.ID, req.Size, req.Apply)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plan": plan, "applied": req.Apply, "skipped": skipped, "size": size})
}
