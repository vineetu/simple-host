package db

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
)

// Transitional (cleanup 4.2): callers move to repository/hackevents.
// Alias (cleanup 4.2): defined in internal/model.
type SignupQuestion = model.SignupQuestion

// Alias (cleanup 4.2): defined in internal/model.
type SignupAnswer = model.SignupAnswer

// Alias (cleanup 4.2): defined in internal/model.
type RegistrationApplication = model.RegistrationApplication

// Alias (cleanup 4.2): defined in internal/model.
type ApplicantProfile = model.ApplicantProfile

// Alias (cleanup 4.2): defined in internal/model.
type MemberStatus = model.MemberStatus

// Alias (cleanup 4.2): defined in internal/model.
type EventTrack = model.EventTrack

var (
	RegistrationSettings             = hackevents.RegistrationSettings
	SetRegistrationSettings          = hackevents.SetRegistrationSettings
	MemberApprovalStatus             = hackevents.MemberApprovalStatus
	JoinParticipantRegistration      = hackevents.JoinParticipantRegistration
	ListRegistrationApplications     = hackevents.ListRegistrationApplications
	ApplyParticipant                 = hackevents.ApplyParticipant
	CountPendingApplications         = hackevents.CountPendingApplications
	CountPendingApplicationsForEvent = hackevents.CountPendingApplicationsForEvent
	GetMemberStatus                  = hackevents.GetMemberStatus
	DecideRegistration               = hackevents.DecideRegistration
	ListEventTracks                  = hackevents.ListEventTracks
	ReplaceEventTracks               = hackevents.ReplaceEventTracks
	SetTeamTrack                     = hackevents.SetTeamTrack
	TeamTrack                        = hackevents.TeamTrack
)

var (
	GetEventBySlug               = hackevents.GetEventBySlug
	GetEventByID                 = hackevents.GetEventByID
	GetDemoEvent                 = hackevents.GetDemoEvent
	GetEventByAccount            = hackevents.GetEventByAccount
	SetEventWebsiteMode          = hackevents.SetEventWebsiteMode
	GetEventByJoinCode           = hackevents.GetEventByJoinCode
	GetEventByJudgeCode          = hackevents.GetEventByJudgeCode
	CountEventMembers            = hackevents.CountEventMembers
	CountEligibleJudges          = hackevents.CountEligibleJudges
	CountOnNoTeam                = hackevents.CountOnNoTeam
	CountEmailableParticipants   = hackevents.CountEmailableParticipants
	EventSlugTaken               = hackevents.EventSlugTaken
	CountEventCreatesSince       = hackevents.CountEventCreatesSince
	CountActiveEventsByOrganiser = hackevents.CountActiveEventsByOrganiser
	InsertEventCreateLog         = hackevents.InsertEventCreateLog
	InsertEvent                  = hackevents.InsertEvent
	UpdateEventPatch             = hackevents.UpdateEventPatch
	GetEventForUpdate            = hackevents.GetEventForUpdate
	GetEventForShare             = hackevents.GetEventForShare
	UpdateEventEntrySettings     = hackevents.UpdateEventEntrySettings
	SetEventStage                = hackevents.SetEventStage
	SetEventJoinCode             = hackevents.SetEventJoinCode
	SetEventJudgeCode            = hackevents.SetEventJudgeCode
	TakeDownEvent                = hackevents.TakeDownEvent
	RestoreEvent                 = hackevents.RestoreEvent
	DeleteEventAndAccount        = hackevents.DeleteEventAndAccount
	ListEventsForUser            = hackevents.ListEventsForUser
	ListAdminEvents              = hackevents.ListAdminEvents
	GetEventMember               = hackevents.GetEventMember
	InsertEventMember            = hackevents.InsertEventMember
	ListEventPeople              = hackevents.ListEventPeople
	ListParticipantsOnNoTeam     = hackevents.ListParticipantsOnNoTeam
)

// Alias (cleanup 4.2): defined in internal/model.
type EventStorageUsage = model.EventStorageUsage

var (
	SetOrganiserInvite    = hackevents.SetOrganiserInvite
	DeleteOrganiserInvite = hackevents.DeleteOrganiserInvite
	OrganiserInviteEvent  = hackevents.OrganiserInviteEvent
	RenameEventTeam       = hackevents.RenameEventTeam
	EventStorage          = hackevents.EventStorage
)

const eventColumns = hackevents.EventColumns

var scanEventFields = hackevents.ScanEventFields
var scanEvent = hackevents.ScanEvent

var (
	LeaveSelf                = hackevents.LeaveSelf
	OtherEventOrganisers     = hackevents.OtherEventOrganisers
	TeamKeptIfEmptied        = hackevents.TeamKeptIfEmptied
	markTeamKeptAfterLeave   = hackevents.MarkTeamKeptAfterLeave
	RecordFormerJudge        = hackevents.RecordFormerJudge
	ListFormerJudges         = hackevents.ListFormerJudges
	FormerJudgeAssignedCount = hackevents.FormerJudgeAssignedCount
	DeleteFormerJudgeScores  = hackevents.DeleteFormerJudgeScores
)

func leaveOperations() hackevents.LeaveOperations {
	return hackevents.LeaveOperations{LockMember: lockMember, LockTeam: lockTeam, DeleteTeamIfEmpty: deleteTeamIfEmpty, EnsureTeamAdmin: EnsureTeamAdmin}
}
func LeaveMembership(ctx context.Context, q Querier, eventID, userID string, selfLeave bool) (string, error) {
	return hackevents.LeaveMembership(ctx, q, eventID, userID, selfLeave, leaveOperations())
}
func RemoveCoOrganiser(ctx context.Context, q *sql.Tx, eventID, actorID, userID string) error {
	return hackevents.RemoveCoOrganiser(ctx, q, eventID, actorID, userID, leaveOperations())
}

// Alias (cleanup 4.2): defined in internal/model.
type OtherOrganiser = model.OtherOrganiser

// Alias (cleanup 4.2): defined in internal/model.
type FormerJudge = model.FormerJudge

var UserEmail = hackevents.UserEmail

func RemovePersonFromEvent(ctx context.Context, q Querier, eventID, userID string) error {
	return hackevents.RemovePersonFromEvent(ctx, q, eventID, userID, leaveOperations())
}
