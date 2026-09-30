package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

func (h *HackHandler) listPeople(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	people, err := db.ListEventPeople(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(people))
	for _, p := range people {
		item := map[string]any{
			"user_id":         p.UserID,
			"email":           p.Email,
			"display_name":    p.DisplayName,
			"role":            p.Role,
			"team":            nil,
			"joined_at":       rfc3339Time(p.JoinedAt),
			"coc_accepted_at": rfc3339UTC(p.CocAcceptedAt),
		}
		if p.TeamSlug.Valid {
			item["team"] = map[string]any{"slug": p.TeamSlug.String, "name": p.TeamName.String}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *HackHandler) removePerson(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if !uuidShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	err = db.RemovePersonFromEvent(r.Context(), tx, a.event.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if errors.Is(err, db.ErrHackCannotRemoveOrganiser) {
		writeHackErr(w, http.StatusConflict, "cannot_remove_organiser", "an organiser cannot be removed from the event")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) listTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	teamObjs := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		obj, err := h.teamOrganiserJSON(r.Context(), team)
		if err != nil {
			writeInternal(w)
			return
		}
		teamObjs = append(teamObjs, obj)
	}
	none, err := db.ListParticipantsOnNoTeam(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
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
	writeJSON(w, http.StatusOK, map[string]any{
		"teams":         teamObjs,
		"team_size_max": a.event.TeamSizeMax,
		"on_no_team":    onNone,
	})
}

func (h *HackHandler) teamOrganiserJSON(ctx context.Context, team db.EventTeam) (map[string]any, error) {
	people, err := db.ListTeamPeople(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	members := make([]map[string]any, 0, len(people))
	for _, p := range people {
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"email":        p.Email,
			"display_name": p.DisplayName,
		})
	}
	return map[string]any{
		"slug":       team.Slug,
		"name":       team.Name,
		"code":       team.Code,
		"created_at": rfc3339Time(team.CreatedAt),
		"members":    members,
	}, nil
}

func (h *HackHandler) createTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	name, ok := checkHackText(w, req.Name, "name", 1, 80)
	if !ok {
		return
	}
	base := deriveTeamSlug(name)
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()

	var team db.EventTeam
	for n := 0; n < 50; n++ {
		slug := teamSlugCandidate(base, n)
		taken, err := db.TeamSlugExists(r.Context(), tx, a.event.ID, slug)
		if err != nil {
			writeInternal(w)
			return
		}
		if taken || eventNameReserved(slug) {
			continue
		}
		var code string
		var last error
		for attempt := 0; attempt < 8; attempt++ {
			code, last = randomHackCode(hackTeamCodeLen)
			if last != nil {
				writeInternal(w)
				return
			}
			team, last = db.CreateTeamAndJoin(r.Context(), tx, a.event.ID, a.user.ID, slug, name, code)
			if last == nil {
				break
			}
			if errors.Is(last, db.ErrHackAlreadyInTeam) {
				writeHackErr(w, http.StatusConflict, "already_in_team", "you are already on a team")
				return
			}
			if errors.Is(last, db.ErrHackNotParticipant) {
				writeEventNotFound(w)
				return
			}
			if dbUnique(last) && attempt < 7 {
				continue
			}
			if dbUnique(last) {
				break
			}
			writeInternal(w)
			return
		}
		if last == nil {
			if err := tx.Commit(); err != nil {
				writeInternal(w)
				return
			}
			obj, err := h.teamOrganiserJSON(r.Context(), team)
			if err != nil {
				writeInternal(w)
				return
			}
			writeJSON(w, http.StatusCreated, obj)
			return
		}
	}
	writeInternal(w)
}

func (h *HackHandler) joinTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if h.rateLimited(w, r, a.user.ID) {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	code := normalizeHackCode(req.Code)
	if code == "" {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, err := db.JoinTeamByCode(r.Context(), tx, a.event.ID, a.user.ID, code, a.event.TeamSizeMax)
	if errors.Is(err, db.ErrHackTeamNotFound) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if errors.Is(err, db.ErrHackTeamFull) {
		writeHackErr(w, http.StatusConflict, "team_full", "this team is full")
		return
	}
	if errors.Is(err, db.ErrHackAlreadyInTeam) {
		writeHackErr(w, http.StatusConflict, "already_in_team", "you are already on a team")
		return
	}
	if errors.Is(err, db.ErrHackNotParticipant) || errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	obj, err := h.teamOrganiserJSON(r.Context(), team)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, obj)
}

func (h *HackHandler) leaveTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err := db.LeaveTeam(r.Context(), tx, a.event.ID, a.user.ID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
			return
		}
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *HackHandler) moveMember(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "event_closed", "an archived event cannot be changed")
		return
	}
	var req struct {
		UserID string `json:"user_id"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if !uuidShape.MatchString(req.UserID) {
		writeEventNotFound(w)
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, err := db.MoveParticipantToTeam(r.Context(), tx, a.event.ID, req.UserID, teamSlug, a.event.TeamSizeMax)
	if errors.Is(err, db.ErrHackTeamNotFound) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if errors.Is(err, db.ErrHackTeamFull) {
		writeHackErr(w, http.StatusConflict, "team_full", "this team is full")
		return
	}
	if errors.Is(err, db.ErrHackNotParticipant) || errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	obj, err := h.teamOrganiserJSON(r.Context(), team)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, obj)
}

func (h *HackHandler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "event_closed", "an archived event cannot be changed")
		return
	}
	userID := r.PathValue("user_id")
	if !uuidShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	err = db.RemoveParticipantFromTeam(r.Context(), tx, a.event.ID, teamSlug, userID)
	if errors.Is(err, db.ErrHackTeamNotFound) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "event_closed", "an archived event cannot be changed")
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	team, err := db.GetEventTeamBySlug(r.Context(), h.database, a.event.ID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.DeleteEventTeam(r.Context(), h.database, team.ID); err != nil {
		writeInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func deriveTeamSlug(name string) string {
	s := strings.ToLower(name)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 30 {
		out = strings.Trim(out[:30], "-")
	}
	if out == "" {
		out = "group"
	}
	return out
}

func teamSlugCandidate(base string, n int) string {
	if n == 0 {
		if len(base) > 30 {
			return base[:30]
		}
		return base
	}
	suf := "-" + strconv.Itoa(n+1)
	keep := 30 - len(suf)
	if keep < 1 {
		keep = 1
	}
	root := base
	if len(root) > keep {
		root = strings.Trim(root[:keep], "-")
	}
	if root == "" {
		root = "g"
	}
	return root + suf
}
