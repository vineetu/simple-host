package hackhttp

import (
	"context"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *participationHTTP) listPeople(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	people, err := h.teams.ListPeople(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(people))
	for _, p := range people {
		item := map[string]any{
			"user_id":           p.UserID,
			"email":             p.Email,
			"display_name":      p.DisplayName,
			"role":              p.Role,
			"approval_status":   p.ApprovalStatus,
			"primary_organiser": a.event.CreatedBy.Valid && p.UserID == a.event.CreatedBy.String,
			"team":              nil,
			"joined_at":         rfc3339Time(p.JoinedAt),
			"coc_accepted_at":   rfc3339UTC(p.CocAcceptedAt),
			// The profile fields (city, country, age_range, gender) stay off this list by design: they are only in the Applications list and the participants CSV.
			"joined_via": p.JoinedVia,
		}
		if p.TeamSlug.Valid {
			item["team"] = map[string]any{"slug": p.TeamSlug.String, "name": p.TeamName.String}
		}
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *participationHTTP) removePerson(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if err := h.teams.RemovePerson(r.Context(), a.event, r.PathValue("user_id")); err != nil {
		writeHackError(w, err)
		return
	}
	h.links.removeOrphanTeamSites(r.Context(), a.event)
	w.WriteHeader(http.StatusNoContent)
}

func (h *participationHTTP) listTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	teams, err := h.teams.ListTeams(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	teamObjs := make([]map[string]any, 0, len(teams))
	tl, err := h.links.loadTeamList(r.Context(), a.event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	for _, team := range teams {
		obj, err := h.teamOrganiserFrom(r.Context(), a.event, team, tl)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		teamObjs = append(teamObjs, obj)
	}
	none, err := h.teams.ListParticipantsOnNoTeam(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	onNone := make([]map[string]any, 0, len(none))
	for _, p := range none {
		onNone = append(onNone, map[string]any{
			"user_id":      p.UserID,
			"email":        p.Email,
			"display_name": p.DisplayName,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"teams":         teamObjs,
		"team_size_max": a.event.TeamSizeMax,
		"on_no_team":    onNone,
	})
}

func (h *participationHTTP) teamOrganiserJSON(ctx context.Context, team model.EventTeam) (map[string]any, error) {
	people, err := h.teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	return teamOrganiserJSONFrom(team, people, h.links.teamLinkURL(team.Code)), nil
}

func teamOrganiserJSONFrom(team model.EventTeam, people []model.EventPerson, link string) map[string]any {
	members := make([]map[string]any, 0, len(people))
	for _, p := range people {
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"email":        p.Email,
			"display_name": p.DisplayName,
			"admin":        p.Admin,
		})
	}
	return map[string]any{
		"id":         team.ID,
		"slug":       team.Slug,
		"name":       team.Name,
		"code":       team.Code,
		"link":       link,
		"locked":     team.Locked(),
		"kept":       team.KeptAfterLeave,
		"created_at": rfc3339Time(team.CreatedAt),
		"members":    members,
	}
}

func (h *participationHTTP) createTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok || h.codes.limited(w, r, a.user.ID) {
		return
	}
	var req struct {
		Name       string `json:"name"`
		LookingFor string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	team, err := h.teams.CreateTeam(r.Context(), a.event, a.user.ID, req.Name, req.LookingFor, h.links.teamSiteFlatHostsOn())
	h.writeParticipantTeam(w, r, http.StatusCreated, a.user.ID, team, err)
}

// writeParticipantTeam answers a participant team write with the team as
// they now see it, or the refusal.
func (h *participationHTTP) writeParticipantTeam(w http.ResponseWriter, r *http.Request, status int, userID string, team model.EventTeam, err error) {
	if err != nil {
		writeHackError(w, err)
		return
	}
	obj, err := h.teamParticipantJSON(r.Context(), team, userID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, status, obj)
}

func (h *participationHTTP) joinTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok || h.codes.limited(w, r, a.user.ID) {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	team, err := h.teams.JoinTeam(r.Context(), a.event, a.user.ID, h.links.teamCodeFromInput(req.Code))
	h.writeParticipantTeam(w, r, http.StatusOK, a.user.ID, team, err)
}

func (h *participationHTTP) leaveTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok || h.codes.limited(w, r, a.user.ID) {
		return
	}
	if err := h.teams.LeaveTeam(r.Context(), a.event, a.user.ID); err != nil {
		writeHackError(w, err)
		return
	}
	h.links.removeOrphanTeamSites(r.Context(), a.event)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *participationHTTP) moveMember(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	team, err := h.teams.MoveMember(r.Context(), a.event, req.UserID, r.PathValue("team"))
	if err != nil {
		writeHackError(w, err)
		return
	}
	h.links.removeOrphanTeamSites(r.Context(), a.event)
	obj, err := h.teamOrganiserJSON(r.Context(), team)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *participationHTTP) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser", "participant")
	if !ok {
		return
	}
	if err := h.teams.RemoveTeamMember(r.Context(), a.event, a.member, a.user.ID, r.PathValue("user_id"), r.PathValue("team")); err != nil {
		writeHackError(w, err)
		return
	}
	h.links.removeOrphanTeamSites(r.Context(), a.event)
	w.WriteHeader(http.StatusNoContent)
}

func (h *participationHTTP) deleteTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if err := h.teams.DeleteTeam(r.Context(), a.event, r.PathValue("team")); err != nil {
		writeHackError(w, err)
		return
	}
	h.links.removeOrphanTeamSites(r.Context(), a.event)
	w.WriteHeader(http.StatusNoContent)
}

// teamParticipantJSON is a team as a participant sees it: names only, never
// anyone's email or account id. Re-reads the team row fresh (never trusting
// a value the caller already had before its own write), so a rename or a
// link rotation just committed by this same request shows up.
func (h *participationHTTP) teamParticipantJSON(ctx context.Context, team model.EventTeam, userID string) (map[string]any, error) {
	team, people, lookingFor, err := h.teams.ParticipantTeam(ctx, team)
	if err != nil {
		return nil, err
	}
	members := make([]map[string]any, 0, len(people))
	admin := false
	for _, p := range people {
		you := p.UserID == userID
		if you && p.Admin {
			admin = true
		}
		members = append(members, map[string]any{"display_name": p.DisplayName, "you": you, "admin": p.Admin})
	}
	return map[string]any{
		"slug": team.Slug, "name": team.Name, "code": team.Code,
		"link": h.links.teamLinkURL(team.Code), "admin": admin, "locked": team.Locked(), "kept": team.KeptAfterLeave,
		"looking_for": lookingFor, "members": members,
	}, nil
}
