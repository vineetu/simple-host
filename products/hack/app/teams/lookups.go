package teams

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

func (s *Service) TeamByID(ctx context.Context, teamID string) (model.EventTeam, error) {
	return teamrepo.GetEventTeamByID(ctx, s.db, teamID)
}

func (s *Service) TeamBySlugLookup(ctx context.Context, eventID string, slug string) (model.EventTeam, error) {
	return teamrepo.GetEventTeamBySlug(ctx, s.db, eventID, slug)
}

func (s *Service) EventTeams(ctx context.Context, eventID string) ([]model.EventTeam, error) {
	return teamrepo.ListEventTeams(ctx, s.db, eventID)
}

func (s *Service) RenameEventTeamLookup(ctx context.Context, eventID string, slug string, name string) error {
	return eventrepo.RenameEventTeam(ctx, s.db, eventID, slug, name)
}

func (s *Service) TeamWriteStateForLookup(ctx context.Context, teamID string, share bool) (model.TeamWriteState, error) {
	return teamrepo.TeamWriteStateFor(ctx, s.db, teamID, share)
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
