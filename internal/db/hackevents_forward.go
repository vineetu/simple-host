package db

import (
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackevents"
)

// Transitional (cleanup 4.2): callers move to repository/hackevents.
// Alias (cleanup 4.2): defined in internal/model.
type SignupQuestion = model.SignupQuestion

// Alias (cleanup 4.2): defined in internal/model.
type SignupAnswer = model.SignupAnswer

// Alias (cleanup 4.2): defined in internal/model.
type EventTrack = model.EventTrack

var (
	RegistrationSettings = hackevents.RegistrationSettings

	MemberApprovalStatus = hackevents.MemberApprovalStatus

	CountPendingApplications = hackevents.CountPendingApplications

	ListEventTracks = hackevents.ListEventTracks

	TeamTrack = hackevents.TeamTrack
)

var (
	GetEventBySlug             = hackevents.GetEventBySlug
	GetEventByID               = hackevents.GetEventByID
	GetDemoEvent               = hackevents.GetDemoEvent
	GetEventByAccount          = hackevents.GetEventByAccount
	SetEventWebsiteMode        = hackevents.SetEventWebsiteMode
	GetEventByJoinCode         = hackevents.GetEventByJoinCode
	GetEventByJudgeCode        = hackevents.GetEventByJudgeCode
	CountEventMembers          = hackevents.CountEventMembers
	CountEligibleJudges        = hackevents.CountEligibleJudges
	CountOnNoTeam              = hackevents.CountOnNoTeam
	CountEmailableParticipants = hackevents.CountEmailableParticipants
	EventSlugTaken             = hackevents.EventSlugTaken
	CountEventCreatesSince     = hackevents.CountEventCreatesSince

	GetEventForUpdate = hackevents.GetEventForUpdate
	GetEventForShare  = hackevents.GetEventForShare

	TakeDownEvent         = hackevents.TakeDownEvent
	RestoreEvent          = hackevents.RestoreEvent
	DeleteEventAndAccount = hackevents.DeleteEventAndAccount
	ListEventsForUser     = hackevents.ListEventsForUser
	ListAdminEvents       = hackevents.ListAdminEvents
	GetEventMember        = hackevents.GetEventMember
	InsertEventMember     = hackevents.InsertEventMember
	ListEventPeople       = hackevents.ListEventPeople
)

var (
	RenameEventTeam = hackevents.RenameEventTeam
	EventStorage    = hackevents.EventStorage
)

const eventColumns = hackevents.EventColumns

var scanEventFields = hackevents.ScanEventFields
var scanEvent = hackevents.ScanEvent

var (
	markTeamKeptAfterLeave = hackevents.MarkTeamKeptAfterLeave

	ListFormerJudges         = hackevents.ListFormerJudges
	FormerJudgeAssignedCount = hackevents.FormerJudgeAssignedCount
	DeleteFormerJudgeScores  = hackevents.DeleteFormerJudgeScores
)

var UserEmail = hackevents.UserEmail
