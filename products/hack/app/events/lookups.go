package events

import (
	"context"
	"database/sql"

	accounts "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/sites"

	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

// CountEligibleJudgesLookup counts joined judges and selected organiser judges.
func (s *Service) CountEligibleJudgesLookup(ctx context.Context, ev model.Event) (int, error) {
	ids, err := hackmembers.JudgeIDs(ctx, s.db, ev.ID, ev.OrganiserJudges)
	return len(ids), err
}

// CountEmailableParticipantsLookup is approved participants with an email-shaped username -- the only ones an announcement's email_participants can reach.
func (s *Service) CountEmailableParticipantsLookup(ctx context.Context, eventID string) (int, error) {
	ids, err := hackmembers.IDs(ctx, s.db, hackmembers.Filter{EventID: eventID, Role: "participant", Status: "approved"})
	if err != nil {
		return 0, err
	}
	users, err := accounts.UsersByIDs(ctx, s.db, ids)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, u := range users {
		if eventrepo.HasAddress(u) {
			n++
		}
	}
	return n, nil
}

// CountEventMembersLookup is the event's approved participants, teams and judges.
func (s *Service) CountEventMembersLookup(ctx context.Context, ev model.Event) (int, int, int, error) {
	return countPeople(ctx, s.db, ev)
}

func (s *Service) CountOnNoTeamLookup(ctx context.Context, eventID string) (int, error) {
	return hackmembers.Count(ctx, s.db, hackmembers.Filter{EventID: eventID, Role: "participant", Status: "approved", NoTeam: true})
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
	u.ScreenshotBytes, err = entryrepo.ScreenshotBytes(ctx, s.db, ev.ID)
	return u, err
}

func (s *Service) GalleryScreenshotLookup(ctx context.Context, eventID string, accountID string, teamSlug string) ([]byte, string, error) {
	live, err := sites.LiveSiteNames(ctx, s.db, accountID)
	if err != nil {
		return nil, "", err
	}
	return entryrepo.GalleryScreenshot(ctx, s.db, eventID, live, teamSlug)
}

func (s *Service) DemoEvent(ctx context.Context, slug string) (model.Event, error) {
	events, err := eventrepo.DemoEventCandidates(ctx, s.db, slug)
	if err != nil {
		return model.Event{}, err
	}
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	counts, err := hackmembers.CountRoles(ctx, s.db, ids)
	if err != nil {
		return model.Event{}, err
	}
	var organisers []string
	for _, c := range counts {
		organisers = append(organisers, c.Organisers...)
	}
	users, err := accounts.UsersByIDs(ctx, s.db, organisers)
	if err != nil {
		return model.Event{}, err
	}
	// The newest open event of that name an operator organises.
	for _, e := range events {
		for _, id := range counts[e.ID].Organisers {
			if users[id].IsAdmin {
				return e, nil
			}
		}
	}
	return model.Event{}, sql.ErrNoRows
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
	m, err := hackmembers.Member(ctx, s.db, eventID, userID, hackmembers.NoLock)
	return hackmembers.WithDisplayName(ctx, s.db, m, err)
}

func (s *Service) HackPreferences(ctx context.Context, userID string) (model.HackPreferences, error) {
	return hackmembers.GetHackPreferences(ctx, s.db, userID)
}

func (s *Service) AnnouncementTimes(ctx context.Context, eventID string) ([]string, error) {
	return eventrepo.ListAnnouncementTimes(ctx, s.db, eventID)
}

func (s *Service) EventAnnouncements(ctx context.Context, eventID string) ([]model.EventAnnouncement, error) {
	return s.ListAnnouncements(ctx, eventID)
}

func (s *Service) EventPeople(ctx context.Context, eventID string) ([]model.EventPerson, error) {
	return teamrepo.ListEventPeople(ctx, s.db, eventID)
}

func (s *Service) EventTracks(ctx context.Context, eventID string) ([]model.EventTrack, error) {
	return eventrepo.ListEventTracks(ctx, s.db, eventID)
}

// EventsForUser is GET /v1/hack/events: every event the person is in, newest
// first, and their membership in each.
func (s *Service) EventsForUser(ctx context.Context, userID string) ([]model.Event, []model.EventMember, error) {
	return memberships(ctx, s.db, userID)
}

func (s *Service) GalleryCards(ctx context.Context, eventID string, accountID string) ([]model.GalleryCard, error) {
	live, err := sites.LiveSiteNames(ctx, s.db, accountID)
	if err != nil {
		return nil, err
	}
	return entryrepo.ListGalleryCards(ctx, s.db, eventID, live)
}

func (s *Service) SetEventIconLookup(ctx context.Context, eventID string, mediaType string, body []byte) error {
	return eventrepo.SetEventIcon(ctx, s.db, eventID, mediaType, body)
}

func (s *Service) SetEventWebsiteModeLookup(ctx context.Context, eventID string, organiserID string, mode string) (model.Event, error) {
	var ev model.Event
	err := s.asOrganiser(ctx, eventID, organiserID, func(tx *sql.Tx) error {
		var err error
		ev, err = eventrepo.SetEventWebsiteMode(ctx, tx, eventID, mode)
		return err
	})
	return ev, err
}

func (s *Service) SetHackPreferencesLookup(ctx context.Context, userID string, p model.HackPreferences) error {
	return hackmembers.SetHackPreferences(ctx, s.db, userID, p)
}

func (s *Service) TeamTrackLookup(ctx context.Context, teamID string) (*model.EventTrack, error) {
	return teamrepo.TeamTrack(ctx, s.db, teamID)
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
