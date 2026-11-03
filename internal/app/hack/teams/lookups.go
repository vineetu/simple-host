package teams

import (
	"context"

	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackteams"
)

// TeamByID forwards the GetEventTeamByID lookup.
func (s *Service) TeamByID(ctx context.Context, teamID string) (model.EventTeam, error) {
	return teamrepo.GetEventTeamByID(ctx, s.db, teamID)
}

// TeamBySlugLookup forwards the GetEventTeamBySlug lookup.
func (s *Service) TeamBySlugLookup(ctx context.Context, eventID string, slug string) (model.EventTeam, error) {
	return teamrepo.GetEventTeamBySlug(ctx, s.db, eventID, slug)
}

// TeamKey forwards the GetTeamKey lookup.
func (s *Service) TeamKey(ctx context.Context, eventID string, userID string) (model.TeamKeyInfo, error) {
	return teamrepo.GetTeamKey(ctx, s.db, eventID, userID)
}

// EventTeams forwards the ListEventTeams lookup.
func (s *Service) EventTeams(ctx context.Context, eventID string) ([]model.EventTeam, error) {
	return teamrepo.ListEventTeams(ctx, s.db, eventID)
}

// RenameEventTeamLookup forwards the RenameEventTeam lookup.
func (s *Service) RenameEventTeamLookup(ctx context.Context, eventID string, slug string, name string) error {
	return eventrepo.RenameEventTeam(ctx, s.db, eventID, slug, name)
}

// TeamWriteStateForLookup forwards the TeamWriteStateFor lookup.
func (s *Service) TeamWriteStateForLookup(ctx context.Context, teamID string, share bool) (model.TeamWriteState, error) {
	return teamrepo.TeamWriteStateFor(ctx, s.db, teamID, share)
}

// TeamWriteStatesForEventLookup forwards the TeamWriteStatesForEvent lookup.
func (s *Service) TeamWriteStatesForEventLookup(ctx context.Context, eventID string) (map[string]model.TeamWriteState, error) {
	return teamrepo.TeamWriteStatesForEvent(ctx, s.db, eventID)
}

// ParticipantTeamStages reports whether participants can change teams.
var ParticipantTeamStages = hackteams.ParticipantTeamStages

// Team workflow sentinel errors retain their identity across the service boundary.
var (
	ErrAlreadyInTeam         = teamrepo.ErrHackAlreadyInTeam
	ErrCannotRemoveOrganiser = eventrepo.ErrHackCannotRemoveOrganiser
	ErrFindingForbidden      = teamrepo.ErrHackFindingForbidden
	ErrFindingOff            = teamrepo.ErrHackFindingOff
	ErrFindingStage          = teamrepo.ErrHackFindingStage
	ErrLastTeamAdmin         = teamrepo.ErrHackLastTeamAdmin
	ErrNotParticipant        = teamrepo.ErrHackNotParticipant
	ErrTeamAdminOnly         = teamrepo.ErrHackTeamAdminOnly
	ErrTeamFrozen            = teamrepo.ErrHackTeamFrozen
	ErrTeamFull              = teamrepo.ErrHackTeamFull
	ErrTeamKept              = teamrepo.ErrHackTeamKept
	ErrTeamLocked            = teamrepo.ErrHackTeamLocked
	ErrTeamNotFound          = teamrepo.ErrHackTeamNotFound
)
