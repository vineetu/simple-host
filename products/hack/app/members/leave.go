package members

import (
	"context"
	"database/sql"
	"errors"

	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// Leave this event (SIM-340).

// LeaveStateChangedMessage is returned when a leave preview is stale.
const LeaveStateChangedMessage = "something changed since you checked: read the leave preview again"

// LeaveRequest is what the caller sent to POST .../leave, shape-checked.
type LeaveRequest struct{ Confirm, SuccessorID, Close string }

// LeaveResult is the committed leave, as the route answers it.
type LeaveResult struct {
	Event       model.Event       `json:"-"`
	Left        bool              `json:"left"`
	Case        string            `json:"case"`
	TeamDeleted bool              `json:"team_deleted"`
	Outcome     string            `json:"event"`
	Successor   map[string]string `json:"successor,omitempty"`
}

// leaveOutcomes names what happened to the event for each close action.
var leaveOutcomes = map[string]string{"": "kept", "end": "ended", "delete": "deleted"}

// leavePlan is a checked leave: the leaver's role, their case and what the request asked for on top.
type leavePlan struct {
	role, leaveCase, close string
	successor              model.OtherOrganiser
}

// Leave removes the caller's own membership of the event at slug, in the event/member/team lock order every hack writer uses.
func (s *Service) Leave(ctx context.Context, slug, userID string, req LeaveRequest) (res LeaveResult, err error) {
	if res.Event, err = eventrepo.GetEventBySlug(ctx, s.db, slug); err != nil {
		return res, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, res.Event.ID)
	if err != nil {
		return res, err
	}
	plan, err := planLeave(ctx, tx, ev, userID, req)
	if err != nil {
		return res, err
	}
	if res.TeamDeleted, err = s.executeLeave(ctx, tx, &ev, userID, plan); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	s.afterLeave(ctx, ev, plan)
	res.Left, res.Case, res.Outcome = true, plan.leaveCase, leaveOutcomes[plan.close]
	if plan.leaveCase == "creator" {
		res.Successor = map[string]string{"display_name": plan.successor.DisplayName}
	}
	return res, nil
}

// planLeave locks the caller's membership and checks the request against the leave case the server finds now.
func planLeave(ctx context.Context, tx *sql.Tx, ev model.Event, userID string, req LeaveRequest) (leavePlan, error) {
	member, status, err := eventrepo.LeaveSelf(ctx, tx, ev.ID, userID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return leavePlan{}, errEventNotFound
	}
	if err != nil {
		return leavePlan{}, err
	}
	if refusal := leaveRefusal(ev); refusal != nil {
		return leavePlan{}, refusal
	}
	if req.Confirm != ev.Slug {
		return leavePlan{}, &Error{Code: "leave_needs_confirm", Message: `to leave, send {"confirm":"<slug>"} with this event's address name`}
	}
	others, err := eventrepo.OtherEventOrganisers(ctx, tx, ev.ID, userID)
	if err != nil {
		return leavePlan{}, err
	}
	plan := leavePlan{role: member.Role, close: req.Close, leaveCase: leaveCase(ev, member, status, userID, others)}
	if plan.successor, err = checkLeaveRequest(plan.leaveCase, req, others); err != nil {
		return leavePlan{}, err
	}
	if req.Close == "end" {
		return plan, checkCanEnd(ctx, tx, ev.ID)
	}
	return plan, nil
}

// leaveCase names the caller's leave case.
func leaveCase(ev model.Event, member model.EventMember, status, userID string, others []model.OtherOrganiser) string {
	isCreator := ev.CreatedBy.Valid && ev.CreatedBy.String == userID
	return hackevents.LeaveCaseFor(member.Role, status, isCreator, len(others))
}

// leaveRefusal is why nobody may leave this event now, or nil.
func leaveRefusal(ev model.Event) *Error {
	switch {
	case ev.TakenDown():
		return errTakenDown
	case ev.Stage == "archived":
		return &Error{Code: "event_closed", Message: "this event has ended, so nobody can leave it"}
	}
	return nil
}

// checkLeaveRequest matches the request to the leave case.
func checkLeaveRequest(leaveCase string, req LeaveRequest, others []model.OtherOrganiser) (model.OtherOrganiser, error) {
	wantsSuccessor, wantsClose := leaveCase == "creator", leaveCase == "last_organiser"
	switch {
	case (req.SuccessorID != "" && !wantsSuccessor) || (req.Close != "" && !wantsClose):
		return model.OtherOrganiser{}, &Error{Code: "leave_state_changed", Message: LeaveStateChangedMessage}
	case wantsSuccessor && req.SuccessorID == "":
		return model.OtherOrganiser{}, &Error{Code: "successor_required", Message: "you created this event, so hand it to another organiser first: send successor_user_id"}
	case wantsClose && req.Close == "":
		return model.OtherOrganiser{}, &Error{Code: "last_organiser", Message: `you are the only organiser, so leaving closes the event: send close "end" or "delete"`}
	case !wantsSuccessor:
		return model.OtherOrganiser{}, nil
	}
	for _, o := range others {
		if o.UserID == req.SuccessorID {
			return o, nil
		}
	}
	return model.OtherOrganiser{}, &Error{Code: "invalid_successor", Message: "pick another organiser of this event"}
}

// checkCanEnd refuses ending an event nobody has joined.
func checkCanEnd(ctx context.Context, tx *sql.Tx, eventID string) error {
	participants, _, _, err := eventrepo.CountEventMembers(ctx, tx, eventID)
	if err == nil && participants == 0 {
		return &Error{Code: "archive_needs_participants", Message: "nobody has joined, so delete the event instead"}
	}
	return err
}

// executeLeave does every write of a checked leave and reports whether the leaver's team went with them.
func (s *Service) executeLeave(ctx context.Context, tx *sql.Tx, ev *model.Event, userID string, plan leavePlan) (bool, error) {
	if plan.role == "judge" || plan.role == "organiser" {
		if err := eventrepo.RecordFormerJudge(ctx, tx, ev.ID, userID, plan.role); err != nil {
			return false, err
		}
	}
	switch {
	case plan.leaveCase == "creator":
		if _, err := eventrepo.Handover(ctx, tx, ev.ID, plan.successor.UserID); err != nil {
			return false, err
		}
	case plan.close == "end":
		if err := s.endEventTx(ctx, tx, ev); err != nil {
			return false, err
		}
	case plan.close == "delete":
		// The event-and-account cascade removes this membership and every other.
		return false, s.deleteEventTx(ctx, tx, *ev)
	}
	return leaveMembership(ctx, tx, ev.ID, userID)
}

// endEventTx archives the event its last organiser is leaving and drops its open invites.
func (s *Service) endEventTx(ctx context.Context, tx *sql.Tx, ev *model.Event) error {
	updated, err := s.archiveEventTx(ctx, tx, ev.ID, ev.Stage)
	if err != nil {
		return err
	}
	ev.UpdatedAt = updated.UpdatedAt
	_, err = eventrepo.DeleteInvites(ctx, tx, ev.ID)
	return err
}

// leaveMembership deletes the caller's own membership and reports whether their team was deleted with it.
func leaveMembership(ctx context.Context, tx *sql.Tx, eventID, userID string) (bool, error) {
	ops := eventrepo.LeaveOperations{LockMember: teamrepo.LockMember, LockTeam: teamrepo.LockTeam, DeleteTeamIfEmpty: teamrepo.DeleteTeamIfEmpty, EnsureTeamAdmin: teamrepo.EnsureTeamAdmin}
	teamID, err := eventrepo.LeaveMembership(ctx, tx, eventID, userID, true, ops)
	if err != nil {
		return false, err
	}
	if err := eventapp.SyncAccess(ctx, tx, eventID); err != nil || teamID == "" {
		return false, err
	}
	var still bool
	err = eventrepo.TeamExists(ctx, tx, teamID, &still)
	return err == nil && !still, err
}

// afterLeave runs the out-of-transaction follow-ups of a committed leave.
func (s *Service) afterLeave(ctx context.Context, ev model.Event, plan leavePlan) {
	switch {
	case plan.close == "end":
		ev.Stage = "archived"
		s.afterEnd(ctx, ev)
	case plan.close == "delete":
		s.removeEventFiles(ev)
	case plan.leaveCase == "participant":
		s.removeOrphanTeamSites(ctx, ev)
	}
}
