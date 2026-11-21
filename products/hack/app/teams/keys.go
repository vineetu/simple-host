package teams

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	model "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// SetDeadline gives the team until raw, a time in the event's zone ("" takes
// the extension back), queues notices in the same transaction and then
// pins the event's teams now due.
func (s *Service) SetDeadline(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam, raw *string) (hackmodel.EventTeam, error) {
	t, err := deadlineInput(ev.TimeZone, raw)
	if err != nil {
		return team, err
	}
	if team, err = s.setDeadline(ctx, ev.ID, team, t); err != nil {
		return team, err
	}
	if err = s.PinDue(ctx, ev.ID); err != nil {
		log.Printf("hack: pin %s: %v", ev.ID, err)
	}
	return team, nil
}

// deadlineInput parses a team deadline: required, "" for none, otherwise
// an event time in zone (UTC when the zone is unknown).
func deadlineInput(zone string, raw *string) (sql.NullTime, error) {
	if raw == nil {
		return sql.NullTime{}, &Error{Code: "invalid_deadline", Message: "deadline is required (\"\" removes the extension)"}
	}
	if strings.TrimSpace(*raw) == "" {
		return sql.NullTime{}, nil
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	return eventapp.ParseEventTime(*raw, loc, "deadline")
}

func (s *Service) setDeadline(ctx context.Context, eventID string, team hackmodel.EventTeam, t sql.NullTime) (hackmodel.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return team, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return team, err
	}
	eventDeadline := ev.SubmissionDeadline
	if t.Valid && !eventDeadline.Valid {
		return team, &Error{Code: "no_event_deadline", Message: "set the event's submission deadline first; a team's own deadline extends it"}
	}
	if t.Valid && !t.Time.After(eventDeadline.Time) {
		return team, &Error{Code: "deadline_not_later", Message: "a team's own deadline must be after the event's deadline"}
	}
	// FOR UPDATE on the team row: a deploy let in holds it FOR SHARE.
	team.DeadlineOverride, err = hackteams.LockTeamDeadline(ctx, tx, team.ID)
	if err != nil {
		return team, err
	}
	if err = hackteams.SetTeamDeadline(ctx, tx, team.ID, t); err != nil {
		return team, err
	}
	if err = noticeDeadlineChange(ctx, tx, eventID, team, t); err != nil {
		return team, err
	}
	if err = hackteams.UnpinReopened(ctx, tx, eventID); err != nil {
		return team, err
	}
	if t.Valid {
		// Granting or extending a team's deadline reopens its draft if the sweep had already settled it incomplete.
		if err = entryrepo.UnsettleTeamEntry(ctx, tx, team.ID); err != nil {
			return team, err
		}
	}
	return team, tx.Commit()
}

// BlockedTeamCount counts teams that cannot publish for the connector.
func (s *Service) BlockedTeamCount(ctx context.Context, userID string, publishable int) (int, error) {
	blocked, err := hackteams.ParticipantTeamCount(ctx, s.db, userID)
	if err != nil {
		return 0, err
	}
	if blocked -= publishable; blocked < 0 {
		blocked = 0
	}
	return blocked, nil
}

// RequestEventCert requests a certificate when the event is eligible.
func (s *Service) RequestEventCert(ev hackmodel.Event, request func(string)) {
	if request == nil || ev.TakenDown() {
		return
	}
	switch ev.Stage {
	case "open", "building", "closed":
		request(ev.Slug)
	}
}

// OpenEventSlugs reads event slugs eligible for certificate requests.
func (s *Service) OpenEventSlugs(ctx context.Context, request func(string)) error {
	slugs, err := eventrepo.OpenEventSlugs(ctx, s.db)
	for _, slug := range slugs {
		request(slug)
	}
	return err
}

