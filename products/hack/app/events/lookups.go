package events

import (
	"context"
	"github.com/vsriram/simple-host/host/sites"

	"github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

func (s *Service) CountEligibleJudgesLookup(ctx context.Context, eventID string) (int, error) {
	return eventrepo.CountEligibleJudges(ctx, s.db, eventID)
}

func (s *Service) CountEmailableParticipantsLookup(ctx context.Context, eventID string) (int, error) {
	return eventrepo.CountEmailableParticipants(ctx, s.db, eventID)
}

func (s *Service) CountEventMembersLookup(ctx context.Context, eventID string) (int, int, int, error) {
	return eventrepo.CountEventMembers(ctx, s.db, eventID)
}

func (s *Service) CountOnNoTeamLookup(ctx context.Context, eventID string) (int, error) {
	return eventrepo.CountOnNoTeam(ctx, s.db, eventID)
}

func (s *Service) EventSlugTakenLookup(ctx context.Context, slug string) (bool, error) {
	return eventrepo.EventSlugTaken(ctx, s.db, slug)
}

// EventStorageLookup counts the event account's site records (Recently deleted included) and kept versions, and its entry screenshots.
func (s *Service) EventStorageLookup(ctx context.Context, ev model.Event) (model.EventStorageUsage, error) {
	var u model.EventStorageUsage
	var err error
	if u.Sites, u.Versions, err = sites.AccountSiteCounts(ctx, s.db, ev.AccountID); err != nil {
		return u, err
	}
	u.ScreenshotBytes, err = eventrepo.EventScreenshotBytes(ctx, s.db, ev.ID)
	return u, err
}

func (s *Service) GalleryScreenshotLookup(ctx context.Context, eventID string, accountID string, teamSlug string) ([]byte, string, error) {
	live, err := sites.LiveSiteNames(ctx, s.db, accountID)
	if err != nil {
		return nil, "", err
	}
	return eventrepo.GalleryScreenshot(ctx, s.db, eventID, live, teamSlug)
}

func (s *Service) DemoEvent(ctx context.Context, slug string) (model.Event, error) {
	return eventrepo.GetDemoEvent(ctx, s.db, slug)
}

func (s *Service) EventByAccount(ctx context.Context, accountID string) (model.Event, error) {
	return eventrepo.GetEventByAccount(ctx, s.db, accountID)
}

func (s *Service) EventByJoinCode(ctx context.Context, code string) (model.Event, error) {
	return eventrepo.GetEventByJoinCode(ctx, s.db, code)
}

func (s *Service) EventByJudgeCode(ctx context.Context, code string) (model.Event, error) {
	return eventrepo.GetEventByJudgeCode(ctx, s.db, code)
}

func (s *Service) EventBySlug(ctx context.Context, slug string) (model.Event, error) {
	return eventrepo.GetEventBySlug(ctx, s.db, slug)
}

func (s *Service) EventIcon(ctx context.Context, eventID string) (string, []byte, error) {
	return eventrepo.GetEventIcon(ctx, s.db, eventID)
}

func (s *Service) EventMember(ctx context.Context, eventID string, userID string) (model.EventMember, error) {
	return eventrepo.GetEventMember(ctx, s.db, eventID, userID)
}

func (s *Service) HackPreferences(ctx context.Context, userID string) (model.HackPreferences, error) {
	return eventrepo.GetHackPreferences(ctx, s.db, userID)
}

func (s *Service) AnnouncementTimes(ctx context.Context, eventID string) ([]string, error) {
	return eventrepo.ListAnnouncementTimes(ctx, s.db, eventID)
}

func (s *Service) EventAnnouncements(ctx context.Context, eventID string) ([]model.EventAnnouncement, error) {
	return s.ListAnnouncements(ctx, eventID)
}

func (s *Service) EventPeople(ctx context.Context, eventID string) ([]model.EventPerson, error) {
	return eventrepo.ListEventPeople(ctx, s.db, eventID)
}

func (s *Service) EventTracks(ctx context.Context, eventID string) ([]model.EventTrack, error) {
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

func (s *Service) EventsForUser(ctx context.Context, userID string) ([]model.Event, []string, error) {
	return eventrepo.ListEventsForUser(ctx, s.db, userID)
}

func (s *Service) GalleryCards(ctx context.Context, eventID string, accountID string) ([]model.GalleryCard, error) {
	live, err := sites.LiveSiteNames(ctx, s.db, accountID)
	if err != nil {
		return nil, err
	}
	return eventrepo.ListGalleryCards(ctx, s.db, eventID, live)
}

func (s *Service) SetEventIconLookup(ctx context.Context, eventID string, mediaType string, body []byte) error {
	return eventrepo.SetEventIcon(ctx, s.db, eventID, mediaType, body)
}

func (s *Service) SetEventWebsiteModeLookup(ctx context.Context, eventID string, organiserID string, mode string) (model.Event, error) {
	return eventrepo.SetEventWebsiteMode(ctx, s.db, eventID, organiserID, mode)
}

func (s *Service) SetHackPreferencesLookup(ctx context.Context, userID string, p model.HackPreferences) error {
	return eventrepo.SetHackPreferences(ctx, s.db, userID, p)
}

func (s *Service) TeamTrackLookup(ctx context.Context, teamID string) (*model.EventTrack, error) {
	return eventrepo.TeamTrack(ctx, s.db, teamID)
}

var (
	AgeRangeLabels           = hackevents.AgeRangeLabels
	AgeRangeOrder            = hackevents.AgeRangeOrder
	ApplicationsClosedReason = hackevents.ApplicationsClosedReason
	ApplicationsOpenNow      = hackevents.ApplicationsOpenNow
	CountryName              = hackevents.CountryName
	EntryFields              = hackevents.EntryFields
	EventOver                = hackevents.EventOver
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

var Country = hackevents.Country
