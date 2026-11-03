package members

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// LeaveResult describes the committed membership change.
type LeaveResult struct {
	Event       db.Event
	Case        string
	TeamDeleted bool
	Successor   db.OtherOrganiser
}

// LeaveStateChangedMessage is returned when a leave preview is stale.
const LeaveStateChangedMessage = "something changed since you checked: read the leave preview again"

// Leave removes a membership while serializing against event changes.
func (s *Service) Leave(ctx context.Context, ev0 db.Event, userID, confirm, successorID, closeAction string) (result LeaveResult, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	ev, err := db.GetEventForUpdate(ctx, tx, ev0.ID)
	if err != nil {
		return result, err
	}
	member, approvalStatus, err := db.LeaveSelf(ctx, tx, ev.ID, userID, true)
	if errors.Is(err, sql.ErrNoRows) {
		return result, &Error{"event_not_found", "event not found"}
	}
	if err != nil {
		return result, err
	}
	if ev.TakenDown() {
		return result, &Error{"event_taken_down", "this event has been taken down"}
	}
	if ev.Stage == "archived" {
		return result, &Error{"event_closed", "this event has ended, so nobody can leave it"}
	}
	if confirm != ev.Slug {
		return result, &Error{"leave_needs_confirm", `to leave, send {"confirm":"<slug>"} with this event's address name`}
	}

	otherOrganisers, err := db.OtherEventOrganisers(ctx, tx, ev.ID, userID)
	if err != nil {
		return result, err
	}
	isCreator := ev.CreatedBy.Valid && ev.CreatedBy.String == userID
	leaveCase := hackevents.LeaveCaseFor(member.Role, approvalStatus, isCreator, len(otherOrganisers))

	var successor db.OtherOrganiser
	switch leaveCase {
	case "participant", "applicant", "judge", "co_organiser":
		if successorID != "" || closeAction != "" {
			return result, &Error{"leave_state_changed", LeaveStateChangedMessage}
		}
	case "creator":
		if closeAction != "" {
			return result, &Error{"leave_state_changed", LeaveStateChangedMessage}
		}
		if successorID == "" {
			return result, &Error{"successor_required", "you created this event, so hand it to another organiser first: send successor_user_id"}
		}
		found := false
		for _, o := range otherOrganisers {
			if o.UserID == successorID {
				successor = o
				found = true
				break
			}
		}
		if !found {
			return result, &Error{"invalid_successor", "pick another organiser of this event"}
		}
	case "last_organiser":
		if successorID != "" {
			return result, &Error{"leave_state_changed", LeaveStateChangedMessage}
		}
		if closeAction == "" {
			return result, &Error{"last_organiser", `you are the only organiser, so leaving closes the event: send close "end" or "delete"`}
		}
		if closeAction == "end" {
			participants, _, _, err := db.CountEventMembers(ctx, tx, ev.ID)
			if err != nil {
				return result, err
			}
			if participants == 0 {
				return result, &Error{"archive_needs_participants", "nobody has joined, so delete the event instead"}
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
		if err := db.RecordFormerJudge(ctx, tx, ev.ID, userID, member.Role, member.DisplayName); err != nil {
			return result, err
		}
	}

	switch leaveCase {
	case "creator":
		if _, err := handover(ctx, tx, ev.ID, successor.UserID); err != nil {
			return result, err
		}
	case "last_organiser":
		if closeAction == "end" {
			updated, err := s.archiveEventTx(ctx, tx, ev.ID, ev.Stage)
			if err != nil {
				return result, err
			}
			ev.UpdatedAt = updated.UpdatedAt
			if _, err := deleteInvites(ctx, tx, ev.ID); err != nil {
				return result, err
			}
		} else {
			if err := s.deleteEventTx(ctx, tx, ev); err != nil {
				return result, err
			}
		}
	}

	teamDeleted := false
	if !(leaveCase == "last_organiser" && closeAction == "delete") {
		// The event-and-account cascade already removed this membership (and
		// every other) when the last organiser deleted the event. selfLeave
		// is true here (this is POST .../leave, never Remove person): if this
		// empties a team deleteTeamIfEmpty keeps, kept_after_leave is set so
		// judging, results and dashboards keep it exactly as if the leaver
		// hadn't left (Codex review, SIM-340, P2).
		teamID, err := db.LeaveMembership(ctx, tx, ev.ID, userID, true)
		if err != nil {
			return result, err
		}
		if err := db.RevokeStaleTeamKeys(ctx, tx, ev.ID); err != nil {
			return result, err
		}
		// Leaving does not touch votes (build plan section 1): a vote cast
		// while eligible stands, the leaver's included, the same as the
		// organiser's Remove person already does. IneligibleVotes sweeps
		// every ineligible vote in the whole event, not just the leaver's --
		// calling it here deleted other people's votes too (ship-blocker
		// B1), so leave must never call it at all.
		if teamID != "" {
			var still bool
			if err := teamExists(ctx, tx, teamID, &still); err != nil {
				return result, err
			}
			teamDeleted = !still
		}
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}

	switch {
	case leaveCase == "last_organiser" && closeAction == "end":
		ev.Stage = "archived"
		s.afterEnd(ctx, ev)
	case leaveCase == "last_organiser" && closeAction == "delete":
		s.removeEventFiles(ev)
	case leaveCase == "participant":
		s.removeOrphanTeamSites(ctx, ev)
	}
	result.Event, result.Case, result.TeamDeleted, result.Successor = ev, leaveCase, teamDeleted, successor
	return result, nil
}

// LeaveEvent finds the event before looking up membership.
func (s *Service) LeaveEvent(ctx context.Context, slug string) (db.Event, error) {
	return db.GetEventBySlug(ctx, s.db, slug)
}

// LookupLeaveSelf finds the caller's membership for a preview.
func (s *Service) LookupLeaveSelf(ctx context.Context, eventID, userID string) (db.EventMember, string, error) {
	return db.LeaveSelf(ctx, s.db, eventID, userID, false)
}

// LookupLeaveMember finds the caller before a leave transaction.
func (s *Service) LookupLeaveMember(ctx context.Context, eventID, userID string) error {
	_, err := db.GetEventMember(ctx, s.db, eventID, userID)
	return err
}

// LeavePreview holds the facts needed to describe a leave.
type LeavePreview struct {
	Organisers   []db.OtherOrganiser
	Case         string
	Team         db.EventTeam
	People       []db.EventPerson
	Kept         bool
	ScoredTeams  int
	Participants int
}

// PreviewLeave loads the facts for a leave preview in their original order.
func (s *Service) PreviewLeave(ctx context.Context, ev db.Event, member db.EventMember, status, userID string) (p LeavePreview, err error) {
	p.Organisers, err = db.OtherEventOrganisers(ctx, s.db, ev.ID, userID)
	if err != nil {
		return
	}
	p.Case = hackevents.LeaveCaseFor(member.Role, status, ev.CreatedBy.Valid && ev.CreatedBy.String == userID, len(p.Organisers))
	if p.Case == "participant" && member.TeamID.Valid {
		p.Team, err = db.GetEventTeamByID(ctx, s.db, member.TeamID.String)
		if err != nil {
			return
		}
		p.People, err = db.ListTeamPeople(ctx, s.db, p.Team.ID)
		if err != nil {
			return
		}
		p.Kept, err = db.TeamKeptIfEmptied(ctx, s.db, p.Team.ID)
		if err != nil {
			return
		}
	}
	switch p.Case {
	case "judge", "co_organiser", "creator", "last_organiser":
		var scores map[string]int
		_, scores, err = db.ScoreCoverage(ctx, s.db, ev.ID)
		if err != nil {
			return
		}
		p.ScoredTeams = scores[userID]
	}
	if p.Case == "last_organiser" {
		p.Participants, _, _, err = db.CountEventMembers(ctx, s.db, ev.ID)
	}
	return
}
