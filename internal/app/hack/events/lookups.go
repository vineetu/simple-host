package events

import (
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"

	"context"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// CountEligibleJudgesLookup forwards the CountEligibleJudges lookup.
func (s *Service) CountEligibleJudgesLookup(ctx context.Context, eventID string) (int, error) {
	return eventrepo.CountEligibleJudges(ctx, s.db, eventID)
}

// CountEmailableParticipantsLookup forwards the CountEmailableParticipants lookup.
func (s *Service) CountEmailableParticipantsLookup(ctx context.Context, eventID string) (int, error) {
	return eventrepo.CountEmailableParticipants(ctx, s.db, eventID)
}

// CountEventMembersLookup forwards the CountEventMembers lookup.
func (s *Service) CountEventMembersLookup(ctx context.Context, eventID string) (int, int, int, error) {
	return eventrepo.CountEventMembers(ctx, s.db, eventID)
}

// CountOnNoTeamLookup forwards the CountOnNoTeam lookup.
func (s *Service) CountOnNoTeamLookup(ctx context.Context, eventID string) (int, error) {
	return eventrepo.CountOnNoTeam(ctx, s.db, eventID)
}

// EventSlugTakenLookup forwards the EventSlugTaken lookup.
func (s *Service) EventSlugTakenLookup(ctx context.Context, slug string) (bool, error) {
	return eventrepo.EventSlugTaken(ctx, s.db, slug)
}

// EventStorageLookup forwards the EventStorage lookup.
func (s *Service) EventStorageLookup(ctx context.Context, ev model.Event) (model.EventStorageUsage, error) {
	return eventrepo.EventStorage(ctx, s.db, ev)
}

// GalleryScreenshotLookup forwards the GalleryScreenshot lookup.
func (s *Service) GalleryScreenshotLookup(ctx context.Context, eventID string, accountID string, teamSlug string) ([]byte, string, error) {
	return eventrepo.GalleryScreenshot(ctx, s.db, eventID, accountID, teamSlug)
}

// DemoEvent forwards the GetDemoEvent lookup.
func (s *Service) DemoEvent(ctx context.Context, slug string) (model.Event, error) {
	return eventrepo.GetDemoEvent(ctx, s.db, slug)
}

// EventByAccount forwards the GetEventByAccount lookup.
func (s *Service) EventByAccount(ctx context.Context, accountID string) (model.Event, error) {
	return eventrepo.GetEventByAccount(ctx, s.db, accountID)
}

// EventByJoinCode forwards the GetEventByJoinCode lookup.
func (s *Service) EventByJoinCode(ctx context.Context, code string) (model.Event, error) {
	return eventrepo.GetEventByJoinCode(ctx, s.db, code)
}

// EventByJudgeCode forwards the GetEventByJudgeCode lookup.
func (s *Service) EventByJudgeCode(ctx context.Context, code string) (model.Event, error) {
	return eventrepo.GetEventByJudgeCode(ctx, s.db, code)
}

// EventBySlug forwards the GetEventBySlug lookup.
func (s *Service) EventBySlug(ctx context.Context, slug string) (model.Event, error) {
	return eventrepo.GetEventBySlug(ctx, s.db, slug)
}

// EventIcon forwards the GetEventIcon lookup.
func (s *Service) EventIcon(ctx context.Context, eventID string) (string, []byte, error) {
	return eventrepo.GetEventIcon(ctx, s.db, eventID)
}

// EventMember forwards the GetEventMember lookup.
func (s *Service) EventMember(ctx context.Context, eventID string, userID string) (model.EventMember, error) {
	return eventrepo.GetEventMember(ctx, s.db, eventID, userID)
}

// HackPreferences forwards the GetHackPreferences lookup.
func (s *Service) HackPreferences(ctx context.Context, userID string) (model.HackPreferences, error) {
	return eventrepo.GetHackPreferences(ctx, s.db, userID)
}

// AnnouncementTimes forwards the ListAnnouncementTimes lookup.
func (s *Service) AnnouncementTimes(ctx context.Context, eventID string) ([]string, error) {
	return eventrepo.ListAnnouncementTimes(ctx, s.db, eventID)
}

// EventAnnouncements forwards the ListEventAnnouncements lookup.
func (s *Service) EventAnnouncements(ctx context.Context, eventID string) ([]model.EventAnnouncement, error) {
	return eventrepo.ListEventAnnouncements(ctx, s.db, eventID)
}

// EventPeople forwards the ListEventPeople lookup.
func (s *Service) EventPeople(ctx context.Context, eventID string) ([]model.EventPerson, error) {
	return eventrepo.ListEventPeople(ctx, s.db, eventID)
}

// EventTracks forwards the ListEventTracks lookup.
func (s *Service) EventTracks(ctx context.Context, eventID string) ([]model.EventTrack, error) {
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

// EventsForUser forwards the ListEventsForUser lookup.
func (s *Service) EventsForUser(ctx context.Context, userID string) ([]model.Event, []string, error) {
	return eventrepo.ListEventsForUser(ctx, s.db, userID)
}

// GalleryCards forwards the ListGalleryCards lookup.
func (s *Service) GalleryCards(ctx context.Context, eventID string, accountID string) ([]model.GalleryCard, error) {
	return eventrepo.ListGalleryCards(ctx, s.db, eventID, accountID)
}

// SetEventIconLookup forwards the SetEventIcon lookup.
func (s *Service) SetEventIconLookup(ctx context.Context, eventID string, mediaType string, body []byte) error {
	return eventrepo.SetEventIcon(ctx, s.db, eventID, mediaType, body)
}

// SetEventWebsiteModeLookup forwards the SetEventWebsiteMode lookup.
func (s *Service) SetEventWebsiteModeLookup(ctx context.Context, eventID string, organiserID string, mode string) (model.Event, error) {
	return eventrepo.SetEventWebsiteMode(ctx, s.db, eventID, organiserID, mode)
}

// SetHackPreferencesLookup forwards the SetHackPreferences lookup.
func (s *Service) SetHackPreferencesLookup(ctx context.Context, userID string, p model.HackPreferences) error {
	return eventrepo.SetHackPreferences(ctx, s.db, userID, p)
}

// TeamTrackLookup forwards the TeamTrack lookup.
func (s *Service) TeamTrackLookup(ctx context.Context, teamID string) (*model.EventTrack, error) {
	return eventrepo.TeamTrack(ctx, s.db, teamID)
}

// Rule exports preserve the existing validation and display decisions.
var (
	AgeRangeLabels           = hackevents.AgeRangeLabels
	AgeRangeOrder            = hackevents.AgeRangeOrder
	ApplicationsClosedReason = hackevents.ApplicationsClosedReason
	ApplicationsOpenNow      = hackevents.ApplicationsOpenNow
	CountryName              = hackevents.CountryName
	EntryFields              = hackevents.EntryFields
	GenderLabels             = hackevents.GenderLabels
	GenderOrder              = hackevents.GenderOrder
	JoinedViaLabel           = hackevents.JoinedViaLabel
	Line                     = hackevents.Line
	Midnight                 = hackevents.Midnight
	NormalizeCode            = hackevents.NormalizeCode
	SignupAnswers            = hackevents.SignupAnswers
	StripInvisible           = hackevents.StripInvisible
	SubmissionsClosedStage   = hackevents.SubmissionsClosedStage
	Text                     = hackevents.Text
	TrackSlug                = hackevents.TrackSlug
	UniqueError              = hackevents.UniqueError
)

// Country validates a country code.
var Country = hackevents.Country
