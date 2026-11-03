package entries

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// FormerJudgeAssignedCountLookup forwards the FormerJudgeAssignedCount lookup.
func (s *Service) FormerJudgeAssignedCountLookup(ctx context.Context, eventID string, judgeID string) (int, error) {
	return db.FormerJudgeAssignedCount(ctx, s.db, eventID, judgeID)
}

// EventResults forwards the GetEventResults lookup.
func (s *Service) EventResults(ctx context.Context, eventID string) (db.EventResultsRow, error) {
	return db.GetEventResults(ctx, s.db, eventID)
}

// VotingSettings forwards the GetVotingSettings lookup.
func (s *Service) VotingSettings(ctx context.Context, eventID string) (db.VotingSettings, error) {
	return db.GetVotingSettings(ctx, s.db, eventID)
}

// EventAssignments forwards the ListEventAssignments lookup.
func (s *Service) EventAssignments(ctx context.Context, eventID string) ([]db.JudgeAssignment, error) {
	return db.ListEventAssignments(ctx, s.db, eventID)
}

// EventEntries forwards the ListEventEntries lookup.
func (s *Service) EventEntries(ctx context.Context, eventID string) ([]db.EventEntry, error) {
	return db.ListEventEntries(ctx, s.db, eventID)
}

// EventJudges forwards the ListEventJudges lookup.
func (s *Service) EventJudges(ctx context.Context, eventID string) ([]model.NamedJudge, error) {
	return db.ListEventJudges(ctx, s.db, eventID)
}

// FormerJudges forwards the ListFormerJudges lookup.
func (s *Service) FormerJudges(ctx context.Context, eventID string) ([]model.FormerJudge, error) {
	return db.ListFormerJudges(ctx, s.db, eventID)
}

// RawScores forwards the ListRawScores lookup.
func (s *Service) RawScores(ctx context.Context, eventID string) ([]model.RawScoreRow, error) {
	return db.ListRawScores(ctx, s.db, eventID)
}

// TeamsWithMembers forwards the ListTeamsWithMembers lookup.
func (s *Service) TeamsWithMembers(ctx context.Context, eventID string) ([]db.NamedTeam, error) {
	return db.ListTeamsWithMembers(ctx, s.db, eventID)
}

// TrackPanels forwards the ListTrackPanels lookup.
func (s *Service) TrackPanels(ctx context.Context, eventID string) ([]db.TrackPanelMember, error) {
	return db.ListTrackPanels(ctx, s.db, eventID)
}

// VoteFinalRankingLookup forwards the VoteFinalRanking lookup.
func (s *Service) VoteFinalRankingLookup(ctx context.Context, eventID string, accountID string) ([]db.VoteOption, error) {
	return db.VoteFinalRanking(ctx, s.db, eventID, accountID)
}

// Check is the result of evaluating an entry.
type Check = hackentries.Check

// RankOverride describes an organiser's rank change.
type RankOverride = hackentries.RankOverride

// Entry rule exports preserve the existing evaluation and validation decisions.
var (
	Evaluate       = hackentries.Evaluate
	LoadRules      = hackentries.LoadRules
	MissingMessage = hackentries.MissingMessage
	PresetKnown    = hackentries.PresetKnown
	RulesEditable  = hackentries.RulesEditable
	RulesSummary   = hackentries.RulesSummary
	ValidateRubric = hackentries.ValidateRubric
	WholeInRange   = hackentries.WholeInRange
)
