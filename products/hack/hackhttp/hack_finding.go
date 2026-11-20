package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

func (h *participationHTTP) findingAccess(w http.ResponseWriter, r *http.Request, write bool) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), write, "participant", "organiser")
	if !ok {
		return a, false
	}
	if write && !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
		return a, false
	}
	if !a.event.TeamFinding {
		writeHackError(w, teams.ErrFindingOff)
		return a, false
	}
	return a, true
}

func (h *participationHTTP) listOpenTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant", "organiser")
	if !ok {
		return
	}
	out := map[string]any{"enabled": a.event.TeamFinding, "teams": []any{}, "solo": []any{}, "pending": []any{}, "looking": false, "intro": ""}
	if !a.event.TeamFinding {
		httpx.WriteJSON(w, 200, out)
		return
	}
	directory, err := h.teams.ListOpenTeams(r.Context(), a.event.ID, a.user.ID, a.member)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	teams := []any{}
	for _, t := range directory.Teams {
		teams = append(teams, map[string]any{"slug": t.Slug, "name": t.Name, "members": t.Members, "member_count": len(t.Members), "team_size_max": a.event.TeamSizeMax, "full": len(t.Members) >= a.event.TeamSizeMax, "track": t.Track, "looking_for": t.LookingFor})
	}
	solo := []any{}
	for _, p := range directory.Solo {
		solo = append(solo, map[string]any{"user_id": p.UserID, "display_name": p.DisplayName, "intro": p.Intro})
	}
	pending := []any{}
	for _, p := range directory.Pending {
		pending = append(pending, p)
	}
	out["teams"], out["solo"], out["pending"] = teams, solo, pending
	out["looking"], out["intro"] = directory.Looking, directory.Intro
	if t := directory.Team; t != nil {
		out["team"] = map[string]any{"looking_for": t.LookingFor, "locked": t.Locked, "admin": t.Admin}
	}
	httpx.WriteJSON(w, 200, out)
}

// setTeamFinding is the deprecated PUT .../team/finding (replaced by PATCH
// .../team): admin only now, writes only looking_for, ignores open. Kept
// for one release so an older cached tool call still works.
func (h *participationHTTP) setTeamFinding(w http.ResponseWriter, r *http.Request) {
	a, ok := h.findingAccess(w, r, true)
	if !ok {
		return
	}
	if !a.member.TeamID.Valid {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Open       bool   `json:"open"`
		LookingFor string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	note, ok := checkHackLine(w, events.StripInvisible(req.LookingFor), "looking_for", 0, 120)
	if !ok {
		return
	}
	err := h.teams.SetTeamFinding(r.Context(), a.event.ID, a.user.ID, note)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true, "deprecated": "use PATCH /v1/hack/events/{slug}/team"})
}
func (h *participationHTTP) setLooking(w http.ResponseWriter, r *http.Request) {
	a, ok := h.findingAccess(w, r, true)
	if !ok {
		return
	}
	if a.member.Role != "participant" {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Looking bool   `json:"looking"`
		Intro   string `json:"intro"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	intro, ok := checkHackLine(w, req.Intro, "intro", 0, 120)
	if !ok {
		return
	}
	err := h.teams.SetLooking(r.Context(), a.event.ID, a.user.ID, req.Looking, intro)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}
func (h *participationHTTP) createTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := h.findingAccess(w, r, true)
	if !ok {
		return
	}
	userID := a.user.ID
	team := r.PathValue("team")
	kind := "request"
	if team == "" {
		kind = "invite"
		var req struct {
			UserID string `json:"user_id"`
		}
		if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
			return
		}
		if !sites.UUIDShape.MatchString(req.UserID) || !a.member.TeamID.Valid {
			writeEventNotFound(w)
			return
		}
		userID = req.UserID
		t, err := h.teams.GetTeam(r.Context(), a.member.TeamID.String)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		team = t.Slug
	}
	state, err := h.teams.CreateTeamRequest(r.Context(), a.event, a.user.ID, team, userID, kind)
	if err != nil {
		writeHackError(w, err)
		return
	}
	status := 200
	if state == "created" {
		status = 201
	}
	httpx.WriteJSON(w, status, map[string]any{"ok": true, "state": state})
}
func (h *participationHTTP) resolveTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := h.findingAccess(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !sites.UUIDShape.MatchString(id) {
		writeEventNotFound(w)
		return
	}
	action := r.PathValue("action")
	if r.Method == http.MethodDelete {
		action = "withdraw"
	}
	if action != "accept" && action != "decline" && action != "withdraw" {
		writeEventNotFound(w)
		return
	}
	err := h.teams.ResolveTeamRequest(r.Context(), a.event, a.user.ID, id, action)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (h *participationHTTP) fillTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
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
	httpx.WriteJSON(w, 200, map[string]any{"plan": plan, "applied": req.Apply, "skipped": skipped, "size": size})
}
