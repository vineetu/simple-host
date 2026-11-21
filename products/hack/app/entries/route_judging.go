package entries

import (
	"context"
	"slices"
	"strings"

	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/products/hack/model"
)

// JudgingSettingsInput is a judging settings change as it arrives, before
// its shape is checked.
type JudgingSettingsInput struct {
	OrganiserJudges           *[]string
	IncludeOrganisers         *bool
	Mode                      *string
	PerTeam                   *float64
	ScoreMode, TieCriterionID *string
	PublicScores, PublicRanks *bool
}

// AssignmentModes are the ways judges can be spread over teams, in the
// order the settings list them.
var AssignmentModes = []string{"open", "automatic", "manual", "panel"}

func (s *Service) ChangeJudgingSettings(ctx context.Context, eventID string, in JudgingSettingsInput) (model.Event, error) {
	req := JudgingSettings{OrganiserJudges: in.OrganiserJudges, IncludeOrganisers: in.IncludeOrganisers, Mode: in.Mode,
		ScoreMode: in.ScoreMode, TieCriterionID: in.TieCriterionID, PublicScores: in.PublicScores, PublicRanks: in.PublicRanks}
	if in.Mode != nil && !slices.Contains(AssignmentModes, *in.Mode) {
		return model.Event{}, &Error{Code: "invalid_request", Message: "assignment_mode must be open, automatic, manual or panel."}
	}
	if in.PerTeam != nil {
		n, ok := WholeInRange(in.PerTeam, 1, 20)
		if !ok {
			return model.Event{}, &Error{Code: "invalid_request", Message: "judges_per_team must be a whole number from 1 to 20."}
		}
		req.PerTeam = &n
	}
	if in.ScoreMode != nil && *in.ScoreMode != "raw" && *in.ScoreMode != "normalised" {
		return model.Event{}, &Error{Code: "invalid_score_mode", Message: "score_mode must be raw or normalised"}
	}
	if in.TieCriterionID != nil && *in.TieCriterionID != "" && !sites.UUIDShape.MatchString(*in.TieCriterionID) {
		return model.Event{}, &Error{Code: "invalid_tie_criterion", Message: "use a criterion in this event"}
	}
	return s.UpdateJudgingSettings(ctx, eventID, req)
}

// JudgingLocked refuses a change to any scoring input once judging is locked.
func JudgingLocked(ev model.Event) error {
	if ev.JudgingLockedAt.Valid {
		return errScoresLocked
	}
	return nil
}

// ConflictCaller is who records or removes a conflict: the caller's id and
// whether they organise the event.
type ConflictCaller struct {
	UserID    string
	Organiser bool
}

// RecordConflict records that a judge cannot score a team. A judge
// declares their own; an organiser may name another judge (requested).
func (s *Service) RecordConflict(ctx context.Context, eventID string, by ConflictCaller, teamID, requested string) (EventConflict, error) {
	judgeID, declaredBy := by.UserID, "judge"
	switch {
	case !by.Organiser && requested != "" && !strings.EqualFold(requested, by.UserID):
		return EventConflict{}, &Error{Code: "forbidden", Message: "Only an organiser can record a conflict for another judge."}
	case by.Organiser && strings.TrimSpace(requested) != "":
		judgeID, declaredBy = requested, "organiser"
	}
	if !sites.UUIDShape.MatchString(teamID) {
		return EventConflict{}, &Error{Code: "team_not_found", Message: "team not found"}
	}
	if !sites.UUIDShape.MatchString(judgeID) {
		return EventConflict{}, &Error{Code: "judge_not_found", Message: "judge not found"}
	}
	return s.DeclareConflict(ctx, eventID, judgeID, teamID, declaredBy)
}

func (s *Service) RemoveConflict(ctx context.Context, ev model.Event, by ConflictCaller, teamID, requested string) error {
	if err := JudgingLocked(ev); err != nil {
		return err
	}
	judgeID := by.UserID
	switch {
	case !by.Organiser && requested != "" && !strings.EqualFold(requested, by.UserID):
		return &Error{Code: "invalid_request", Message: "You can only remove your own conflict."}
	case by.Organiser && requested != "":
		judgeID = requested
	}
	if !sites.UUIDShape.MatchString(teamID) || !sites.UUIDShape.MatchString(judgeID) {
		return &Error{Code: "conflict_not_found", Message: "conflict not found"}
	}
	return s.DeleteConflict(ctx, ev.ID, judgeID, teamID)
}

// ChangeVotingSettings saves a whole voting window; votes made ineligible are removed only on confirm.
func (s *Service) ChangeVotingSettings(ctx context.Context, eventID string, v VotingConfig, confirm bool) (model.Event, VotingConfig, int64, error) {
	if err := ValidateVotingWindow(v); err != nil {
		return model.Event{}, v, 0, err
	}
	return s.SaveVotingSettings(ctx, eventID, v, confirm)
}
