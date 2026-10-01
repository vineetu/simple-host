package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

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
	keyed, err := db.EventKeyHolders(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
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
		}
		if p.TeamSlug.Valid {
			item["team"] = map[string]any{"slug": p.TeamSlug.String, "name": p.TeamName.String}
		}
		if p.Role == "participant" {
			item["has_key"] = keyed[p.UserID]
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
	if !organiserTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
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
	oldTeam := memberTeamID(r.Context(), tx, a.event.ID, userID)
	err = db.RemovePersonFromEvent(r.Context(), tx, a.event.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if errors.Is(err, db.ErrHackCannotRemoveOrganiser) {
		writeHackErr(w, http.StatusConflict, "cannot_remove_organiser", "an organiser cannot be removed from the event")
		return
	}
	if err == nil && oldTeam != "" {
		// Emptied teams are gone already; rotate a team that is left.
		var still bool
		if qerr := tx.QueryRowContext(r.Context(), `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, oldTeam).Scan(&still); qerr != nil {
			err = qerr
		} else if still {
			err = rotateTeamCode(r.Context(), tx, oldTeam)
		}
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.RevokeStaleTeamKeys(r.Context(), tx, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
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
	teams, err := db.ListEventTeams(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	teamObjs := make([]map[string]any, 0, len(teams))
	tl, err := h.loadTeamList(r.Context(), a.event)
	if err != nil {
		writeInternal(w)
		return
	}
	for _, team := range teams {
		obj, err := h.teamOrganiserFrom(r.Context(), a.event, team, tl)
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
	return teamOrganiserJSONFrom(team, people), nil
}

// teamOrganiserJSONFrom is teamOrganiserJSON with the members already read.
func teamOrganiserJSONFrom(team db.EventTeam, people []db.EventPerson) map[string]any {
	members := make([]map[string]any, 0, len(people))
	for _, p := range people {
		members = append(members, map[string]any{
			"user_id":      p.UserID,
			"email":        p.Email,
			"display_name": p.DisplayName,
		})
	}
	return map[string]any{
		"id":         team.ID,
		"slug":       team.Slug,
		"name":       team.Name,
		"code":       team.Code,
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
	name, ok := checkHackLine(w, stripInvisible(req.Name), "team_name", 1, 80)
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
	// After the event's deadline a new team would be frozen from birth.
	var closed bool
	if err := tx.QueryRowContext(r.Context(), `
		SELECT COALESCE(submission_deadline <= clock_timestamp(), false) FROM events WHERE id = $1 FOR SHARE`, a.event.ID).Scan(&closed); err != nil {
		writeInternal(w)
		return
	}
	if closed {
		writeHackErr(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed, so new teams can't start")
		return
	}
	if taken, err := teamNameTaken(r.Context(), tx, a.event.ID, name); err != nil {
		writeInternal(w)
		return
	} else if taken {
		writeTeamNameTaken(w)
		return
	}

	var team db.EventTeam
	for n := 0; n < 50; n++ {
		slug := teamSlugCandidate(base, n)
		taken, err := db.TeamSlugExists(r.Context(), tx, a.event.ID, slug)
		if err != nil {
			writeInternal(w)
			return
		}
		if taken || eventNameReserved(slug) || validateSiteShape(slug) != nil || validateSiteReserved(slug) != nil {
			continue
		}
		// A name whose site belonged to an earlier team is never reused: the
		// new team must not inherit its work, data or visitors.
		if used, err := db.TeamNameEverUsed(r.Context(), tx, a.event.Slug, a.event.AccountID, slug); err != nil {
			writeInternal(w)
			return
		} else if used {
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
			last = trySavepoint(r.Context(), tx, func() error {
				var terr error
				team, terr = db.CreateTeamAndJoin(r.Context(), tx, a.event.ID, a.user.ID, slug, name, code)
				return terr
			})
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
			if dbUnique(last) && strings.Contains(last.Error(), "event_teams_name_idx") {
				writeTeamNameTaken(w)
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
			if err := db.RecordUsedTeamName(r.Context(), tx, a.event.Slug, slug); err != nil {
				writeInternal(w)
				return
			}
			if err := tx.Commit(); err != nil {
				writeInternal(w)
				return
			}
			obj, err := h.teamParticipantJSON(r.Context(), team, a.user.ID)
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
	// A team past its deadline takes nobody new (they could never publish
	// or leave).
	if st, serr := db.TeamWriteStateFor(r.Context(), tx, team.ID, true); serr != nil {
		writeInternal(w)
		return
	} else if st.Frozen() {
		writeHackErr(w, http.StatusConflict, "submissions_closed", "that team's deadline has passed, so it can't take new members")
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	obj, err := h.teamParticipantJSON(r.Context(), team, a.user.ID)
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
	if h.rateLimited(w, r, a.user.ID) {
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
		if errors.Is(err, db.ErrHackTeamFrozen) {
			writeHackErr(w, http.StatusConflict, "submissions_closed", "your team's deadline has passed, so the team can't change; ask the organiser")
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
			return
		}
		writeInternal(w)
		return
	}
	if err := db.RevokeStaleTeamKeys(r.Context(), tx, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
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
	oldTeam := memberTeamID(r.Context(), tx, a.event.ID, req.UserID)
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
	if oldTeam != "" && oldTeam != team.ID {
		if err := rotateTeamCode(r.Context(), tx, oldTeam); err != nil {
			writeInternal(w)
			return
		}
	}
	if err := db.RevokeStaleTeamKeys(r.Context(), tx, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
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
	oldTeam := memberTeamID(r.Context(), tx, a.event.ID, userID)
	err = db.RemoveParticipantFromTeam(r.Context(), tx, a.event.ID, teamSlug, userID)
	if errors.Is(err, db.ErrHackTeamNotFound) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err == nil && oldTeam != "" {
		err = rotateTeamCode(r.Context(), tx, oldTeam)
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.RevokeStaleTeamKeys(r.Context(), tx, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
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
	// Its members' keys go with it, and its site to Recently deleted.
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE team_id = $1)`, team.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := db.DeleteEventTeam(r.Context(), tx, team.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.removeOrphanTeamSites(r.Context(), a.event)
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

// teamParticipantJSON is a team as a participant sees it: names only, never
// anyone's email or account id.
func (h *HackHandler) teamParticipantJSON(ctx context.Context, team db.EventTeam, userID string) (map[string]any, error) {
	people, err := db.ListTeamPeople(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	members := make([]map[string]any, 0, len(people))
	for _, p := range people {
		members = append(members, map[string]any{"display_name": p.DisplayName, "you": p.UserID == userID})
	}
	return map[string]any{"slug": team.Slug, "name": team.Name, "code": team.Code, "members": members}, nil
}

// rotateTeamCode gives a team a new code, so someone the organiser took off
// it cannot walk back in with the old one. A team deleted for being empty
// has nothing to rotate.
func rotateTeamCode(ctx context.Context, q db.Querier, teamID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := randomHackCode(hackTeamCodeLen)
		if err != nil {
			return err
		}
		err = trySavepoint(ctx, q, func() error {
			_, uerr := q.ExecContext(ctx, `UPDATE event_teams SET code = $2 WHERE id = $1`, teamID, code)
			return uerr
		})
		if err == nil || !dbUnique(err) {
			return err
		}
	}
	return errors.New("could not find a free team code")
}

// memberTeamID is the team a person is on before an organiser's change. It
// locks their row for the rest of the transaction, so a switch of team made
// at the same moment waits and the team they really leave is the one read.
func memberTeamID(ctx context.Context, q db.Querier, eventID, userID string) string {
	var team sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`, eventID, userID).Scan(&team); err != nil || !team.Valid {
		return ""
	}
	return team.String
}

// teamNameTaken: another team of the event already has this name (any case).
func teamNameTaken(ctx context.Context, q db.Querier, eventID, name string) (bool, error) {
	var taken bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND lower(name) = lower($2))`, eventID, name).Scan(&taken)
	return taken, err
}

func writeTeamNameTaken(w http.ResponseWriter) {
	writeHackErr(w, http.StatusConflict, "team_name_taken", "another team already has that name")
}

// stripInvisible drops every character that is not seen (format
// characters, variation selectors, fillers), so two team names that look the
// same are the same name. An emoji sequence may lose its joiners: a
// team name is a label, not a place for composed emoji.
func stripInvisible(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.Is(unicode.Cf, r),
			r >= 0xFE00 && r <= 0xFE0F, r >= 0xE0100 && r <= 0xE01EF,
			r >= 0x180B && r <= 0x180F, r == 0x034F, r == 0x17B4, r == 0x17B5,
			r == 0x2065, r >= 0xFFF0 && r <= 0xFFF8, r >= 0x1D173 && r <= 0x1D17A, r >= 0xE0000 && r <= 0xE0FFF,
			r == 0x115F, r == 0x1160, r == 0x3164, r == 0xFFA0:
			return -1
		}
		return r
	}, s)
}
