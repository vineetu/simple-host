package hackhttp

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"net/http"

	"github.com/vsriram/simple-host/internal/app/hack/teams"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func findingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrHackFindingStage):
		httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
	case errors.Is(err, db.ErrHackFindingOff):
		httpx.WriteError(w, 409, "team_finding_off", "team finding is off")
	case errors.Is(err, db.ErrHackTeamFull):
		httpx.WriteError(w, 409, "team_full", "this team is full")
	case errors.Is(err, db.ErrHackTeamLocked):
		httpx.WriteError(w, 409, "team_locked", "this team isn't taking new people")
	case errors.Is(err, db.ErrHackTeamKept):
		httpx.WriteError(w, 409, "team_kept", "this team can't take new members")
	case errors.Is(err, db.ErrHackLastTeamAdmin):
		httpx.WriteError(w, 409, "last_team_admin", "make someone else admin first")
	case errors.Is(err, db.ErrHackTeamAdminOnly):
		httpx.WriteError(w, 403, "team_admin_only", "only the team's admins can do that")
	case errors.Is(err, db.ErrHackAlreadyInTeam):
		httpx.WriteError(w, 409, "already_in_team", "you are already on a team")
	case errors.Is(err, db.ErrHackTeamFrozen):
		httpx.WriteError(w, 409, "submissions_closed", "the team's deadline has passed")
	case errors.Is(err, db.ErrHackFindingForbidden), errors.Is(err, db.ErrHackNotParticipant):
		httpx.WriteError(w, 403, "team_action_unavailable", "this team action is not available")
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, db.ErrHackTeamNotFound):
		writeEventNotFound(w)
	default:
		httpx.WriteInternal(w)
	}
}

func (h *HackHandler) findingAccess(w http.ResponseWriter, r *http.Request, write bool) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), write, "participant", "organiser")
	if !ok {
		return a, false
	}
	if write && !participantTeamStages(a.event.Stage) {
		httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
		return a, false
	}
	if !a.event.TeamFinding {
		findingError(w, db.ErrHackFindingOff)
		return a, false
	}
	return a, true
}

func (h *HackHandler) listOpenTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant", "organiser")
	if !ok {
		return
	}
	out := map[string]any{"enabled": a.event.TeamFinding, "teams": []any{}, "solo": []any{}, "pending": []any{}, "looking": false, "intro": ""}
	if !a.event.TeamFinding {
		httpx.WriteJSON(w, 200, out)
		return
	}
	directory, err := h.teamsSvc().ListOpenTeams(r.Context(), a.event.ID, a.user.ID, a.member)
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
func (h *HackHandler) setTeamFinding(w http.ResponseWriter, r *http.Request) {
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
	if !decodeHackJSON(w, r, &req) {
		return
	}
	note, ok := checkHackLine(w, hackevents.StripInvisible(req.LookingFor), "looking_for", 0, 120)
	if !ok {
		return
	}
	err := h.teamsSvc().SetTeamFinding(r.Context(), a.event.ID, a.user.ID, note)
	if err != nil {
		findingError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true, "deprecated": "use PATCH /v1/hack/events/{slug}/team"})
}
func (h *HackHandler) setLooking(w http.ResponseWriter, r *http.Request) {
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
	if !decodeHackJSON(w, r, &req) {
		return
	}
	intro, ok := checkHackLine(w, req.Intro, "intro", 0, 120)
	if !ok {
		return
	}
	err := h.teamsSvc().SetLooking(r.Context(), a.event.ID, a.user.ID, req.Looking, intro)
	if err != nil {
		findingError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}
func (h *HackHandler) createTeamRequest(w http.ResponseWriter, r *http.Request) {
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
		if !decodeHackJSON(w, r, &req) {
			return
		}
		if !sites.UUIDShape.MatchString(req.UserID) || !a.member.TeamID.Valid {
			writeEventNotFound(w)
			return
		}
		userID = req.UserID
		t, err := h.teamsSvc().GetTeam(r.Context(), a.member.TeamID.String)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		team = t.Slug
	}
	state, err := h.teamsSvc().CreateTeamRequest(r.Context(), a.event, a.user.ID, team, userID, kind)
	if err != nil {
		findingError(w, err)
		return
	}
	status := 200
	if state == "created" {
		status = 201
	}
	httpx.WriteJSON(w, status, map[string]any{"ok": true, "state": state})
}
func (h *HackHandler) resolveTeamRequest(w http.ResponseWriter, r *http.Request) {
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
	err := h.teamsSvc().ResolveTeamRequest(r.Context(), a.event, a.user.ID, id, action)
	if err != nil {
		findingError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"ok": true})
}

func (h *HackHandler) fillTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Apply bool `json:"apply"`
		Size  *int `json:"size"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	plan, skipped, size, err := h.teamsSvc().FillTeams(r.Context(), a.event.ID, a.user.ID, req.Size, req.Apply)
	if err != nil {
		var teamErr *teams.Error
		if errors.As(err, &teamErr) {
			switch teamErr.Code {
			case "teams_locked":
				httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
			case "invalid_team_size":
				httpx.WriteError(w, http.StatusBadRequest, "invalid_team_size", "size must be between 1 and the event's team size")
			default:
				httpx.WriteInternal(w)
			}
		} else {
			findingError(w, err)
		}
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"plan": plan, "applied": req.Apply, "skipped": skipped, "size": size})
}
