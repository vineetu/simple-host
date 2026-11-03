package entries

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

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
