package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// Team admins (owner 2026-10-31): the creator is admin; admins approve or
// decline requests, lock or unlock, rename, edit Looking for, remove
// members, make or remove admins, make a new link. Every write here checks
// the admin flag fresh, under the right locks, re-reading it from
// event_members ... FOR UPDATE in the same transaction as the change --
// nothing trusts the event view's cached "admin" flag (security review 1).

// lockEventStage locks the event row FOR SHARE and returns its stage,
// fresh, inside the transaction. setStage takes the event row FOR UPDATE
// as its very first step (and every team row FOR UPDATE right after), so
// this either waits for a setStage already running to commit and then
// sees its result, or runs first and makes setStage wait for it -- never a
// stage value read before this transaction even began. Lock order for
// every handler that calls this: the event row first, then member rows,
// then team rows, the same order setStage and every other hack writer
// uses (Grok security review, 2026-11-01: lockCallerTeam's comment used
// to claim the event was "already locked by loadMember's read", which it
// never was -- loadMember's read holds no lock at all).
func lockEventStage(ctx context.Context, tx *sql.Tx, eventID string) (string, error) {
	var stage string
	err := tx.QueryRowContext(ctx, `SELECT stage FROM events WHERE id = $1 FOR SHARE`, eventID).Scan(&stage)
	return stage, err
}

// lockCallerTeam locks the event row (see lockEventStage), then the
// caller's member row, then (if they are on a team) the team row.
// sql.ErrNoRows when the caller is on no team. stage is always returned
// (even on a later error) so the caller can re-check participantTeamStages
// with a value that is guaranteed current, not the one read before BEGIN.
func (h *HackHandler) lockCallerTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (team db.EventTeam, admin bool, stage string, err error) {
	stage, err = lockEventStage(ctx, tx, eventID)
	if err != nil {
		return db.EventTeam{}, false, stage, err
	}
	var teamID sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved' FOR UPDATE`,
		eventID, userID).Scan(&teamID, &admin); err != nil {
		return db.EventTeam{}, false, stage, err
	}
	if !teamID.Valid {
		return db.EventTeam{}, false, stage, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, teamID.String); err != nil {
		return db.EventTeam{}, false, stage, err
	}
	team, err = db.GetEventTeamByID(ctx, tx, teamID.String)
	return team, admin, stage, err
}

// lockCallerAndTargetTeam locks the event row (see lockEventStage), then
// the caller's and target's membership rows together, sorted by user_id
// (the same person collapses to one row), then the caller's team row --
// always in that order. Used only where a second member is named (the
// admin toggle): locking just the caller's row, then the team row, then
// the target's row (as a plain lockCallerTeam call followed by a write on
// userID would) reproduces a deadlock against that same member's own
// concurrent request naming the caller back as its target (Codex review,
// 2026-11-01) -- Postgres aborts one side with a 500 instead of either
// succeeding or cleanly refusing. Returns the caller's team id
// (sql.ErrNoRows when they are on no team), whether they are its admin,
// and the event's current stage; it does not check that target is on the
// same team, since SetTeamAdmin does that under the same locks.
func (h *HackHandler) lockCallerAndTargetTeam(ctx context.Context, tx *sql.Tx, eventID, callerID, targetID string) (teamID string, callerAdmin bool, stage string, err error) {
	stage, err = lockEventStage(ctx, tx, eventID)
	if err != nil {
		return "", false, stage, err
	}
	ids := []string{callerID, targetID}
	sort.Strings(ids)
	if ids[0] == ids[1] {
		if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`, eventID, ids[0]); err != nil {
			return "", false, stage, err
		}
	} else if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id IN ($2, $3) ORDER BY user_id FOR UPDATE`, eventID, ids[0], ids[1]); err != nil {
		return "", false, stage, err
	}
	var team sql.NullString
	if err = tx.QueryRowContext(ctx, `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved'`,
		eventID, callerID).Scan(&team, &callerAdmin); err != nil {
		return "", false, stage, err
	}
	if !team.Valid {
		return "", false, stage, sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, team.String); err != nil {
		return "", false, stage, err
	}
	return team.String, callerAdmin, stage, nil
}

// writeTeamAdminErr maps the teams-redesign sentinel errors to their REST
// codes; anything else is internal or event_not_found as usual.
func writeTeamAdminErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrHackTeamAdminOnly):
		writeHackErr(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
	case errors.Is(err, db.ErrHackLastTeamAdmin):
		writeHackErr(w, http.StatusConflict, "last_team_admin", "make someone else admin first")
	case errors.Is(err, db.ErrHackTeamKept):
		writeHackErr(w, http.StatusConflict, "team_kept", "this team can't take new members")
	case errors.Is(err, db.ErrHackTeamLocked):
		writeHackErr(w, http.StatusConflict, "team_locked", "this team isn't taking new people")
	case errors.Is(err, sql.ErrNoRows):
		writeEventNotFound(w)
	default:
		writeInternal(w)
	}
}

