package hackhttp

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/app/hack/teams"
)

// Team admins (owner 2026-10-31): the creator is admin; admins approve or
// decline requests, lock or unlock, rename, edit Looking for, remove
// members, make or remove admins, make a new link. Every write here checks
// the admin flag fresh, under the right locks, re-reading it from
// event_members ... FOR UPDATE in the same transaction as the change --
// nothing trusts the event view's cached "admin" flag (security review 1).

// writeTeamAdminErr maps the teams-redesign sentinel errors to their REST
// codes; anything else is internal or event_not_found as usual.
func writeTeamAdminErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, teams.ErrTeamAdminOnly):
		httpx.WriteError(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
	case errors.Is(err, teams.ErrLastTeamAdmin):
		httpx.WriteError(w, http.StatusConflict, "last_team_admin", "make someone else admin first")
	case errors.Is(err, teams.ErrTeamKept):
		httpx.WriteError(w, http.StatusConflict, "team_kept", "this team can't take new members")
	case errors.Is(err, teams.ErrTeamLocked):
		httpx.WriteError(w, http.StatusConflict, "team_locked", "this team isn't taking new people")
	case errors.Is(err, sql.ErrNoRows):
		writeEventNotFound(w)
	default:
		httpx.WriteInternal(w)
	}
}

func writeParticipantTeamError(w http.ResponseWriter, err error) {
	var teamErr *teams.Error
	if errors.As(err, &teamErr) {
		switch teamErr.Code {
		case "teams_locked":
			httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
			return
		case "submissions_closed":
			httpx.WriteError(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed")
			return
		case "team_name_taken":
			writeTeamNameTaken(w)
			return
		}
	}
	writeTeamAdminErr(w, err)
}

// patchTeam is PATCH /v1/hack/events/{slug}/team: the caller's own team,
// admin only. name and looking_for are each optional; send only what
// changes.
func (h *HackHandler) patchTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Name       *string `json:"name"`
		LookingFor *string `json:"looking_for"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	var name string
	if req.Name != nil {
		var okName bool
		name, okName = checkHackLine(w, events.StripInvisible(*req.Name), "team_name", 1, 80)
		if !okName {
			return
		}
	}
	var lookingFor string
	if req.LookingFor != nil {
		var okNote bool
		lookingFor, okNote = checkHackLine(w, events.StripInvisible(*req.LookingFor), "looking_for", 0, 120)
		if !okNote {
			return
		}
	}
	var nameArg, noteArg *string
	if req.Name != nil {
		nameArg = &name
	}
	if req.LookingFor != nil {
		noteArg = &lookingFor
	}
	team, err := h.teamsSvc().PatchTeam(r.Context(), a.event.ID, a.user.ID, nameArg, noteArg)
	if err != nil {
		writeParticipantTeamError(w, err)
		return
	}
	obj, err := h.teamParticipantJSON(r.Context(), team, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

// setMyTeamLock is PUT /v1/hack/events/{slug}/team/lock: the caller's own
// team, admin only. Locking clears the team's pending requests and invites
// and reports the counts; unlocking a team kept after its last member left
// is refused.
func (h *HackHandler) setMyTeamLock(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	requests, invites, err := h.teamsSvc().ParticipantLock(r.Context(), a.event.ID, a.user.ID, req.Locked)
	if err != nil {
		writeParticipantTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"locked": req.Locked, "cleared": map[string]any{"requests": requests, "invites": invites}})
}

// setMyTeammateAdmin is PUT /v1/hack/events/{slug}/team/members/{user_id}/admin:
// the caller's own team, admin only. A user_id not on the caller's team is
// 404, not 403, so this never confirms where else someone belongs. Gated
// by the same participant team stages and frozen-team check as the other
// participant team handlers (patch, lock, new link): a deviation from the
// original plan, which left this route ungated (Codex review, 2026-11-01)
// -- on reflection it should follow the others, since it is still a team
// composition write, not pure bookkeeping, and the organiser's equivalent
// keeps its own separate override (no stage/deadline check, matching every
// other organiser team write).
func (h *HackHandler) setMyTeammateAdmin(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	userID := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Admin bool `json:"admin"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	err := h.teamsSvc().ParticipantAdmin(r.Context(), a.event.ID, a.user.ID, userID, req.Admin)
	if err != nil {
		writeParticipantTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": userID, "admin": req.Admin})
}

// newMyTeamLink is POST /v1/hack/events/{slug}/team/link: the caller's own
// team, admin only. The old code and link stop working at once.
func (h *HackHandler) newMyTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !teams.ParticipantTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	fresh, err := h.teamsSvc().ParticipantLink(r.Context(), a.event.ID, a.user.ID)
	if err != nil {
		writeParticipantTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": fresh.Code, "link": h.teamLinkURL(fresh.Code)})
}

// --- Organiser equivalents: the organiser picks the team by slug, never
// needs to be its admin, and is gated by organiserTeamStages (not archived)
// rather than the participant team-building stages or the frozen check. ---

func (h *HackHandler) lockOrganiserTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	requests, invites, err := h.teamsSvc().OrganiserLock(r.Context(), a.event.ID, teamSlug, a.user.ID, req.Locked)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	var teamErr *teams.Error
	if errors.As(err, &teamErr) && teamErr.Code == "member_not_found" {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeTeamAdminErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"locked": req.Locked, "cleared": map[string]any{"requests": requests, "invites": invites}})
}

func (h *HackHandler) setOrganiserTeamAdmin(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Admin bool `json:"admin"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	err := h.teamsSvc().OrganiserAdmin(r.Context(), a.event.ID, teamSlug, userID, req.Admin)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	var teamErr *teams.Error
	if errors.As(err, &teamErr) && teamErr.Code == "member_not_found" {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeTeamAdminErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": userID, "admin": req.Admin})
}

func (h *HackHandler) newOrganiserTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	fresh, err := h.teamsSvc().OrganiserLink(r.Context(), a.event.ID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"code": fresh.Code, "link": h.teamLinkURL(fresh.Code)})
}
