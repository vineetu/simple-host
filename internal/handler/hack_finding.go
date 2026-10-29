package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/db"
)

func findingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrHackFindingOff):
		writeHackErr(w, 409, "team_finding_off", "team finding is off")
	case errors.Is(err, db.ErrHackTeamFull):
		writeHackErr(w, 409, "team_full", "this team is full")
	case errors.Is(err, db.ErrHackAlreadyInTeam):
		writeHackErr(w, 409, "already_in_team", "you are already on a team")
	case errors.Is(err, db.ErrHackTeamFrozen):
		writeHackErr(w, 409, "submissions_closed", "the team's deadline has passed")
	case errors.Is(err, db.ErrHackFindingForbidden), errors.Is(err, db.ErrHackNotParticipant):
		writeHackErr(w, 403, "team_action_unavailable", "this team action is not available")
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, db.ErrHackTeamNotFound):
		writeEventNotFound(w)
	default:
		writeInternal(w)
	}
}

func (h *HackHandler) findingAccess(w http.ResponseWriter, r *http.Request, write bool) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), write, "participant", "organiser")
	if !ok {
		return a, false
	}
	if write && !participantTeamStages(a.event.Stage) {
		writeHackErr(w, 409, "teams_locked", "teams cannot be changed at this stage")
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
		writeJSON(w, 200, out)
		return
	}
	ctx := r.Context()
	teams := []any{}
	solo := []any{}
	pending := []any{}
	// Solo participants see open rosters; team members see opted-in solo people.
	if !a.member.TeamID.Valid || a.member.Role == "organiser" {
		all, err := db.ListEventTeams(ctx, h.database, a.event.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		for _, t := range all {
			var open bool
			var note string
			if err = h.database.QueryRowContext(ctx, `SELECT open,looking_for FROM event_teams WHERE id=$1`, t.ID).Scan(&open, &note); err != nil {
				writeInternal(w)
				return
			}
			if !open {
				continue
			}
			people, err := db.ListTeamPeople(ctx, h.database, t.ID)
			if err != nil {
				writeInternal(w)
				return
			}
			names := []string{}
			for _, p := range people {
				names = append(names, p.DisplayName)
			}
			track, err := db.TeamTrack(ctx, h.database, t.ID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				writeInternal(w)
				return
			}
			teams = append(teams, map[string]any{"slug": t.Slug, "name": t.Name, "members": names, "member_count": len(names), "team_size_max": a.event.TeamSizeMax, "track": track, "looking_for": note})
		}
	}
	if a.member.TeamID.Valid || a.member.Role == "organiser" {
		rows, err := h.database.QueryContext(ctx, `SELECT user_id,display_name,looking_intro FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL AND looking ORDER BY joined_at,user_id`, a.event.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		for rows.Next() {
			var id, name, intro string
			if err = rows.Scan(&id, &name, &intro); err != nil {
				rows.Close()
				writeInternal(w)
				return
			}
			solo = append(solo, map[string]any{"user_id": id, "display_name": name, "intro": intro})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			writeInternal(w)
			return
		}
	}
	rows, err := h.database.QueryContext(ctx, `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,m.display_name,r.kind FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id JOIN event_members m ON m.event_id=r.event_id AND m.user_id=r.user_id WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`, a.event.ID, a.user.ID, a.member.TeamID, a.member.Role == "organiser")
	if err != nil {
		writeInternal(w)
		return
	}
	for rows.Next() {
		var p db.TeamRequest
		if err = rows.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.DisplayName, &p.Kind); err != nil {
			rows.Close()
			writeInternal(w)
			return
		}
		pending = append(pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		writeInternal(w)
		return
	}
	var looking bool
	var intro string
	if err = h.database.QueryRowContext(ctx, `SELECT looking,looking_intro FROM event_members WHERE event_id=$1 AND user_id=$2`, a.event.ID, a.user.ID).Scan(&looking, &intro); err != nil {
		writeInternal(w)
		return
	}
	out["looking"] = looking
	out["intro"] = intro
	out["teams"] = teams
	out["solo"] = solo
	out["pending"] = pending
	if a.member.TeamID.Valid {
		var open bool
		var note string
		if err = h.database.QueryRowContext(ctx, `SELECT open,looking_for FROM event_teams WHERE id=$1`, a.member.TeamID).Scan(&open, &note); err != nil {
			writeInternal(w)
			return
		}
		out["team"] = map[string]any{"open": open, "looking_for": note}
	}
	writeJSON(w, 200, out)
}

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
	note, ok := checkHackLine(w, req.LookingFor, "looking_for", 0, 120)
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		// Membership is rechecked while locked, before editing the team.
		var team string
		err = tx.QueryRowContext(r.Context(), `SELECT team_id FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved' FOR UPDATE`, a.event.ID, a.user.ID).Scan(&team)
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE event_teams SET open=$2,looking_for=$3 WHERE id=$1`, team, req.Open, note)
		}
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		var res sql.Result
		res, err = tx.ExecContext(r.Context(), `UPDATE event_members SET looking=$3,looking_intro=$4 WHERE event_id=$1 AND user_id=$2 AND role='participant' AND approval_status='approved' AND team_id IS NULL`, a.event.ID, a.user.ID, req.Looking, intro)
		if err == nil {
			n, _ := res.RowsAffected()
			if n == 0 {
				err = db.ErrHackFindingForbidden
			}
		}
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
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
		if !uuidShape.MatchString(req.UserID) || !a.member.TeamID.Valid {
			writeEventNotFound(w)
			return
		}
		userID = req.UserID
		t, err := db.GetEventTeamByID(r.Context(), h.database, a.member.TeamID.String)
		if err != nil {
			writeInternal(w)
			return
		}
		team = t.Slug
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		err = db.NewTeamRequest(r.Context(), tx, a.event, a.user.ID, team, userID, kind)
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true})
}
func (h *HackHandler) resolveTeamRequest(w http.ResponseWriter, r *http.Request) {
	a, ok := h.findingAccess(w, r, true)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !uuidShape.MatchString(id) {
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		err = db.ResolveTeamRequest(r.Context(), tx, a.event, a.user.ID, id, action)
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (h *HackHandler) fillTeams(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, 409, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Apply bool `json:"apply"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	// Lock all prospective members before teams, matching ordinary joining.
	ev, err := db.GetEventForUpdate(ctx, tx, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if !ev.TeamFinding {
		findingError(w, db.ErrHackFindingOff)
		return
	}
	var past bool
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(submission_deadline<=clock_timestamp(),false) FROM events WHERE id=$1`, ev.ID).Scan(&past); err != nil {
		writeInternal(w)
		return
	}
	if past {
		findingError(w, db.ErrHackTeamFrozen)
		return
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id=$1 ORDER BY user_id FOR UPDATE`, ev.ID); err != nil {
		writeInternal(w)
		return
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id=$1 ORDER BY id FOR UPDATE`, ev.ID); err != nil {
		writeInternal(w)
		return
	}
	plan, err := db.PlanFillTeams(ctx, tx, ev)
	if err != nil {
		writeInternal(w)
		return
	}
	if req.Apply {
		for _, p := range plan {
			if p.New {
				code, e := randomHackCode(hackTeamCodeLen)
				if e != nil {
					writeInternal(w)
					return
				}
				if _, err = db.InsertEventTeam(ctx, tx, ev.ID, p.Team, p.Name, code, a.user.ID); err != nil {
					writeInternal(w)
					return
				}
				if err = db.RecordUsedTeamName(ctx, tx, ev.Slug, p.Team); err != nil {
					writeInternal(w)
					return
				}
			}
			for _, m := range p.Members {
				if _, err = db.MoveParticipantToTeam(ctx, tx, ev.ID, m.UserID, p.Team, ev.TeamSizeMax); err != nil {
					findingError(w, err)
					return
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"plan": plan, "applied": req.Apply})
}
