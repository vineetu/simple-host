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

type EventContent = model.EventContent
type EventAnnouncement = model.EventAnnouncement
type EventMail = model.EventMail
type EntryReminder = model.EntryReminder
type GalleryCard = model.GalleryCard
type Notice = model.Notice
type PendingJoinRequest = model.PendingJoinRequest
type PendingInvite = model.PendingInvite
type MailBatch = model.MailBatch
type MailNotice = model.MailNotice
type HackPreferences = model.HackPreferences

var GetEventContent = hackevents.GetEventContent
var PutEventContent = hackevents.PutEventContent
var ListEventAnnouncements = hackevents.ListEventAnnouncements
var ListAnnouncementTimes = hackevents.ListAnnouncementTimes
var CreateEventAnnouncement = hackevents.CreateEventAnnouncement
var QueueEntryReceipt = hackevents.QueueEntryReceipt
var NextEventMail = hackevents.NextEventMail
var DeferEventMail = hackevents.DeferEventMail
var MarkEventMailSent = hackevents.MarkEventMailSent
var DueEntryReminders = hackevents.DueEntryReminders
var ClaimEntryReminder = hackevents.ClaimEntryReminder
var ListGalleryCards = hackevents.ListGalleryCards
var GalleryScreenshot = hackevents.GalleryScreenshot
var SetEventIcon = hackevents.SetEventIcon
var GetEventIcon = hackevents.GetEventIcon
var NoticeTeamScoped = hackevents.NoticeTeamScoped
var NotifyUsers = hackevents.NotifyUsers
var NotifyTeamAdmins = hackevents.NotifyTeamAdmins
var TeamMemberIDs = hackevents.TeamMemberIDs
var NotifyTeamMembers = hackevents.NotifyTeamMembers
var NotifyOrganisers = hackevents.NotifyOrganisers
var SupersedeTeamNotices = hackevents.SupersedeTeamNotices
var TeamMembersWhoKnew = hackevents.TeamMembersWhoKnew
var ListNews = hackevents.ListNews
var MarkNewsSeen = hackevents.MarkNewsSeen
var ListPendingJoinRequests = hackevents.ListPendingJoinRequests
var ListPendingInvites = hackevents.ListPendingInvites
var CountWaitingApplications = hackevents.CountWaitingApplications
var EmailUpdates = hackevents.EmailUpdates
var NextMailBatch = hackevents.NextMailBatch
var IsMailBatchRaced = hackevents.IsMailBatchRaced
var FinishMailNotices = hackevents.FinishMailNotices
var DeferMailNotices = hackevents.DeferMailNotices
var PruneNotices = hackevents.PruneNotices
var GetHackPreferences = hackevents.GetHackPreferences
var SetHackPreferences = hackevents.SetHackPreferences

const NoticeEntryReminder = hackevents.NoticeEntryReminder
const NoticeJoinRequest = hackevents.NoticeJoinRequest
const NoticeInvite = hackevents.NoticeInvite
const NoticeRequestAnswered = hackevents.NoticeRequestAnswered
const NoticeApplicationNew = hackevents.NoticeApplicationNew
const NoticeApplicationDecided = hackevents.NoticeApplicationDecided
const NoticeSiteTakenDown = hackevents.NoticeSiteTakenDown
const NoticeSiteRestored = hackevents.NoticeSiteRestored
const NoticeDeadlineChanged = hackevents.NoticeDeadlineChanged
const NoticeTeamAdmin = hackevents.TeamAdminNotice
const NoticeTeamLocked = hackevents.NoticeTeamLocked
const NoticeTeamRemoved = hackevents.NoticeTeamRemoved

type DirectoryEvent = model.DirectoryEvent

var ListDirectoryEvents = hackevents.ListDirectoryEvents

var NewUUID = hackevents.NewUUID
var LockUserRow = hackevents.LockUserRow
var AccountKeyCount = hackevents.AccountKeyCount
var EventAccountFlag = hackevents.EventAccountFlag

var HackNotificationAllowed = hackevents.HackNotificationAllowed
var HackNotificationContextExists = hackevents.HackNotificationContextExists
var lockNotificationMember = hackevents.LockNotificationMember
