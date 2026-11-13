package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

func (h *HackHandler) listPeople(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	people, keyed, err := h.teamsSvc().ListPeople(r.Context(), a.event.ID)
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
			// zp-hack-applications.sql. The profile fields (city, country,
			// age_range, gender) stay off this list by design (D3): they
			// are only in the Applications list and the participants CSV.
			"joined_via": p.JoinedVia,
		}
		if p.TeamSlug.Valid {
			item["team"] = map[string]any{"slug": p.TeamSlug.String, "name": p.TeamName.String}
		}
		if p.Role == "participant" {
			item["has_key"] = keyed[p.UserID]
		}
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HackHandler) removePerson(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	userID := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	err := h.teamsSvc().RemovePerson(r.Context(), a.event.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if errors.Is(err, teams.ErrCannotRemoveOrganiser) {
		httpx.WriteError(w, http.StatusConflict, "cannot_remove_organiser", "an organiser cannot be removed from the event")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) listTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	teams, err := h.teamsSvc().ListTeams(r.Context(), a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	teamObjs := make([]map[string]any, 0, len(teams))
	tl, err := h.loadTeamList(r.Context(), a.event)
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
	none, err := h.teamsSvc().ListParticipantsOnNoTeam(r.Context(), a.event.ID)
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

func (h *HackHandler) teamOrganiserJSON(ctx context.Context, team model.EventTeam) (map[string]any, error) {
	people, err := h.teamsSvc().TeamPeople(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	return teamOrganiserJSONFrom(team, people, h.teamLinkURL(team.Code)), nil
}

// teamOrganiserJSONFrom is teamOrganiserJSON with the members already read.
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

func (h *HackHandler) createTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if h.rateLimited(w, r, a.user.ID) {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Name       string `json:"name"`
		Open       *bool  `json:"open"`
		LookingFor string `json:"looking_for"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	name, ok := checkHackLine(w, events.StripInvisible(req.Name), "team_name", 1, 80)
	if !ok {
		return
	}
	note, ok := checkHackLine(w, events.StripInvisible(req.LookingFor), "looking_for", 0, 120)
	if !ok {
		return
	}
	team, err := h.teamsSvc().CreateTeam(r.Context(), a.event, a.user.ID, name, note, h.teamSiteFlatHostsOn(), eventNameReserved)
	var teamErr *teams.Error
	switch {
	case errors.As(err, &teamErr) && teamErr.Code == "submissions_closed":
		httpx.WriteError(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed, so new teams can't start")
		return
	case errors.As(err, &teamErr) && teamErr.Code == "team_name_taken":
		writeTeamNameTaken(w)
		return
	case errors.Is(err, teams.ErrAlreadyInTeam):
		httpx.WriteError(w, http.StatusConflict, "already_in_team", "you are already on a team")
		return
	case errors.Is(err, teams.ErrNotParticipant):
		writeEventNotFound(w)
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	obj, err := h.teamParticipantJSON(r.Context(), team, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, obj)
}

func (h *HackHandler) joinTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if h.rateLimited(w, r, a.user.ID) {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	code := h.teamCodeFromInput(req.Code)
	if code == "" {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	team, err := h.teamsSvc().JoinTeam(r.Context(), a.event.ID, a.user.ID, code, a.event.TeamSizeMax)
	var teamErr *teams.Error
	switch {
	case errors.As(err, &teamErr) && teamErr.Code == "teams_locked":
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	case errors.As(err, &teamErr) && teamErr.Code == "submissions_closed":
		httpx.WriteError(w, http.StatusConflict, "submissions_closed", "that team's deadline has passed, so it can't take new members")
		return
	case errors.Is(err, teams.ErrTeamNotFound):
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	case errors.Is(err, teams.ErrTeamFull):
		httpx.WriteError(w, http.StatusConflict, "team_full", "this team is full")
		return
	case errors.Is(err, teams.ErrTeamLocked):
		httpx.WriteError(w, http.StatusConflict, "team_locked", "this team isn't taking new people")
		return
	case errors.Is(err, teams.ErrAlreadyInTeam):
		httpx.WriteError(w, http.StatusConflict, "already_in_team", "you are already on a team")
		return
	case errors.Is(err, teams.ErrNotParticipant) || errors.Is(err, sql.ErrNoRows):
		writeEventNotFound(w)
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	obj, err := h.teamParticipantJSON(r.Context(), team, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *HackHandler) leaveTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if h.rateLimited(w, r, a.user.ID) {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	if err := h.teamsSvc().LeaveTeam(r.Context(), a.event.ID, a.user.ID); err != nil {
		if errors.Is(err, teams.ErrTeamFrozen) {
			httpx.WriteError(w, http.StatusConflict, "submissions_closed", "your team's deadline has passed, so the team can't change; ask the organiser")
		} else if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *HackHandler) moveMember(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an archived event cannot be changed")
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if !sites.UUIDShape.MatchString(req.UserID) {
		writeEventNotFound(w)
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	team, err := h.teamsSvc().MoveMember(r.Context(), a.event.ID, req.UserID, teamSlug, a.event.TeamSizeMax)
	if errors.Is(err, teams.ErrTeamNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if errors.Is(err, teams.ErrTeamFull) {
		httpx.WriteError(w, http.StatusConflict, "team_full", "this team is full")
		return
	}
	if errors.Is(err, teams.ErrTeamKept) {
		httpx.WriteError(w, http.StatusConflict, "team_kept", "this team can't take new members")
		return
	}
	if errors.Is(err, teams.ErrNotParticipant) || errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
	obj, err := h.teamOrganiserJSON(r.Context(), team)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *HackHandler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser", "participant")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an archived event cannot be changed")
		return
	}
	userID := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	participant := a.member.Role == "participant"
	teamName := ""
	if participant {
		if !teams.ParticipantTeamStages(a.event.Stage) || !a.member.TeamID.Valid || userID == a.user.ID {
			findingError(w, teams.ErrFindingForbidden)
			return
		}
		team, err := h.teamsSvc().GetTeam(r.Context(), a.member.TeamID.String)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		if teamSlug != "" && teamSlug != team.Slug {
			writeEventNotFound(w)
			return
		}
		teamSlug = team.Slug
		teamName = team.Name
	}
	err := h.teamsSvc().RemoveTeamMember(r.Context(), a.event.ID, a.user.ID, userID, teamSlug, teamName, participant)
	switch {
	case errors.Is(err, teams.ErrTeamAdminOnly):
		httpx.WriteError(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
		return
	case errors.Is(err, teams.ErrTeamFrozen):
		findingError(w, err)
		return
	case errors.Is(err, teams.ErrTeamNotFound):
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	case errors.Is(err, sql.ErrNoRows):
		writeEventNotFound(w)
		return
	case err != nil:
		httpx.WriteInternal(w)
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an archived event cannot be changed")
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	if err := h.teamsSvc().DeleteTeam(r.Context(), a.event.ID, teamSlug); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
	w.WriteHeader(http.StatusNoContent)
}

// teamParticipantJSON is a team as a participant sees it: names only, never
// anyone's email or account id. Re-reads the team row fresh (never trusting
// a value the caller already had before its own write), so a rename or a
// link rotation just committed by this same request shows up.
func (h *HackHandler) teamParticipantJSON(ctx context.Context, team model.EventTeam, userID string) (map[string]any, error) {
	team, people, lookingFor, err := h.teamsSvc().ParticipantTeam(ctx, team)
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
		"link": h.teamLinkURL(team.Code), "admin": admin, "locked": team.Locked(), "kept": team.KeptAfterLeave,
		"looking_for": lookingFor, "members": members,
	}, nil
}

func writeTeamNameTaken(w http.ResponseWriter) {
	httpx.WriteError(w, http.StatusConflict, "team_name_taken", "another team already has that name")
}

// stripInvisible moved to internal/rules/hackevents.StripInvisible in
// cleanup 3.2 slice 1c: pure text normalization, no handler dependency.
