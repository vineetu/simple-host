package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

// Leave this event (SIM-340). One route, self only: the server decides
// which of six cases applies (build plan section 1) under a lock on the
// event row, every time, so a stale dialog can never act on a role or
// membership that changed since it was opened. Owner decision 2026-10-31
// widened this twice: any member may leave any time before the event ends
// (the only remaining refusal is an archived event), and a rejected
// applicant is no longer a special case at all -- leaving deletes their
// row exactly like a pending applicant's withdraw, so they can apply again
// later and the organiser decides again, with no extra machinery to stop
// it. The earlier "rejected can never leave" rule is superseded.

func (h *HackHandler) registerLeave(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/leave", wrap(h.previewLeaveEvent))
	mux.Handle("POST /v1/hack/events/{slug}/leave", wrap(h.postLeaveEvent))
}

// leaveCaseFor names which of the six outcomes applies to one membership,
// from role, approval status and how many other organisers the event has.
// A pending or rejected participant is the same "applicant" case either
// way: withdrawing and leaving a rejected application do the same thing
// (delete the row), so there is nothing left to tell them apart on. An
// organiser with nobody else left is last_organiser whether or not they
// are the creator; a creator with others is creator; any other organiser
// with others is co_organiser.
func leaveCaseFor(role, approvalStatus string, isCreator bool, otherOrganisers int) string {
	switch role {
	case "participant":
		if approvalStatus == "pending" || approvalStatus == "rejected" {
			return "applicant"
		}
		return "participant"
	case "judge":
		return "judge"
	case "organiser":
		if otherOrganisers == 0 {
			return "last_organiser"
		}
		if isCreator {
			return "creator"
		}
		return "co_organiser"
	}
	return ""
}

