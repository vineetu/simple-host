package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
)

// Leave this event (SIM-340). One route, self only: the server decides
// which of seven cases applies (build plan section 1) under a lock on the
// event row, every time, so a stale dialog can never act on a role or
// membership that changed since it was opened.

func (h *HackHandler) registerLeave(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/leave", wrap(h.previewLeaveEvent))
	mux.Handle("POST /v1/hack/events/{slug}/leave", wrap(h.postLeaveEvent))
}

// leaveCaseFor names which of the seven outcomes applies to one membership,
// from role, approval status and how many other organisers the event has.
// Rejected is its own case because it is never allowed, whatever else is
// true. An organiser with nobody else left is last_organiser whether or not
// they are the creator; a creator with others is creator; any other
// organiser with others is co_organiser.
func leaveCaseFor(role, approvalStatus string, isCreator bool, otherOrganisers int) string {
	switch role {
	case "participant":
		switch approvalStatus {
		case "pending":
			return "applicant"
		case "rejected":
			return "rejected"
		default:
			return "participant"
		}
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
		"slug":    ev.Slug,
		"title":   ev.Title,
		"stage":   ev.Stage,
		"role":    member.Role,
		"case":    leaveCase,
		"confirm": ev.Slug,
	}

	var blocked map[string]any
	switch {
	case ev.TakenDown():
		blocked = map[string]any{"code": "event_taken_down", "message": "this event has been taken down"}
	case ev.Stage == "archived":
		blocked = map[string]any{"code": "event_closed", "message": "this event has ended, so nobody can leave it"}
	case leaveCase == "rejected":
		blocked = map[string]any{"code": "application_rejected", "message": "your application was not accepted, and that decision stays on record"}
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
		out["team"] = map[string]any{
			"slug": team.Slug, "name": team.Name,
			"others": shown, "others_count": len(teammates),
			"deleted_on_leave": len(teammates) == 0,
		}
		if blocked == nil {
			frozen, err := db.TeamFrozenForLeave(ctx, h.database, team.ID, false)
			if err != nil {
				writeInternal(w)
				return
			}
			if frozen {
				blocked = map[string]any{"code": "submissions_closed", "message": "your team's deadline has passed, so your team can't change and you can't leave. Ask an organiser"}
			}
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
	if approvalStatus == "rejected" {
		writeHackErr(w, http.StatusConflict, "application_rejected", "your application was not accepted, and that decision stays on record")
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

	if leaveCase == "participant" && member.TeamID.Valid {
		frozen, err := db.TeamFrozenForLeave(ctx, tx, member.TeamID.String, true)
		if err != nil {
			writeInternal(w)
			return
		}
		if frozen {
			writeHackErr(w, http.StatusConflict, "submissions_closed", "your team's deadline has passed, so your team can't change and you can't leave. Ask an organiser")
			return
		}
	}

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
			if _, err := archiveEventTx(ctx, tx, ev.ID); err != nil {
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
		// every other) when the last organiser deleted the event.
		teamID, err := db.LeaveMembership(ctx, tx, ev.ID, user.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		if err := db.RevokeStaleTeamKeys(ctx, tx, ev.ID); err != nil {
			writeInternal(w)
			return
		}
		// A leaver's vote stops being eligible the moment their membership
		// is gone, same as anyone else voting settings would already have
		// excluded (Grok security review, SIM-340). Reuse IneligibleVotes'
		// own exclusion logic -- it already skips "all_signed_in" (a ballot
		// there is not tied to membership, so it stands) and already
		// matches "participants" eligibility by role -- rather than
		// duplicate it: after the member row above is gone, re-sweeping
		// removes exactly this leaver's now-ineligible vote and nobody
		// else's.
		voting, err := db.GetVotingSettings(ctx, tx, ev.ID)
		if err != nil {
			writeInternal(w)
			return
		}
		if _, err := db.IneligibleVotes(ctx, tx, ev.ID, voting.Eligibility, true); err != nil {
			writeInternal(w)
			return
		}
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
