package entries

import (
	"context"
	"github.com/vsriram/simple-host/host/sites"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

func (s *Service) FormerJudgeAssignedCountLookup(ctx context.Context, eventID string, judgeID string) (int, error) {
	return eventrepo.FormerJudgeAssignedCount(ctx, s.db, eventID, judgeID)
}

func (s *Service) EventResults(ctx context.Context, eventID string) (entryrepo.EventResultsRow, error) {
	return entryrepo.GetEventResults(ctx, s.db, eventID)
}

func (s *Service) VotingSettings(ctx context.Context, eventID string) (model.VotingSettings, error) {
	return entryrepo.GetVotingSettings(ctx, s.db, eventID)
}

func (s *Service) EventAssignments(ctx context.Context, eventID string) ([]model.JudgeAssignment, error) {
	return entryrepo.ListEventAssignments(ctx, s.db, eventID)
}

func (s *Service) EventEntries(ctx context.Context, eventID string) ([]model.EventEntry, error) {
	return entryrepo.ListEventEntries(ctx, s.db, eventID)
}

func (s *Service) EventJudges(ctx context.Context, eventID string) ([]model.NamedJudge, error) {
	return entryrepo.ListEventJudges(ctx, s.db, eventID)
}

func (s *Service) FormerJudges(ctx context.Context, eventID string) ([]model.FormerJudge, error) {
	return eventrepo.ListFormerJudges(ctx, s.db, eventID)
}

func (s *Service) RawScores(ctx context.Context, eventID string) ([]model.RawScoreRow, error) {
	return entryrepo.ListRawScores(ctx, s.db, eventID)
}

func (s *Service) TeamsWithMembers(ctx context.Context, eventID string) ([]model.NamedTeam, error) {
	return entryrepo.ListTeamsWithMembers(ctx, s.db, eventID)
}

func (s *Service) TrackPanels(ctx context.Context, eventID string) ([]entryrepo.TrackPanelMember, error) {
	return entryrepo.ListTrackPanels(ctx, s.db, eventID)
}

func (s *Service) VoteFinalRankingLookup(ctx context.Context, eventID string, accountID string) ([]model.VoteOption, error) {
	shown, err := sites.ShownSiteNames(ctx, s.db, accountID)
	if err != nil {
		return nil, err
	}
	return entryrepo.VoteFinalRanking(ctx, s.db, eventID, shown)
}

type Check = hackentries.Check

type RankOverride = hackentries.RankOverride

var (
	Evaluate       = hackentries.Evaluate
	LoadRules      = hackentries.LoadRules
	MissingMessage = hackentries.MissingMessage
	PresetKnown    = hackentries.PresetKnown
	PresetSettable = hackentries.PresetSettable
	RulesEditable  = hackentries.RulesEditable
	RulesSummary   = hackentries.RulesSummary
	RuleFields     = hackentries.RuleFields
	ValidateRubric = hackentries.ValidateRubric
	WholeInRange   = hackentries.WholeInRange
)

type EventResultsRow = entryrepo.EventResultsRow

type Querier = sqlkit.Querier

type EventConflict = entryrepo.EventConflict

type JudgeScore = entryrepo.JudgeScore

type TrackPanelMember = entryrepo.TrackPanelMember

// TeamsWithMembersInTx reads teams using the publication transaction.
func (s *Service) TeamsWithMembersInTx(ctx context.Context, q sqlkit.Querier, eventID string) ([]model.NamedTeam, error) {
	return entryrepo.ListTeamsWithMembers(ctx, q, eventID)
}

// EventEntriesInTx reads entries using the publication transaction.
func (s *Service) EventEntriesInTx(ctx context.Context, q sqlkit.Querier, eventID string) ([]model.EventEntry, error) {
	return entryrepo.ListEventEntries(ctx, q, eventID)
}

// TeamTracks maps team ID to the team's chosen track.
func (s *Service) TeamTracks(ctx context.Context, eventID string) (map[string]*model.EventTrack, error) {
	return entryrepo.EventTeamTracks(ctx, s.db, eventID)
}