// loadLeaveSelf is GetEventBySlug plus the caller's own membership row, for
// GET/POST .../leave: any role, any approval status. Unlike loadMember
// there is no admin bypass -- a platform admin who never joined gets 404
// event_not_found like anyone else (build plan 2.1: "Do not use
// loadMember(..., write=false): it synthesises organiser access for the
// admin key").
func (h *HackHandler) loadLeaveSelf(w http.ResponseWriter, r *http.Request, slug string) (db.Event, db.EventMember, string, *db.User, bool) {
	user := hackNeedUser(w, r)
	if user == nil {
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	if err != nil {
		writeInternal(w)
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	member, status, err := db.LeaveSelf(r.Context(), h.database, ev.ID, user.ID, false)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	if err != nil {
		writeInternal(w)
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	return ev, member, status, user, true
}

const leaveStateChangedMsg = "something changed since you checked: read the leave preview again"

// previewLeaveEvent is GET .../leave: read-only, every case, never errors
// for a member beyond 404 (build plan 2.1).
func (h *HackHandler) previewLeaveEvent(w http.ResponseWriter, r *http.Request) {
	ev, member, approvalStatus, user, ok := h.loadLeaveSelf(w, r, r.PathValue("slug"))
	if !ok {
		return
	}
	ctx := r.Context()
	otherOrganisers, err := db.OtherEventOrganisers(ctx, h.database, ev.ID, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	isCreator := ev.CreatedBy.Valid && ev.CreatedBy.String == user.ID
	leaveCase := leaveCaseFor(member.Role, approvalStatus, isCreator, len(otherOrganisers))

	out := map[string]any{
		"slug":  ev.Slug,
		"title": ev.Title,
		"stage": ev.Stage,
		"role":  member.Role,
		// A rejected applicant is the same "applicant" leave case as a
		// pending one -- same mechanics, same copy-less branch on the server
		// -- but the two need different client copy (Codex review, SIM-340,
		// P3: "Withdraw your application?" reads wrong for someone whose
		// application was already decided). approval_status lets leave.js
		// tell them apart without a new server-side case.
		"approval_status": approvalStatus,
		"case":            leaveCase,
		"confirm":         ev.Slug,
	}

	var blocked map[string]any
	switch {
	case ev.TakenDown():
		blocked = map[string]any{"code": "event_taken_down", "message": "this event has been taken down"}
	case ev.Stage == "archived":
		blocked = map[string]any{"code": "event_closed", "message": "this event has ended, so nobody can leave it"}
	}

	if leaveCase == "participant" && member.TeamID.Valid {
		team, err := db.GetEventTeamByID(ctx, h.database, member.TeamID.String)
		if err != nil {
			writeInternal(w)
			return
		}
		people, err := db.ListTeamPeople(ctx, h.database, team.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		teammates := make([]string, 0, len(people))
		for _, p := range people {
			if p.UserID != user.ID {
				teammates = append(teammates, p.DisplayName)
			}
		}
		shown := teammates
		if len(shown) > 3 {
			shown = shown[:3]
		}
		// Owner decision 2026-10-31: a participant may leave any time before
		// the event ends, including past their team's effective deadline (or
		// once the event is closed, judging or results). "frozen" and
		// deleted_on_leave both come from TeamKeptIfEmptied -- NOT
		// TeamKeepableSQL, the exact predicate deleteTeamIfEmpty itself uses
		// -- so this preview can never promise "stays" for a case the write
		// path would actually delete (Codex review, SIM-340, P1: a team with
		// a future deadline_override extension stays deletable-if-empty even
		// once the event's own stage and deadline have moved past it).
		kept, err := db.TeamKeptIfEmptied(ctx, h.database, team.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		out["team"] = map[string]any{
			"slug": team.Slug, "name": team.Name,
			"others": shown, "others_count": len(teammates),
			"deleted_on_leave": len(teammates) == 0 && !kept,
			"frozen":           kept,
		}
	}

	switch leaveCase {
	case "judge", "co_organiser", "creator", "last_organiser":
		_, byJudge, err := db.ScoreCoverage(ctx, h.database, ev.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		out["scored_teams"] = byJudge[user.ID]
	}

	if leaveCase == "co_organiser" {
		names := make([]string, 0, len(otherOrganisers))
		for _, o := range otherOrganisers {
			names = append(names, o.DisplayName)
		}
		if len(names) > 3 {
			names = names[:3]
		}
		out["organisers_staying"] = names
	}

	if leaveCase == "creator" {
		successors := make([]map[string]any, 0, len(otherOrganisers))
		for _, o := range otherOrganisers {
			successors = append(successors, map[string]any{
				"user_id": o.UserID, "display_name": o.DisplayName, "email": o.Email,
			})
		}
		out["successors"] = successors
	}

	if leaveCase == "last_organiser" {
		participants, _, _, err := db.CountEventMembers(ctx, h.database, ev.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		end := map[string]any{"allowed": true, "blocked": nil}
		if participants == 0 {
			end = map[string]any{"allowed": false, "blocked": "archive_needs_participants"}
		}
		out["close_options"] = map[string]any{
			"end":    end,
			"delete": map[string]any{"allowed": true},
		}
	}

	rejoin := "none"
	switch leaveCase {
	case "participant", "applicant":
		if ev.Stage == "open" || ev.Stage == "building" {
			rejoin = "join"
		}
	case "judge":
		if ev.Stage != "results" && ev.Stage != "archived" {
			rejoin = "judge_link"
		}
	case "co_organiser", "creator":
		rejoin = "organiser_invite"
	}
	out["rejoin"] = rejoin
	out["allowed"] = blocked == nil
	if blocked == nil {
		out["blocked"] = nil
	} else {
		out["blocked"] = blocked
	}
	// The creator case carries other organisers' login emails (Grok security
	// review, SIM-340); X-API-Key auth means a cache is not required to skip
	// storing the GET, so say so explicitly.
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}

// postLeaveEvent is POST .../leave: every write the build plan's section
// 2.3 transaction describes, under the event/member/team lock order every
// hack writer uses.
func (h *HackHandler) postLeaveEvent(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.rateLimited(w, r, user.ID) {
		return
	}
	var req struct {
		Confirm         string `json:"confirm"`
		SuccessorUserID string `json:"successor_user_id"`
		Close           string `json:"close"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Close != "" && req.Close != "end" && req.Close != "delete" {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "close must be end or delete")
		return
	}
	if req.SuccessorUserID != "" && !uuidShape.MatchString(req.SuccessorUserID) {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "successor_user_id must be an id")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev0, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if _, err := db.GetEventMember(r.Context(), h.database, ev0.ID, user.ID); errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	} else if err != nil {
		writeInternal(w)
		return
	}

	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ctx := r.Context()

	ev, err := db.GetEventForUpdate(ctx, tx, ev0.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	member, approvalStatus, err := db.LeaveSelf(ctx, tx, ev.ID, user.ID, true)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeHackErr(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return
	}
	if ev.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended, so nobody can leave it")
		return
	}
	if req.Confirm != ev.Slug {
		writeHackErr(w, http.StatusConflict, "leave_needs_confirm", `to leave, send {"confirm":"<slug>"} with this event's address name`)
		return
	}

	otherOrganisers, err := db.OtherEventOrganisers(ctx, tx, ev.ID, user.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	isCreator := ev.CreatedBy.Valid && ev.CreatedBy.String == user.ID
	leaveCase := leaveCaseFor(member.Role, approvalStatus, isCreator, len(otherOrganisers))

	var successor db.OtherOrganiser
	switch leaveCase {
	case "participant", "applicant", "judge", "co_organiser":
		if req.SuccessorUserID != "" || req.Close != "" {
			writeHackErr(w, http.StatusConflict, "leave_state_changed", leaveStateChangedMsg)
			return
		}
	case "creator":
		if req.Close != "" {
			writeHackErr(w, http.StatusConflict, "leave_state_changed", leaveStateChangedMsg)
			return
		}
		if req.SuccessorUserID == "" {
			writeHackErr(w, http.StatusConflict, "successor_required", "you created this event, so hand it to another organiser first: send successor_user_id")
			return
		}
		found := false
		for _, o := range otherOrganisers {
			if o.UserID == req.SuccessorUserID {
				successor = o
				found = true
				break
			}
		}
		if !found {
			writeHackErr(w, http.StatusConflict, "invalid_successor", "pick another organiser of this event")
			return
		}
	case "last_organiser":
		if req.SuccessorUserID != "" {
			writeHackErr(w, http.StatusConflict, "leave_state_changed", leaveStateChangedMsg)
			return
		}
		if req.Close == "" {
			writeHackErr(w, http.StatusConflict, "last_organiser", `you are the only organiser, so leaving closes the event: send close "end" or "delete"`)
			return
		}
		if req.Close == "end" {
			participants, _, _, err := db.CountEventMembers(ctx, tx, ev.ID)
			if err != nil {
				writeInternal(w)
				return
			}
			if participants == 0 {
				writeHackErr(w, http.StatusConflict, "archive_needs_participants", "nobody has joined, so delete the event instead")
				return
			}
		}
	}

	// Owner decision 2026-10-31: leaving no longer refuses past the team's
	// deadline (or once the event is closed, judging or results). The team
	// row still gets locked, in the usual event/member/team order, inside
	// LeaveMembership below -- db.LeaveMembership -> deleteTeamIfEmpty keeps
	// a frozen team even once this leave empties it (TeamKeepableSQL is
	// false once frozen), so the team, its entry, its site and its scores
	// are never touched here.

	if member.Role == "judge" || member.Role == "organiser" {
		if err := db.RecordFormerJudge(ctx, tx, ev.ID, user.ID, member.Role, member.DisplayName); err != nil {
			writeInternal(w)
			return
		}
	}

	switch leaveCase {
	case "creator":
		if _, err := tx.ExecContext(ctx, `UPDATE events SET created_by = $2, updated_at = now() WHERE id = $1`, ev.ID, successor.UserID); err != nil {
			writeInternal(w)
			return
		}
	case "last_organiser":
		if req.Close == "end" {
			if _, err := archiveEventTx(ctx, tx, ev.ID, ev.Stage); err != nil {
				writeInternal(w)
				return
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM event_organiser_invites WHERE event_id = $1`, ev.ID); err != nil {
				writeInternal(w)
				return
			}
		} else {
			if err := h.deleteEventTx(ctx, tx, ev); err != nil {
				writeInternal(w)
				return
			}
		}
	}

	teamDeleted := false
	if !(leaveCase == "last_organiser" && req.Close == "delete") {
		// The event-and-account cascade already removed this membership (and
		// every other) when the last organiser deleted the event. selfLeave
		// is true here (this is POST .../leave, never Remove person): if this
		// empties a team deleteTeamIfEmpty keeps, kept_after_leave is set so
		// judging, results and dashboards keep it exactly as if the leaver
		// hadn't left (Codex review, SIM-340, P2).
		teamID, err := db.LeaveMembership(ctx, tx, ev.ID, user.ID, true)
		if err != nil {
			writeInternal(w)
			return
		}
		if err := db.RevokeStaleTeamKeys(ctx, tx, ev.ID); err != nil {
			writeInternal(w)
			return
		}
		// Leaving does not touch votes (build plan section 1): a vote cast
		// while eligible stands, the leaver's included, the same as the
		// organiser's Remove person already does. IneligibleVotes sweeps
		// every ineligible vote in the whole event, not just the leaver's --
		// calling it here deleted other people's votes too (ship-blocker
		// B1), so leave must never call it at all.
		if teamID != "" {
			var still bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, teamID).Scan(&still); err != nil {
				writeInternal(w)
				return
			}
			teamDeleted = !still
		}
	}

	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}

	eventResult := "kept"
	switch {
	case leaveCase == "last_organiser" && req.Close == "end":
		eventResult = "ended"
		h.pinDue(ctx, ev.ID)
		// setStage calls requestEventCert after archiving too, but that call
		// is a no-op for an archived event: requestEventCert only asks for a
		// certificate while the stage is open, building or closed. Ending
		// here does not need its own call for the same reason (Nit 2).
	case leaveCase == "last_organiser" && req.Close == "delete":
		eventResult = "deleted"
		h.removeEventFiles(ev)
	case leaveCase == "participant":
		h.removeOrphanTeamSites(ctx, ev)
	}

	out := map[string]any{"left": true, "case": leaveCase, "team_deleted": teamDeleted, "event": eventResult}
	if leaveCase == "creator" {
		out["successor"] = map[string]any{"display_name": successor.DisplayName}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}
