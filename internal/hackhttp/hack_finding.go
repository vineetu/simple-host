package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"net/http"

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
	ctx := r.Context()
	teams := []any{}
	solo := []any{}
	pending := []any{}
	// Solo participants see open rosters; team members see opted-in solo people.
	// A frozen team (Codex review, SIM-340, P2: TeamWriteState.Frozen's own
	// condition, the one NewTeamRequest/JoinTeamByCode actually enforce) is
	// excluded here too -- otherwise a team whose deadline passed, or whose
	// last member just left past it, still advertises as open, inviting a
	// request that can only 409 submissions_closed with nobody left to see it.
	if !a.member.TeamID.Valid || a.member.Role == "organiser" {
		// "Who can join" is gone (decision 5): every unlocked team is
		// askable, the former code-only teams simply join this list.
		rows, err := h.database.QueryContext(ctx, `SELECT t.id,t.slug,t.name,t.looking_for,
 COALESCE(x.id::text,''),COALESCE(x.slug,''),COALESCE(x.name,''),COALESCE(x.challenge,''),COALESCE(x.prize,'')
 FROM event_teams t JOIN events e ON e.id=t.event_id LEFT JOIN event_tracks x ON x.id=t.track_id
 WHERE t.event_id=$1 AND t.locked_at IS NULL
   AND e.stage NOT IN ('judging','results','archived')
   AND NOT COALESCE(`+db.EffectiveDeadlineSQL+` <= clock_timestamp(), false)
 ORDER BY t.created_at`, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		byID := map[string]map[string]any{}
		for rows.Next() {
			var id, slug, name, note string
			var track db.EventTrack
			if err = rows.Scan(&id, &slug, &name, &note, &track.ID, &track.Slug, &track.Name, &track.Challenge, &track.Prize); err != nil {
				rows.Close()
				httpx.WriteInternal(w)
				return
			}
			var tr *db.EventTrack
			if track.ID != "" {
				tr = &track
			}
			t := map[string]any{"slug": slug, "name": name, "members": []string{}, "member_count": 0, "team_size_max": a.event.TeamSizeMax, "full": false, "track": tr, "looking_for": note}
			teams = append(teams, t)
			byID[id] = t
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		rows, err = h.database.QueryContext(ctx, `SELECT m.team_id,m.display_name FROM event_members m JOIN event_teams t ON t.id=m.team_id WHERE t.event_id=$1 AND t.locked_at IS NULL AND m.approval_status='approved' ORDER BY m.joined_at,m.user_id`, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		for rows.Next() {
			var id, name string
			if err = rows.Scan(&id, &name); err != nil {
				rows.Close()
				httpx.WriteInternal(w)
				return
			}
			if t, ok := byID[id]; ok {
				names := append(t["members"].([]string), name)
				t["members"] = names
				t["member_count"] = len(names)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		for _, t := range byID {
			t["full"] = t["member_count"].(int) >= a.event.TeamSizeMax
		}
	}
	if a.member.TeamID.Valid || a.member.Role == "organiser" {
		rows, err := h.database.QueryContext(ctx, `SELECT user_id,display_name,looking_intro FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL AND looking ORDER BY joined_at,user_id`, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		for rows.Next() {
			var id, name, intro string
			if err = rows.Scan(&id, &name, &intro); err != nil {
				rows.Close()
				httpx.WriteInternal(w)
				return
			}
			solo = append(solo, map[string]any{"user_id": id, "display_name": name, "intro": intro})
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	rows, err := h.database.QueryContext(ctx, `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,m.display_name,r.kind,r.created_at FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id JOIN event_members m ON m.event_id=r.event_id AND m.user_id=r.user_id WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`, a.event.ID, a.user.ID, a.member.TeamID, a.member.Role == "organiser")
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	for rows.Next() {
		var p db.TeamRequest
		if err = rows.Scan(&p.ID, &p.TeamID, &p.Team, &p.TeamName, &p.UserID, &p.DisplayName, &p.Kind, &p.CreatedAt); err != nil {
			rows.Close()
			httpx.WriteInternal(w)
			return
		}
		pending = append(pending, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	var looking bool
	var intro string
	if err = h.database.QueryRowContext(ctx, `SELECT looking,looking_intro FROM event_members WHERE event_id=$1 AND user_id=$2`, a.event.ID, a.user.ID).Scan(&looking, &intro); err != nil {
		httpx.WriteInternal(w)
		return
	}
	out["looking"] = looking
	out["intro"] = intro
	out["teams"] = teams
	out["solo"] = solo
	out["pending"] = pending
	if a.member.TeamID.Valid {
		var locked bool
		var note string
		var admin bool
		if err = h.database.QueryRowContext(ctx, `
			SELECT t.locked_at IS NOT NULL, t.looking_for, m.team_admin
			  FROM event_teams t JOIN event_members m ON m.team_id = t.id
			 WHERE t.id = $1 AND m.event_id = $2 AND m.user_id = $3`,
			a.member.TeamID, a.event.ID, a.user.ID).Scan(&locked, &note, &admin); err != nil && !errors.Is(err, sql.ErrNoRows) {
			httpx.WriteInternal(w)
			return
		}
		out["team"] = map[string]any{"looking_for": note, "locked": locked, "admin": admin}
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		// Membership is rechecked while locked, before editing the team.
		var team string
		var admin bool
		err = tx.QueryRowContext(r.Context(), `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved' FOR UPDATE`, a.event.ID, a.user.ID).Scan(&team, &admin)
		if err == nil && !admin {
			err = db.ErrHackTeamAdminOnly
		}
		// Same team-row lock and frozen-team check as patchTeam (Grok
		// security review, 2026-11-01): this deprecated alias writes the
		// same column and deserves the same guard, not a looser one just
		// because it predates patchTeam.
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, team)
		}
		if err == nil {
			if st, serr := db.TeamWriteStateFor(r.Context(), tx, team, false); serr != nil {
				err = serr
			} else if st.Frozen() {
				err = db.ErrHackTeamFrozen
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE event_teams SET looking_for=$2 WHERE id=$1`, team, note)
		}
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteInternal(w)
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
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
		httpx.WriteInternal(w)
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
		t, err := db.GetEventTeamByID(r.Context(), h.database, a.member.TeamID.String)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		team = t.Slug
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	state := ""
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		state, err = db.NewTeamRequest(r.Context(), tx, a.event, a.user.ID, team, userID, kind)
	}
	if err == nil {
		err = h.noticeTeamRequest(r.Context(), tx, a.event.ID, team, userID, kind, state)
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteInternal(w)
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	var answered struct{ teamID, userID, kind string }
	if err = db.LockFindingEvent(r.Context(), tx, a.event.ID); err == nil {
		// Read before it is resolved: resolving deletes the row.
		err = tx.QueryRowContext(r.Context(), `SELECT team_id, user_id, kind FROM event_team_requests WHERE event_id = $1 AND id = $2`,
			a.event.ID, id).Scan(&answered.teamID, &answered.userID, &answered.kind)
		if err == nil {
			err = db.ResolveTeamRequest(r.Context(), tx, a.event, a.user.ID, id, action)
		}
	}
	// A team's admin answered someone's request to join: tell that person.
	// An invite's answer and a withdrawal are the actor's own news.
	if err == nil && answered.kind == "request" && action != "withdraw" {
		detail := "declined"
		if action == "accept" {
			detail = "accepted"
		}
		err = db.NotifyUsers(r.Context(), tx, a.event.ID, db.NoticeRequestAnswered, answered.teamID, "", detail, answered.userID)
	}
	if err != nil {
		findingError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteInternal(w)
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
	ctx := r.Context()
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	// Lock all prospective members before teams, matching ordinary joining.
	// Fill teams no longer needs team finding on (decision 10): only the
	// stage check, rechecked here under the event row's lock.
	ev, err := db.GetEventForUpdate(ctx, tx, a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if !participantTeamStages(ev.Stage) {
		httpx.WriteError(w, 409, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	// Target size (decision 13): default min(4, team_size_max), so a plain
	// cap of 20 doesn't make one 20-person team of strangers.
	size := 4
	if ev.TeamSizeMax < size {
		size = ev.TeamSizeMax
	}
	if req.Size != nil {
		size = *req.Size
	}
	if size < 1 || size > ev.TeamSizeMax {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_team_size", "size must be between 1 and the event's team size")
		return
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id=$1 ORDER BY user_id FOR UPDATE`, ev.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id=$1 ORDER BY id FOR UPDATE`, ev.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	plan, skipped, err := db.PlanFillTeams(ctx, tx, ev, size)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if req.Apply {
		for _, p := range plan {
			if p.New {
				code, e := hackevents.RandomCode(hackTeamCodeLen)
				if e != nil {
					httpx.WriteInternal(w)
					return
				}
				if _, err = db.InsertEventTeam(ctx, tx, ev.ID, p.Team, p.Name, code, a.user.ID); err != nil {
					httpx.WriteInternal(w)
					return
				}
				// event_teams.open is no longer written (deprecated 2026-10-31).
				if err = db.RecordUsedTeamName(ctx, tx, ev.Slug, p.Team); err != nil {
					httpx.WriteInternal(w)
					return
				}
			}
			for _, m := range p.Members {
				// MoveParticipantToTeam runs EnsureTeamAdmin on the
				// destination itself, so a new team's first person placed
				// becomes its admin without any extra call here.
				if _, err = db.MoveParticipantToTeam(ctx, tx, ev.ID, m.UserID, p.Team, ev.TeamSizeMax); err != nil {
					findingError(w, err)
					return
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"plan": plan, "applied": req.Apply, "skipped": skipped, "size": size})
}

// noticeTeamRequest queues the notice for a new request or invite, inside
// the transaction that made it and after its event, member and team locks: a
// request reaches the team's admins, an invite the invited person. When the
// call instead completed an earlier offer ("joined"), a teammate's invite
// answered someone's waiting request, so that person hears they're in.
func (h *HackHandler) noticeTeamRequest(ctx context.Context, tx *sql.Tx, eventID, teamSlug, userID, kind, state string) error {
	if state != "created" && !(state == "joined" && kind == "invite") {
		return nil
	}
	var teamID, requestID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, teamSlug).Scan(&teamID); err != nil {
		return err
	}
	if state == "joined" {
		return db.NotifyUsers(ctx, tx, eventID, db.NoticeRequestAnswered, teamID, "", "accepted", userID)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM event_team_requests WHERE team_id = $1 AND user_id = $2`, teamID, userID).Scan(&requestID); err != nil {
		return err
	}
	if kind == "request" {
		return db.NotifyTeamAdmins(ctx, tx, eventID, teamID, db.NoticeJoinRequest, requestID, "")
	}
	return db.NotifyUsers(ctx, tx, eventID, db.NoticeInvite, teamID, requestID, "", userID)
}