// NoticeSiteChange tells the team their site went down or came back; it runs after the site's commit, so failures are only logged.
func (s *Service) NoticeSiteChange(ctx context.Context, eventID, teamID string, down bool, reason string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("hack: site notice: %v", err)
		return
	}
	defer tx.Rollback()
	family := []string{hackmembers.NoticeSiteTakenDown, hackmembers.NoticeSiteRestored}
	if down {
		if err = hackmembers.SupersedeTeamNotices(ctx, tx, teamID, "", family...); err == nil {
			err = hackmembers.NotifyTeamMembers(ctx, tx, eventID, teamID, hackmembers.NoticeSiteTakenDown, reason, "")
		}
	} else {
		var knew []string
		if knew, err = hackmembers.TeamMembersWhoKnew(ctx, tx, eventID, teamID, "", hackmembers.NoticeSiteTakenDown); err == nil {
			if err = hackmembers.SupersedeTeamNotices(ctx, tx, teamID, "", family...); err == nil {
				err = hackmembers.NotifyUsers(ctx, tx, eventID, hackmembers.NoticeSiteRestored, teamID, "", "", knew...)
			}
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		log.Printf("hack: site notice: %v", err)
	}
}

// noticeDeadlineChange tells the team about a new, moved or removed
// extension, in the organiser's transaction. Taking an extension away only
// reaches members who could have known they had it.
func noticeDeadlineChange(ctx context.Context, tx *sql.Tx, eventID string, team hackmodel.EventTeam, t sql.NullTime) error {
	before := team.DeadlineOverride
	if before.Valid == t.Valid && (!t.Valid || before.Time.Equal(t.Time)) {
		return nil
	}
	if t.Valid {
		if err := hackmembers.SupersedeTeamNotices(ctx, tx, team.ID, "", hackmembers.NoticeDeadlineChanged); err != nil {
			return err
		}
		return hackmembers.NotifyTeamMembers(ctx, tx, eventID, team.ID, hackmembers.NoticeDeadlineChanged, "extended", "")
	}
	knew, err := hackmembers.TeamMembersWhoKnew(ctx, tx, eventID, team.ID, "", hackmembers.NoticeDeadlineChanged)
	if err != nil {
		return err
	}
	if err := hackmembers.SupersedeTeamNotices(ctx, tx, team.ID, "", hackmembers.NoticeDeadlineChanged); err != nil {
		return err
	}
	return hackmembers.NotifyUsers(ctx, tx, eventID, hackmembers.NoticeDeadlineChanged, team.ID, "", "cleared", knew...)
}

type TeamList struct {
	States   map[string]hackmodel.TeamWriteState
	Members  map[string][]hackmodel.EventPerson
	Versions map[string]bool
	Sites    map[string]model.Site
}

func (s *Service) LoadTeamList(ctx context.Context, ev hackmodel.Event) (TeamList, error) {
	tl := TeamList{Sites: map[string]model.Site{}, Versions: map[string]bool{}, Members: map[string][]hackmodel.EventPerson{}}
	people, err := hackteams.ListEventPeople(ctx, s.db, ev.ID)
	if err != nil {
		return tl, err
	}
	for _, p := range people {
		if p.TeamID.Valid {
			tl.Members[p.TeamID.String] = append(tl.Members[p.TeamID.String], p)
		}
	}
	if tl.States, err = hackteams.TeamWriteStatesForEvent(ctx, s.db, ev.ID); err != nil {
		return tl, err
	}
	list, err := sites.AccountSites(ctx, s.db, ev.AccountID)
	if err != nil {
		return tl, err
	}
	if tl.Versions, err = sites.StoredVersions(ctx, s.db, ev.AccountID); err != nil {
		return tl, err
	}
	for _, site := range list {
		tl.Sites[site.Name] = site
	}
	return tl, nil
}

func (s *Service) TeamBySlug(ctx context.Context, eventID, slug string) (hackmodel.EventTeam, error) {
	return hackteams.GetEventTeamBySlug(ctx, s.db, eventID, slug)
}

// FindTeam is the event's team a path names (trimmed, any case);
// team_not_found when none.
func (s *Service) FindTeam(ctx context.Context, eventID, rawSlug string) (hackmodel.EventTeam, error) {
	return teamBySlug(ctx, s.db, eventID, rawSlug)
}

