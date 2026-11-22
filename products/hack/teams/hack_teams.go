package teams

import (
	"context"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *handler) listPeople(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	people, err := h.Teams.ListPeople(r.Context(), a.Event.ID)
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
			"primary_organiser": a.Event.CreatedBy.Valid && p.UserID == a.Event.CreatedBy.String,
			"team":              nil,
			"joined_at":         core.RFC3339Time(p.JoinedAt),
			"coc_accepted_at":   core.RFC3339(p.CocAcceptedAt),
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

func (h *handler) removePerson(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if err := h.Teams.RemovePerson(r.Context(), a.Event, r.PathValue("user_id")); err != nil {
		core.WriteError(w, err)
		return
	}
	h.Links.RemoveOrphanTeamSites(r.Context(), a.Event)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) listTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	teams, err := h.Teams.ListTeams(r.Context(), a.Event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	teamObjs := make([]map[string]any, 0, len(teams))
	tl, err := h.Links.LoadTeamList(r.Context(), a.Event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	for _, team := range teams {
		obj, err := h.TeamOrganiserFrom(r.Context(), a.Event, team, tl)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		teamObjs = append(teamObjs, obj)
	}
	none, err := h.Teams.ListParticipantsOnNoTeam(r.Context(), a.Event.ID)
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
		"team_size_max": a.Event.TeamSizeMax,
		"on_no_team":    onNone,
	})
}

func (h *handler) teamOrganiserJSON(ctx context.Context, team model.EventTeam) (map[string]any, error) {
	people, err := h.Teams.TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	return core.TeamOrganiserJSONFrom(team, people, h.Links.TeamLinkURL(team.Code)), nil
}

func (h *handler) createTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok || h.Codes.Limited(w, r, a.User.ID) {
		return
	}
	var req struct {
		Name       string `json:"name"`
		LookingFor string `json:"looking_for"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	team, err := h.Teams.CreateTeam(r.Context(), a.Event, a.User.ID, req.Name, req.LookingFor, h.Links.TeamSiteFlatHostsOn())
	h.writeParticipantTeam(w, r, http.StatusCreated, a.User.ID, team, err)
}

// writeParticipantTeam answers a participant team write with the team as
// they now see it, or the refusal.
func (h *handler) writeParticipantTeam(w http.ResponseWriter, r *http.Request, status int, userID string, team model.EventTeam, err error) {
	if err != nil {
		core.WriteError(w, err)
		return
	}
	obj, err := h.teamParticipantJSON(r.Context(), team, userID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, status, obj)
}

func (h *handler) joinTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok || h.Codes.Limited(w, r, a.User.ID) {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	team, err := h.Teams.JoinTeam(r.Context(), a.Event, a.User.ID, h.Links.TeamCodeFromInput(req.Code))
	h.writeParticipantTeam(w, r, http.StatusOK, a.User.ID, team, err)
}

func (h *handler) leaveTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok || h.Codes.Limited(w, r, a.User.ID) {
		return
	}
	if err := h.Teams.LeaveTeam(r.Context(), a.Event, a.User.ID); err != nil {
		core.WriteError(w, err)
		return
	}
	h.Links.RemoveOrphanTeamSites(r.Context(), a.Event)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *handler) moveMember(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	team, err := h.Teams.MoveMember(r.Context(), a.Event, req.UserID, r.PathValue("team"))
	if err != nil {
		core.WriteError(w, err)
		return
	}
	h.Links.RemoveOrphanTeamSites(r.Context(), a.Event)
	obj, err := h.teamOrganiserJSON(r.Context(), team)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *handler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser", "participant")
	if !ok {
		return
	}
	if err := h.Teams.RemoveTeamMember(r.Context(), a.Event, a.Member, a.User.ID, r.PathValue("user_id"), r.PathValue("team")); err != nil {
		core.WriteError(w, err)
		return
	}
	h.Links.RemoveOrphanTeamSites(r.Context(), a.Event)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if err := h.Teams.DeleteTeam(r.Context(), a.Event, r.PathValue("team")); err != nil {
		core.WriteError(w, err)
		return
	}
	h.Links.RemoveOrphanTeamSites(r.Context(), a.Event)
	w.WriteHeader(http.StatusNoContent)
}

// teamParticipantJSON is a team as a participant sees it: names only, never
// anyone's email or account id. Re-reads the team row fresh (never trusting
// a value the caller already had before its own write), so a rename or a
// link rotation just committed by this same request shows up.
func (h *handler) teamParticipantJSON(ctx context.Context, team model.EventTeam, userID string) (map[string]any, error) {
	team, people, lookingFor, err := h.Teams.ParticipantTeam(ctx, team)
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
		"link": h.Links.TeamLinkURL(team.Code), "admin": admin, "locked": team.Locked(), "kept": team.KeptAfterLeave,
		"looking_for": lookingFor, "members": members,
	}, nil
}
