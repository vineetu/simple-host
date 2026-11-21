package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
)

func (h *participationHTTP) listOpenTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant", "organiser")
	if !ok {
		return
	}
	directory, err := h.teams.ListOpenTeams(r.Context(), a.event, a.user.ID, a.member)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	max := a.event.TeamSizeMax
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
	out := map[string]any{"enabled": a.event.TeamFinding, "teams": teams, "solo": solo, "pending": pending, "looking": directory.Looking, "intro": directory.Intro}
	if t := directory.Team; t != nil {
		out["team"] = map[string]any{"looking_for": t.LookingFor, "locked": t.Locked, "admin": t.Admin}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// setTeamFinding is the deprecated PUT .../team/finding (replaced by PATCH
// .../team): admin only now, writes only looking_for, ignores open. Kept
// for one release so an older cached tool call still works.
func (h *participationHTTP) setTeamFinding(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		LookingFor string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if err := h.teams.SetTeamFinding(r.Context(), a.event, a.member, a.user.ID, req.LookingFor); err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "deprecated": "use PATCH /v1/hack/events/{slug}/team"})
}

func (h *participationHTTP) setLooking(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		Looking bool   `json:"looking"`
		Intro   string `json:"intro"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if err := h.teams.SetLooking(r.Context(), a.event, a.member, a.user.ID, req.Looking, req.Intro); err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// createTeamRequest asks to join {team}, or, on the route without one,
// invites body.user_id to the caller's team.
func (h *participationHTTP) createTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	team := r.PathValue("team")
	if team == "" && !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	state, err := h.teams.CreateTeamRequest(r.Context(), a.event, a.member, a.user.ID, team, req.UserID)
	if err != nil {
		writeHackError(w, err)
		return
	}
	status := http.StatusOK
	if state == "created" {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"ok": true, "state": state})
}

func (h *participationHTTP) resolveTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant", "organiser")
	if !ok {
		return
	}
	action := r.PathValue("action")
	if r.Method == http.MethodDelete {
		action = "withdraw"
	}
	if err := h.teams.ResolveTeamRequest(r.Context(), a.event, a.user.ID, r.PathValue("id"), action); err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *participationHTTP) fillTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Apply bool `json:"apply"`
		Size  *int `json:"size"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	plan, skipped, size, err := h.teams.FillTeams(r.Context(), a.event.ID, a.user.ID, req.Size, req.Apply)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"plan": plan, "applied": req.Apply, "skipped": skipped, "size": size})
}
