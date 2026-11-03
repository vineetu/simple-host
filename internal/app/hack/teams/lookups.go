package teams

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackteams"
)

// TeamByID forwards the GetEventTeamByID lookup.
func (s *Service) TeamByID(ctx context.Context, teamID string) (db.EventTeam, error) {
	return db.GetEventTeamByID(ctx, s.db, teamID)
}

// TeamBySlugLookup forwards the GetEventTeamBySlug lookup.
func (s *Service) TeamBySlugLookup(ctx context.Context, eventID string, slug string) (db.EventTeam, error) {
	return db.GetEventTeamBySlug(ctx, s.db, eventID, slug)
}

// TeamKey forwards the GetTeamKey lookup.
func (s *Service) TeamKey(ctx context.Context, eventID string, userID string) (db.TeamKeyInfo, error) {
	return db.GetTeamKey(ctx, s.db, eventID, userID)
}

// EventTeams forwards the ListEventTeams lookup.
func (s *Service) EventTeams(ctx context.Context, eventID string) ([]db.EventTeam, error) {
	return db.ListEventTeams(ctx, s.db, eventID)
}

// RenameEventTeamLookup forwards the RenameEventTeam lookup.
func (s *Service) RenameEventTeamLookup(ctx context.Context, eventID string, slug string, name string) error {
	return db.RenameEventTeam(ctx, s.db, eventID, slug, name)
}

// TeamWriteStateForLookup forwards the TeamWriteStateFor lookup.
func (s *Service) TeamWriteStateForLookup(ctx context.Context, teamID string, share bool) (db.TeamWriteState, error) {
	return db.TeamWriteStateFor(ctx, s.db, teamID, share)
}

// TeamWriteStatesForEventLookup forwards the TeamWriteStatesForEvent lookup.
func (s *Service) TeamWriteStatesForEventLookup(ctx context.Context, eventID string) (map[string]db.TeamWriteState, error) {
	return db.TeamWriteStatesForEvent(ctx, s.db, eventID)
}

// ParticipantTeamStages reports whether participants can change teams.
var ParticipantTeamStages = hackteams.ParticipantTeamStages

// Team workflow sentinel errors retain their identity across the service boundary.
var (
	ErrAlreadyInTeam         = db.ErrHackAlreadyInTeam
	ErrCannotRemoveOrganiser = db.ErrHackCannotRemoveOrganiser
	ErrFindingForbidden      = db.ErrHackFindingForbidden
	ErrFindingOff            = db.ErrHackFindingOff
	ErrFindingStage          = db.ErrHackFindingStage
	ErrLastTeamAdmin         = db.ErrHackLastTeamAdmin
	ErrNotParticipant        = db.ErrHackNotParticipant
	ErrTeamAdminOnly         = db.ErrHackTeamAdminOnly
	ErrTeamFrozen            = db.ErrHackTeamFrozen
	ErrTeamFull              = db.ErrHackTeamFull
	ErrTeamKept              = db.ErrHackTeamKept
	ErrTeamLocked            = db.ErrHackTeamLocked
	ErrTeamNotFound          = db.ErrHackTeamNotFound
)