// patchTeam is PATCH /v1/hack/events/{slug}/team: the caller's own team,
// admin only. name and looking_for are each optional; send only what
// changes.
func (h *HackHandler) patchTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
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
		name, okName = checkHackLine(w, hackevents.StripInvisible(*req.Name), "team_name", 1, 80)
		if !okName {
			return
		}
	}
	var lookingFor string
	if req.LookingFor != nil {
		var okNote bool
		lookingFor, okNote = checkHackLine(w, hackevents.StripInvisible(*req.LookingFor), "looking_for", 0, 120)
		if !okNote {
			return
		}
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, admin, stage, err := h.lockCallerTeam(r.Context(), tx, a.event.ID, a.user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	// Re-checked fresh, under the event lock lockCallerTeam just took: the
	// pre-BEGIN check above only rejects an obviously-wrong request early,
	// cheaply, before opening a transaction (Grok security review,
	// 2026-11-01).
	if !participantTeamStages(stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	if !admin {
		writeHackErr(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
		return
	}
	if req.Name != nil || req.LookingFor != nil {
		if st, serr := db.TeamWriteStateFor(r.Context(), tx, team.ID, false); serr != nil {
			writeInternal(w)
			return
		} else if st.Frozen() {
			writeHackErr(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed")
			return
		}
	}
	if req.Name != nil {
		if err := db.RenameEventTeam(r.Context(), tx, a.event.ID, team.Slug, name); err != nil {
			if dbUnique(err) {
				writeTeamNameTaken(w)
				return
			}
			writeInternal(w)
			return
		}
	}
	if req.LookingFor != nil {
		if _, err := tx.ExecContext(r.Context(), `UPDATE event_teams SET looking_for = $2 WHERE id = $1`, team.ID, lookingFor); err != nil {
			writeInternal(w)
			return
		}
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

// setMyTeamLock is PUT /v1/hack/events/{slug}/team/lock: the caller's own
// team, admin only. Locking clears the team's pending requests and invites
// and reports the counts; unlocking a team kept after its last member left
// is refused.
func (h *HackHandler) setMyTeamLock(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, admin, stage, err := h.lockCallerTeam(r.Context(), tx, a.event.ID, a.user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if !participantTeamStages(stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	if !admin {
		writeHackErr(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
		return
	}
	if st, serr := db.TeamWriteStateFor(r.Context(), tx, team.ID, false); serr != nil {
		writeInternal(w)
		return
	} else if st.Frozen() {
		writeHackErr(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed")
		return
	}
	requests, invites, err := db.SetTeamLock(r.Context(), tx, team.ID, a.user.ID, req.Locked)
	if err != nil {
		writeTeamAdminErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locked": req.Locked, "cleared": map[string]any{"requests": requests, "invites": invites}})
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
	if !participantTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	userID := r.PathValue("user_id")
	if !uuidShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Admin bool `json:"admin"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	teamID, admin, stage, err := h.lockCallerAndTargetTeam(r.Context(), tx, a.event.ID, a.user.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if !participantTeamStages(stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	if !admin {
		writeHackErr(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
		return
	}
	if st, serr := db.TeamWriteStateFor(r.Context(), tx, teamID, false); serr != nil {
		writeInternal(w)
		return
	} else if st.Frozen() {
		writeHackErr(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed")
		return
	}
	if err := db.SetTeamAdmin(r.Context(), tx, a.event.ID, teamID, userID, req.Admin); err != nil {
		writeTeamAdminErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "admin": req.Admin})
}

// newMyTeamLink is POST /v1/hack/events/{slug}/team/link: the caller's own
// team, admin only. The old code and link stop working at once.
func (h *HackHandler) newMyTeamLink(w http.ResponseWriter, r *http.Request) {
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
	team, admin, stage, err := h.lockCallerTeam(r.Context(), tx, a.event.ID, a.user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if !participantTeamStages(stage) {
		writeHackErr(w, http.StatusConflict, "teams_locked", "teams cannot be changed at this stage")
		return
	}
	if !admin {
		writeHackErr(w, http.StatusForbidden, "team_admin_only", "only the team's admins can do that")
		return
	}
	if st, serr := db.TeamWriteStateFor(r.Context(), tx, team.ID, false); serr != nil {
		writeInternal(w)
		return
	} else if st.Frozen() {
		writeHackErr(w, http.StatusConflict, "submissions_closed", "the submission deadline has passed")
		return
	}
	if err := rotateTeamCode(r.Context(), tx, team.ID); err != nil {
		writeInternal(w)
		return
	}
	fresh, err := db.GetEventTeamByID(r.Context(), tx, team.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": fresh.Code, "link": h.teamLinkURL(fresh.Code)})
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
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	var req struct {
		Locked bool `json:"locked"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, err := db.GetEventTeamBySlug(r.Context(), tx, a.event.ID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	requests, invites, err := db.SetTeamLock(r.Context(), tx, team.ID, a.user.ID, req.Locked)
	if err != nil {
		writeTeamAdminErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"locked": req.Locked, "cleared": map[string]any{"requests": requests, "invites": invites}})
}

func (h *HackHandler) setOrganiserTeamAdmin(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		Admin bool `json:"admin"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, err := db.GetEventTeamBySlug(r.Context(), tx, a.event.ID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.SetTeamAdmin(r.Context(), tx, a.event.ID, team.ID, userID, req.Admin); err != nil {
		writeTeamAdminErr(w, err)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": userID, "admin": req.Admin})
}

func (h *HackHandler) newOrganiserTeamLink(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if !organiserTeamStages(a.event.Stage) {
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	teamSlug := strings.ToLower(strings.TrimSpace(r.PathValue("team")))
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	team, err := db.GetEventTeamBySlug(r.Context(), tx, a.event.ID, teamSlug)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if err := rotateTeamCode(r.Context(), tx, team.ID); err != nil {
		writeInternal(w)
		return
	}
	fresh, err := db.GetEventTeamByID(r.Context(), tx, team.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"code": fresh.Code, "link": h.teamLinkURL(fresh.Code)})
}
