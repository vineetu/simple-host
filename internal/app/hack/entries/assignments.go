package entries

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"github.com/vsriram/simple-host/internal/rules/sites"
)

// Error describes a rejected judging workflow.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Message }

// BeginAssignmentsError identifies a failure to begin the assignment transaction.
type BeginAssignmentsError struct{ Err error }

func (e *BeginAssignmentsError) Error() string { return e.Err.Error() }

// Unwrap returns the transaction error.
func (e *BeginAssignmentsError) Unwrap() error { return e.Err }

// GenerateAssignments plans and replaces assignments in one transaction.
func (s *Service) GenerateAssignments(ctx context.Context, eventID string, includeOrganisers bool, pool map[string]bool) ([]model.JudgeAssignment, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, &BeginAssignmentsError{Err: err}
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if ev.Stage == "archived" {
		return nil, nil, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if ev.JudgingLockedAt.Valid {
		return nil, nil, &Error{Code: "scores_locked", Message: "Judging is locked; unlock it first."}
	}
	// 409, not 400: the route exists, the event is just not in that mode.
	// The code stays invalid_request, the same name as the other bad-input refusals.
	if ev.JudgeAssignmentMode != "automatic" {
		return nil, nil, &Error{Code: "invalid_request", Message: "Switch to automatic assignment first."}
	}
	judges, err := entryrepo.ListEventJudges(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if !includeOrganisers {
		only := []model.NamedJudge{}
		for _, j := range judges {
			if j.Role == "judge" {
				only = append(only, j)
			}
		}
		judges = only
	}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if len(judges) == 0 {
		return nil, nil, &Error{Code: "too_few_judges", Message: "Invite at least one judge first."}
	}
	// Only teams that will be judged get judges: a team with no entry would
	// take a judge's slot and never show in anyone's queue.
	judgedTeams := make([]model.NamedTeam, 0, len(teams))
	for _, t := range teams {
		if pool[t.ID] {
			judgedTeams = append(judgedTeams, t)
		}
	}
	if len(teams) == 0 {
		return nil, nil, &Error{Code: "too_few_teams", Message: "No teams to assign yet."}
	}
	if len(judgedTeams) == 0 {
		return nil, nil, &Error{Code: "too_few_teams", Message: "No team has an entry to judge yet."}
	}
	teams = judgedTeams
	pairs, err := entryrepo.ListConflictPairs(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	conflicted := map[string]bool{}
	for _, p := range pairs {
		conflicted[hackentries.JudgeTeamKey(p.JudgeID, p.TeamID)] = true
	}
	planned, under := hackentries.PlanAssignments(teams, judges, conflicted, ev.JudgesPerTeam)
	if err := entryrepo.ReplaceAssignments(ctx, tx, eventID, planned); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return planned, under, nil
}

// PutManualAssignments replaces the event's manual assignments.
func (s *Service) PutManualAssignments(ctx context.Context, eventID string, items []model.JudgeAssignment) ([]model.JudgeAssignment, error) {
	tx, ev, err := s.optionsTx(ctx, eventID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if ev.JudgeAssignmentMode != "manual" {
		return nil, &Error{Code: "invalid_assignment_mode", Message: "switch to manual assignment first"}
	}
	seen := map[string]bool{}
	for _, row := range items {
		if !sites.UUIDShape.MatchString(row.JudgeID) || !sites.UUIDShape.MatchString(row.TeamID) {
			return nil, &Error{Code: "invalid_assignment", Message: "use event judge and team ids"}
		}
		key := hackentries.JudgeTeamKey(row.JudgeID, row.TeamID)
		if seen[key] {
			return nil, &Error{Code: "duplicate_assignment", Message: "each judge and team pair appears once"}
		}
		seen[key] = true
		judge, err := entryrepo.EventJudgeInEvent(ctx, tx, ev.ID, row.JudgeID)
		if err != nil {
			return nil, err
		}
		if !judge {
			return nil, &Error{Code: "judge_not_found", Message: "judge not found in this event"}
		}
		if _, err := entryrepo.EventTeamInEvent(ctx, tx, ev.ID, row.TeamID); errors.Is(err, sql.ErrNoRows) {
			return nil, &Error{Code: "team_not_found", Message: "team not found in this event"}
		} else if err != nil {
			return nil, err
		}
	}
	conflicts, err := entryrepo.ListConflictPairs(ctx, tx, ev.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range conflicts {
		if seen[hackentries.JudgeTeamKey(p.JudgeID, p.TeamID)] {
			return nil, &Error{Code: "judge_conflict", Message: "a conflicted judge cannot be assigned to that team"}
		}
	}
	if err := entryrepo.ReplaceAssignments(ctx, tx, ev.ID, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := entryrepo.ListEventAssignments(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// PutTrackPanels replaces the event's track panels.
func (s *Service) PutTrackPanels(ctx context.Context, eventID string, items []entryrepo.TrackPanelMember) ([]entryrepo.TrackPanelMember, error) {
	tx, ev, err := s.optionsTx(ctx, eventID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen := map[string]bool{}
	for _, p := range items {
		if !sites.UUIDShape.MatchString(p.TrackID) || !sites.UUIDShape.MatchString(p.JudgeID) {
			return nil, &Error{Code: "invalid_panel", Message: "use event track and judge ids"}
		}
		key := p.TrackID + "\x00" + p.JudgeID
		if seen[key] {
			return nil, &Error{Code: "duplicate_panel_member", Message: "each judge appears once per track"}
		}
		seen[key] = true
		track, err := entryrepo.EventTrackInEvent(ctx, tx, ev.ID, p.TrackID)
		if err != nil {
			return nil, err
		}
		if !track {
			return nil, &Error{Code: "track_not_found", Message: "track not found in this event"}
		}
		judge, err := entryrepo.EventJudgeInEvent(ctx, tx, ev.ID, p.JudgeID)
		if err != nil {
			return nil, err
		}
		if !judge {
			return nil, &Error{Code: "judge_not_found", Message: "judge not found in this event"}
		}
	}
	if err := entryrepo.ReplaceTrackPanels(ctx, tx, ev.ID, items); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	rows, err := entryrepo.ListTrackPanels(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (s *Service) optionsTx(ctx context.Context, eventID string) (*sql.Tx, db.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, db.Event{}, err
	}
	ev, err := db.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		tx.Rollback()
		return nil, db.Event{}, err
	}
	if ev.Stage == "archived" {
		tx.Rollback()
		return nil, db.Event{}, &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	}
	if ev.JudgingLockedAt.Valid {
		tx.Rollback()
		return nil, db.Event{}, &Error{Code: "scores_locked", Message: "Judging is locked; unlock it first."}
	}
	return tx, ev, nil
}