// OrganiserTeam is FindTeam for an organiser write: refused once the event
// is archived.
func (s *Service) OrganiserTeam(ctx context.Context, ev hackmodel.Event, rawSlug string) (hackmodel.EventTeam, error) {
	if err := organiserStage(ev.Stage, msgEnded); err != nil {
		return hackmodel.EventTeam{}, err
	}
	return teamBySlug(ctx, s.db, ev.ID, rawSlug)
}

// CallerTeam is the team of the calling member; no_team when they are on
// none.
func (s *Service) CallerTeam(ctx context.Context, member hackmodel.EventMember) (hackmodel.EventTeam, error) {
	noTeam := &Error{Code: "no_team", Message: "join or start a team first"}
	if !member.TeamID.Valid {
		return hackmodel.EventTeam{}, noTeam
	}
	team, err := hackteams.GetEventTeamByID(ctx, s.db, member.TeamID.String)
	return team, notFoundAs(err, noTeam)
}

func (s *Service) WriteState(ctx context.Context, eventID, teamID string) (hackmodel.TeamWriteState, error) {
	return hackteams.TeamWriteStateFor(ctx, s.db, eventID, teamID, false)
}

func (s *Service) Track(ctx context.Context, teamID string) (*hackmodel.EventTrack, error) {
	return hackteams.TeamTrack(ctx, s.db, teamID)
}

func (s *Service) MyTeams(ctx context.Context, userID string) ([]hackmodel.MyTeam, error) {
	return hackteams.ListMyTeams(ctx, s.db, userID)
}

func (s *Service) PinDue(ctx context.Context, eventID string) error {
	return hackteams.PinDueTeams(ctx, s.db, eventID)
}

// RemoveOrphanTeamSites moves sites whose teams have gone to Recently deleted.
func (s *Service) RemoveOrphanTeamSites(ctx context.Context, ev hackmodel.Event, trash func(context.Context, string, string) error) {
	if trash == nil {
		return
	}
	orphans, err := hackteams.OrphanTeamSites(ctx, s.db, ev.AccountID)
	if err != nil {
		log.Printf("hack: orphan sites: %v", err)
		return
	}
	for _, site := range orphans {
		if err := trash(ctx, site.UserID, site.Name); err != nil {
			log.Printf("hack: remove site %s/%s: %v", ev.Slug, site.Name, err)
		}
	}
}

func (s *Service) ActiveSiteVersion(ctx context.Context, siteID string) (model.Version, error) {
	return sites.ActiveSiteVersion(ctx, s.db, siteID)
}

type TeamSiteChanger interface {
	SuspendHeldSite(context.Context, sites.HeldSiteSuspension) error
}

// SetSiteTakenDown takes the team's site down with a reason, or puts it back unless the platform took it down.
func (s *Service) SetSiteTakenDown(ctx context.Context, changer TeamSiteChanger, event hackmodel.Event, team hackmodel.EventTeam, down bool, rawReason string) error {
	reason, err := checkLine(rawReason, "reason", 0, 300)
	if err != nil {
		return err
	}
	if changer == nil {
		return internalError(errors.New("no site hooks"))
	}
	err = changer.SuspendHeldSite(ctx, sites.HeldSiteSuspension{
		AccountID: event.AccountID, Name: team.Slug, Prefix: organiserTakedownPrefix, Reason: reason, On: down,
		Also: func(tx *sql.Tx) error { return hackteams.SetTeamSiteTakedown(ctx, tx, team.ID, down, reason) },
	})
	if errors.Is(err, model.ErrNotOrganiserTakedown) {
		return &Error{Code: "platform_takedown", Message: "Simple Hack took this site down; only the platform can put it back"}
	}
	if err != nil {
		return internalError(err)
	}
	if down || team.SiteTakenDownAt.Valid {
		s.NoticeSiteChange(ctx, event.ID, team.ID, down, reason)
	}
	return nil
}
