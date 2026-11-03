package hackhttp

import (
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
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
	ev, err := h.membersSvc().LeaveEvent(r.Context(), slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	var member db.EventMember
	var status string
	if err == nil {
		member, status, err = h.membersSvc().LookupLeaveSelf(r.Context(), ev.ID, user.ID)
	}
	if err == nil {
		paperHackKnownMember(r, ev, user.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return db.Event{}, db.EventMember{}, "", nil, false
	}
	return ev, member, status, user, true
}

// previewLeaveEvent is GET .../leave: read-only, every case, never errors
// for a member beyond 404 (build plan 2.1).
func (h *HackHandler) previewLeaveEvent(w http.ResponseWriter, r *http.Request) {
	ev, member, approvalStatus, user, ok := h.loadLeaveSelf(w, r, r.PathValue("slug"))
	if !ok {
		return
	}
	preview, err := h.membersSvc().PreviewLeave(r.Context(), ev, member, approvalStatus, user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	otherOrganisers := preview.Organisers
	leaveCase := preview.Case

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
		team, people := preview.Team, preview.People
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
		kept := preview.Kept
		out["team"] = map[string]any{
			"slug": team.Slug, "name": team.Name,
			"others": shown, "others_count": len(teammates),
			"deleted_on_leave": len(teammates) == 0 && !kept,
			"frozen":           kept,
		}
	}

	switch leaveCase {
	case "judge", "co_organiser", "creator", "last_organiser":
		out["scored_teams"] = preview.ScoredTeams
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
		participants := preview.Participants
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
	httpx.WriteJSON(w, http.StatusOK, out)
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
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "close must be end or delete")
		return
	}
	if req.SuccessorUserID != "" && !sites.UUIDShape.MatchString(req.SuccessorUserID) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "successor_user_id must be an id")
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev0, err := h.membersSvc().LeaveEvent(r.Context(), slug)
	if err == nil {
		paperHackEvent(r, ev0)
		err = h.membersSvc().LookupLeaveMember(r.Context(), ev0.ID, user.ID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}

	ctx := r.Context()
	result, err := h.membersSvc().Leave(ctx, ev0, user.ID, req.Confirm, req.SuccessorUserID, req.Close)
	if err != nil {
		writeMemberError(w, err)
		return
	}
	leaveCase, teamDeleted, successor := result.Case, result.TeamDeleted, result.Successor

	eventResult := "kept"
	switch {
	case leaveCase == "last_organiser" && req.Close == "end":
		eventResult = "ended"

	case leaveCase == "last_organiser" && req.Close == "delete":
		eventResult = "deleted"
	}

	out := map[string]any{"left": true, "case": leaveCase, "team_deleted": teamDeleted, "event": eventResult}
	if leaveCase == "creator" {
		out["successor"] = map[string]any{"display_name": successor.DisplayName}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	httpx.WriteJSON(w, http.StatusOK, out)
}
