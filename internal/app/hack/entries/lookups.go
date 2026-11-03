package entries

import (
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"

	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// FormerJudgeAssignedCountLookup forwards the FormerJudgeAssignedCount lookup.
func (s *Service) FormerJudgeAssignedCountLookup(ctx context.Context, eventID string, judgeID string) (int, error) {
	return eventrepo.FormerJudgeAssignedCount(ctx, s.db, eventID, judgeID)
}

// EventResults forwards the GetEventResults lookup.
func (s *Service) EventResults(ctx context.Context, eventID string) (entryrepo.EventResultsRow, error) {
	return entryrepo.GetEventResults(ctx, s.db, eventID)
}

// VotingSettings forwards the GetVotingSettings lookup.
func (s *Service) VotingSettings(ctx context.Context, eventID string) (model.VotingSettings, error) {
	return entryrepo.GetVotingSettings(ctx, s.db, eventID)
}

// EventAssignments forwards the ListEventAssignments lookup.
func (s *Service) EventAssignments(ctx context.Context, eventID string) ([]model.JudgeAssignment, error) {
	return entryrepo.ListEventAssignments(ctx, s.db, eventID)
}

// EventEntries forwards the ListEventEntries lookup.
func (s *Service) EventEntries(ctx context.Context, eventID string) ([]model.EventEntry, error) {
	return entryrepo.ListEventEntries(ctx, s.db, eventID)
}

// EventJudges forwards the ListEventJudges lookup.
func (s *Service) EventJudges(ctx context.Context, eventID string) ([]model.NamedJudge, error) {
	return entryrepo.ListEventJudges(ctx, s.db, eventID)
}

// FormerJudges forwards the ListFormerJudges lookup.
func (s *Service) FormerJudges(ctx context.Context, eventID string) ([]model.FormerJudge, error) {
	return eventrepo.ListFormerJudges(ctx, s.db, eventID)
}

// RawScores forwards the ListRawScores lookup.
func (s *Service) RawScores(ctx context.Context, eventID string) ([]model.RawScoreRow, error) {
	return entryrepo.ListRawScores(ctx, s.db, eventID)
}

// TeamsWithMembers forwards the ListTeamsWithMembers lookup.
func (s *Service) TeamsWithMembers(ctx context.Context, eventID string) ([]model.NamedTeam, error) {
	return entryrepo.ListTeamsWithMembers(ctx, s.db, eventID)
}

// TrackPanels forwards the ListTrackPanels lookup.
func (s *Service) TrackPanels(ctx context.Context, eventID string) ([]entryrepo.TrackPanelMember, error) {
	return entryrepo.ListTrackPanels(ctx, s.db, eventID)
}

// VoteFinalRankingLookup forwards the VoteFinalRanking lookup.
func (s *Service) VoteFinalRankingLookup(ctx context.Context, eventID string, accountID string) ([]model.VoteOption, error) {
	return entryrepo.VoteFinalRanking(ctx, s.db, eventID, accountID)
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

// EventResultsRow is the stored event results snapshot.
type EventResultsRow = entryrepo.EventResultsRow

// Querier is the query interface used by results computation and publication.
type Querier = sqlkit.Querier

// EventConflict is a conflict between a judge and a team.
type EventConflict = entryrepo.EventConflict

// JudgeScore is a judge's score for a criterion.
type JudgeScore = entryrepo.JudgeScore

// TrackPanelMember identifies a judge assigned to a track.
type TrackPanelMember = entryrepo.TrackPanelMember

// TeamsWithMembersInTx reads teams using the publication transaction.
func (s *Service) TeamsWithMembersInTx(ctx context.Context, q sqlkit.Querier, eventID string) ([]model.NamedTeam, error) {
	return entryrepo.ListTeamsWithMembers(ctx, q, eventID)
}

// EventEntriesInTx reads entries using the publication transaction.
func (s *Service) EventEntriesInTx(ctx context.Context, q sqlkit.Querier, eventID string) ([]model.EventEntry, error) {
	return entryrepo.ListEventEntries(ctx, q, eventID)
}
