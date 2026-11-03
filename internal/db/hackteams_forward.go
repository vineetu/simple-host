package db

import (
	"context"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackteams"
)

// Transitional (cleanup 4.2): callers move to repository/hackteams.
var (
	ListEventTeams     = hackteams.ListEventTeams
	GetEventTeamByID   = hackteams.GetEventTeamByID
	GetEventTeamBySlug = hackteams.GetEventTeamBySlug

	ListTeamPeople   = hackteams.ListTeamPeople
	CountTeamMembers = hackteams.CountTeamMembers

	AdminTeamIDs = hackteams.AdminTeamIDs

	lockMember = hackteams.LockMember
	lockTeam   = hackteams.LockTeam

	lockTeamBySlug = hackteams.LockTeamBySlug

	rotateTeamCodeTx  = hackteams.RotateTeamCodeTx
	deleteTeamIfEmpty = hackteams.DeleteTeamIfEmpty

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

	findingEvent = hackteams.FindingEvent
)

// JoinTeamByCode forwards to hackteams.JoinTeamByCode.
func JoinTeamByCode(ctx context.Context, q Querier, eventID, userID, code string, sizeMax int) (EventTeam, error) {
	return hackteams.JoinTeamByCode(ctx, q, eventID, userID, code, sizeMax, EnsureTeamAdmin)
}

type TeamWriteState = model.TeamWriteState
type MyTeam = model.MyTeam

var ResolveTeamIdentity = hackteams.ResolveTeamIdentity
var GetTeamKey = hackteams.GetTeamKey
var RevokeTeamKeys = hackteams.RevokeTeamKeys
var RevokeStaleTeamKeys = hackteams.RevokeStaleTeamKeys
var TeamWriteStateFor = hackteams.TeamWriteStateFor
var TeamWriteStatesForEvent = hackteams.TeamWriteStatesForEvent
var PinDueTeams = hackteams.PinDueTeams
var UnpinReopened = hackteams.UnpinReopened
var PinnedVersionOfSite = hackteams.PinnedVersionOfSite
var HoldingAccountEvent = hackteams.HoldingAccountEvent
var TeamNameEverUsed = hackteams.TeamNameEverUsed
var RecordUsedTeamName = hackteams.RecordUsedTeamName
var DeleteEmptyOpenTeams = hackteams.DeleteEmptyOpenTeams
var EventKeyHolders = hackteams.EventKeyHolders
var ListMyTeams = hackteams.ListMyTeams
var StoredVersionsOfAccount = hackteams.StoredVersionsOfAccount
var HackSiteOwnerOnSite = hackteams.HackSiteOwnerOnSite

var EnsureTeamAdmin = hackteams.EnsureTeamAdmin
var SetTeamAdmin = hackteams.SetTeamAdmin
var SetTeamLock = hackteams.SetTeamLock
var NewTeamRequest = hackteams.NewTeamRequest
var ResolveTeamRequest = hackteams.ResolveTeamRequest
var PlanFillTeams = hackteams.PlanFillTeams

var teamKeyBinding = hackteams.TeamKeyBinding
var ReplaceTeamKey = hackteams.ReplaceTeamKey
