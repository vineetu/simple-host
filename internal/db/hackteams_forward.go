package db

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
)

// Transitional (cleanup 4.2): callers move to repository/hackteams.
var (
	ListEventTeams         = hackteams.ListEventTeams
	GetEventTeamByID       = hackteams.GetEventTeamByID
	GetEventTeamBySlug     = hackteams.GetEventTeamBySlug
	GetEventTeamByCode     = hackteams.GetEventTeamByCode
	GetEventTeamByCodeOnly = hackteams.GetEventTeamByCodeOnly
	ListTeamPeople         = hackteams.ListTeamPeople
	CountTeamMembers       = hackteams.CountTeamMembers
	TeamSlugExists         = hackteams.TeamSlugExists
	InsertEventTeam        = hackteams.InsertEventTeam
	DeleteEventTeam        = hackteams.DeleteEventTeam
	AdminTeamIDs           = hackteams.AdminTeamIDs
	CreateTeamAndJoin      = hackteams.CreateTeamAndJoin

	lockMember                   = hackteams.LockMember
	lockTeam                     = hackteams.LockTeam
	lockTeamByCode               = hackteams.LockTeamByCode
	lockTeamBySlug               = hackteams.LockTeamBySlug
	setMemberTeam                = hackteams.SetMemberTeam
	rotateTeamCodeTx             = hackteams.RotateTeamCodeTx
	deleteTeamIfEmpty            = hackteams.DeleteTeamIfEmpty
	ErrHackNameTaken             = hackteams.ErrHackNameTaken
	ErrHackTeamNotFound          = hackteams.ErrHackTeamNotFound
	ErrHackTeamFull              = hackteams.ErrHackTeamFull
	ErrHackAlreadyInTeam         = hackteams.ErrHackAlreadyInTeam
	ErrHackCannotRemoveOrganiser = hackteams.ErrHackCannotRemoveOrganiser
	ErrHackNotParticipant        = hackteams.ErrHackNotParticipant
	ErrHackTeamFrozen            = hackteams.ErrHackTeamFrozen
	ErrHackTeamLocked            = hackteams.ErrHackTeamLocked
	ErrHackTeamKept              = hackteams.ErrHackTeamKept
	ErrHackLastTeamAdmin         = hackteams.ErrHackLastTeamAdmin
	ErrHackTeamAdminOnly         = hackteams.ErrHackTeamAdminOnly
	ErrHackFindingStage          = hackteams.ErrHackFindingStage
	ErrHackFindingOff            = hackteams.ErrHackFindingOff
	ErrHackFindingForbidden      = hackteams.ErrHackFindingForbidden
	LockFindingEvent             = hackteams.LockFindingEvent
	findingEvent                 = hackteams.FindingEvent
)

func JoinTeamByCode(ctx context.Context, q Querier, eventID, userID, code string, sizeMax int) (EventTeam, error) {
	return hackteams.JoinTeamByCode(ctx, q, eventID, userID, code, sizeMax, EnsureTeamAdmin)
}
func LeaveTeam(ctx context.Context, q Querier, eventID, userID string) error {
	return hackteams.LeaveTeam(ctx, q, eventID, userID, EnsureTeamAdmin)
}
func MoveParticipantToTeam(ctx context.Context, q Querier, eventID, userID, slug string, sizeMax int) (EventTeam, error) {
	return hackteams.MoveParticipantToTeam(ctx, q, eventID, userID, slug, sizeMax, EnsureTeamAdmin)
}
func RemoveParticipantFromTeam(ctx context.Context, q Querier, eventID, slug, userID string) error {
	return hackteams.RemoveParticipantFromTeam(ctx, q, eventID, slug, userID, EnsureTeamAdmin)
}
