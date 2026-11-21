package entries

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/rules/sites"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

type Error = hackmodel.Error

// Refusals shared by every judging setup write.
var (
	errEventClosed  = &Error{Code: "event_closed", Message: "an ended event cannot be changed"}
	errScoresLocked = &Error{Code: "scores_locked", Message: "Judging is locked because the event is at Results. Move the event back to Judging to change it."}
	errSetupLocked  = &Error{Code: "judging_setup_locked", Message: "Judging has started. Move the event back to Entries closed to change this."}
)

func (s *Service) GenerateAssignments(ctx context.Context, eventID string, includeOrganisers *bool, pool map[string]bool, atStageMove bool) ([]hackmodel.JudgeAssignment, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if err := generateRefusal(ev, atStageMove, includeOrganisers != nil); err != nil {
		return nil, nil, err
	}
	if err := updateOrganiserJudges(ctx, tx, eventID, nil, includeOrganisers); err != nil {
		return nil, nil, err
	}
	judges, err := entryrepo.ListEventJudges(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, tx, eventID)
	if err != nil {
		return nil, nil, err
	}
	if teams, err = poolTeams(len(judges), teams, pool); err != nil {
		return nil, nil, err
	}
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

// generateRefusal is why automatic assignments cannot be generated for ev now.
func generateRefusal(ev hackmodel.Event, atStageMove, organisersChanged bool) error {
	switch {
	case ev.Stage == "archived":
		return errEventClosed
	case ev.JudgingLockedAt.Valid:
		return errScoresLocked
	case ev.Stage == "judging" && (!atStageMove || organisersChanged), ev.Stage == "results" && organisersChanged:
		return errSetupLocked
	case ev.JudgeAssignmentMode != "automatic":
		return &Error{Code: "invalid_request", Message: "Switch to automatic assignment first."}
	}
	return nil
}

// poolTeams is the teams in the judging pool, refusing a plan with nobody to judge or be judged.
func poolTeams(judges int, teams []hackmodel.NamedTeam, pool map[string]bool) ([]hackmodel.NamedTeam, error) {
	if judges == 0 {
		return nil, &Error{Code: "too_few_judges", Message: "Invite at least one judge first."}
	}
	if len(teams) == 0 {
		return nil, &Error{Code: "too_few_teams", Message: "No teams to assign yet."}
	}
	out := make([]hackmodel.NamedTeam, 0, len(teams))
	for _, t := range teams {
		if pool[t.ID] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, &Error{Code: "too_few_teams", Message: "No team has an entry to judge yet."}
	}
	return out, nil
}

func (s *Service) PutManualAssignments(ctx context.Context, eventID string, items []hackmodel.JudgeAssignment) ([]hackmodel.JudgeAssignment, error) {
	tx, ev, err := s.optionsTx(ctx, eventID, true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if ev.JudgeAssignmentMode != "manual" {
		return nil, &Error{Code: "invalid_assignment_mode", Message: "switch to manual assignment first"}
	}
	seen := map[string]bool{}
	for _, row := range items {
		key := hackentries.JudgeTeamKey(row.JudgeID, row.TeamID)
		switch {
		case !sites.UUIDShape.MatchString(row.JudgeID) || !sites.UUIDShape.MatchString(row.TeamID):
			return nil, &Error{Code: "invalid_assignment", Message: "use event judge and team ids"}
		case seen[key]:
			return nil, &Error{Code: "duplicate_assignment", Message: "each judge and team pair appears once"}
		}
		seen[key] = true
		if err := assignmentInEvent(ctx, tx, ev.ID, row); err != nil {
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
	return entryrepo.ListEventAssignments(ctx, s.db, ev.ID)
}

func assignmentInEvent(ctx context.Context, tx *sql.Tx, eventID string, row hackmodel.JudgeAssignment) error {
	judge, err := entryrepo.EventJudgeInEvent(ctx, tx, eventID, row.JudgeID)
	if err != nil {
		return err
	}
	if !judge {
		return &Error{Code: "judge_not_found", Message: "judge not found in this event"}
	}
	_, err = entryrepo.EventTeamInEvent(ctx, tx, eventID, row.TeamID)
	if errors.Is(err, sql.ErrNoRows) {
		return &Error{Code: "team_not_found", Message: "team not found in this event"}
	}
	return err
}

func (s *Service) PutTrackPanels(ctx context.Context, eventID string, items []entryrepo.TrackPanelMember) ([]entryrepo.TrackPanelMember, error) {
	tx, ev, err := s.optionsTx(ctx, eventID, true)
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
	return entryrepo.ListTrackPanels(ctx, s.db, ev.ID)
}

// optionsTx locks the event for a judging write; setup writes are also refused once judging starts.
func (s *Service) optionsTx(ctx context.Context, eventID string, setup bool) (*sql.Tx, hackmodel.Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, hackmodel.Event{}, err
	}
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	switch {
	case err != nil:
	case ev.Stage == "archived":
		err = errEventClosed
	case ev.JudgingLockedAt.Valid:
		err = errScoresLocked
	case setup && ev.Stage == "judging":
		err = errSetupLocked
	}
	if err != nil {
		tx.Rollback()
		return nil, hackmodel.Event{}, err
	}
	return tx, ev, nil
}
