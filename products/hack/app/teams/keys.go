package teams

import (
	"context"
	"database/sql"
	"errors"
	"log"

	model "github.com/vsriram/simple-host/internal/model"
	siterepo "github.com/vsriram/simple-host/internal/repository/sites"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// MintTeamKey replaces a participant's key under the event and member locks.
func (s *Service) MintTeamKey(ctx context.Context, event model.Event, userID, key string) (model.EventTeam, model.TeamKeyInfo, error) {
	var team model.EventTeam
	var k model.TeamKeyInfo
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return team, k, err
	}
	defer tx.Rollback()
	// The event lock precedes the member lock, matching leave and avoiding a cycle.
	if _, err = eventrepo.GetEventForShare(ctx, tx, event.ID); err != nil {
		return team, k, err
	}
	teamID, err := hackteams.LockKeyMembership(ctx, tx, event.ID, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return team, k, &Error{Code: "event_not_found"}
	}
	if err != nil {
		return team, k, err
	}
	if !teamID.Valid {
		return team, k, &Error{Code: "no_team"}
	}
	team, err = hackteams.GetEventTeamByID(ctx, tx, teamID.String)
	if err != nil {
		return team, k, err
	}
	st, err := hackteams.TeamWriteStateFor(ctx, tx, team.ID, false)
	if err != nil {
		return team, k, err
	}
	if code := st.WriteRefusal(); code != "" {
		return team, k, &Error{Code: code}
	}
	k, err = hackteams.ReplaceTeamKey(ctx, tx, event.ID, team.ID, userID, key, "team "+team.Slug+" · "+event.Slug)
	if err != nil {
		return team, k, err
	}
	if err = tx.Commit(); err != nil {
		return team, k, err
	}
	return team, k, nil
}

// SetDeadline changes the override and queues notices in the same transaction.
func (s *Service) SetDeadline(ctx context.Context, eventID string, team model.EventTeam, t sql.NullTime) (model.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return team, err
	}
	defer tx.Rollback()
	eventDeadline, err := hackteams.LockEventDeadline(ctx, tx, eventID)
	if err != nil {
		return team, err
	}
	if t.Valid && !eventDeadline.Valid {
		return team, &Error{Code: "no_event_deadline"}
	}
	if t.Valid && !t.Time.After(eventDeadline.Time) {
		return team, &Error{Code: "deadline_not_later"}
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
		// Section 7, "Unsettle": granting or extending a team's deadline
		// reopens its draft if the sweep had already settled it incomplete.
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
func (s *Service) RequestEventCert(ev model.Event, request func(string)) {
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
	rows, err := hackteams.OpenEventSlugs(ctx, s.db)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			request(slug)
		}
	}
	return nil
}

// NoticeSiteChange tells the team their site was taken down, with the
// organiser's reason, or put back. It runs after the site handler's own
// transaction has committed (that one is the site's, not the event's), so a
// failure here is logged and the change itself stands. A restore reaches only
// members who could have known it was down: anyone whose take-down notice
// was never seen nor emailed (a restore within the first minute) hears
// nothing at all.
func (s *Service) NoticeSiteChange(ctx context.Context, eventID, teamID string, down bool, reason string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("hack: site notice: %v", err)
		return
	}
	defer tx.Rollback()
	family := []string{eventrepo.NoticeSiteTakenDown, eventrepo.NoticeSiteRestored}
	if down {
		if err = eventrepo.SupersedeTeamNotices(ctx, tx, teamID, "", family...); err == nil {
			err = eventrepo.NotifyTeamMembers(ctx, tx, eventID, teamID, eventrepo.NoticeSiteTakenDown, reason, "")
		}
	} else {
		var knew []string
		if knew, err = eventrepo.TeamMembersWhoKnew(ctx, tx, eventID, teamID, "", eventrepo.NoticeSiteTakenDown); err == nil {
			if err = eventrepo.SupersedeTeamNotices(ctx, tx, teamID, "", family...); err == nil {
				err = eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeSiteRestored, teamID, "", "", knew...)
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
func noticeDeadlineChange(ctx context.Context, tx *sql.Tx, eventID string, team model.EventTeam, t sql.NullTime) error {
	before := team.DeadlineOverride
	if before.Valid == t.Valid && (!t.Valid || before.Time.Equal(t.Time)) {
		return nil
	}
	if t.Valid {
		if err := eventrepo.SupersedeTeamNotices(ctx, tx, team.ID, "", eventrepo.NoticeDeadlineChanged); err != nil {
			return err
		}
		return eventrepo.NotifyTeamMembers(ctx, tx, eventID, team.ID, eventrepo.NoticeDeadlineChanged, "extended", "")
	}
	knew, err := eventrepo.TeamMembersWhoKnew(ctx, tx, eventID, team.ID, "", eventrepo.NoticeDeadlineChanged)
	if err != nil {
		return err
	}
	if err := eventrepo.SupersedeTeamNotices(ctx, tx, team.ID, "", eventrepo.NoticeDeadlineChanged); err != nil {
		return err
	}
	return eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeDeadlineChanged, team.ID, "", "cleared", knew...)
}

// TeamList contains data shared by event team listings.
type TeamList struct {
	States   map[string]model.TeamWriteState
	Members  map[string][]model.EventPerson
	Versions map[string]bool
	Sites    map[string]model.Site
}

// LoadTeamList reads the batched data for event team listings.
func (s *Service) LoadTeamList(ctx context.Context, ev model.Event) (TeamList, error) {
	tl := TeamList{Sites: map[string]model.Site{}, Versions: map[string]bool{}, Members: map[string][]model.EventPerson{}}
	people, err := eventrepo.ListEventPeople(ctx, s.db, ev.ID)
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
	sites, err := siterepo.ListSitesByUser(ctx, s.db, ev.AccountID)
	if err != nil {
		return tl, err
	}
	if tl.Versions, err = hackteams.StoredVersionsOfAccount(ctx, s.db, ev.AccountID); err != nil {
		return tl, err
	}
	for _, site := range sites {
		tl.Sites[site.Name] = site
	}
	return tl, nil
}

// Key reads a participant's key metadata.
func (s *Service) Key(ctx context.Context, eventID, userID string) (model.TeamKeyInfo, error) {
	return hackteams.GetTeamKey(ctx, s.db, eventID, userID)
}

// RevokeKey revokes a participant's keys.
func (s *Service) RevokeKey(ctx context.Context, eventID, userID string) error {
	_, err := hackteams.RevokeTeamKeys(ctx, s.db, eventID, userID)
	return err
}

// RevokePersonKey checks membership before revoking a key.
func (s *Service) RevokePersonKey(ctx context.Context, eventID, userID string) error {
	if _, err := eventrepo.GetEventMember(ctx, s.db, eventID, userID); err != nil {
		return err
	}
	return s.RevokeKey(ctx, eventID, userID)
}

// TeamBySlug reads an event's team.
func (s *Service) TeamBySlug(ctx context.Context, eventID, slug string) (model.EventTeam, error) {
	return hackteams.GetEventTeamBySlug(ctx, s.db, eventID, slug)
}

// WriteState reads a team's publishing state.
func (s *Service) WriteState(ctx context.Context, teamID string) (model.TeamWriteState, error) {
	return hackteams.TeamWriteStateFor(ctx, s.db, teamID, false)
}

// Track reads a team's track.
func (s *Service) Track(ctx context.Context, teamID string) (*model.EventTrack, error) {
	return eventrepo.TeamTrack(ctx, s.db, teamID)
}

// MyTeams reads the caller's publishable teams.
func (s *Service) MyTeams(ctx context.Context, userID string) ([]model.MyTeam, error) {
	return hackteams.ListMyTeams(ctx, s.db, userID)
}

// PinDue pins teams whose deadline has passed.
func (s *Service) PinDue(ctx context.Context, eventID string) error {
	return hackteams.PinDueTeams(ctx, s.db, eventID)
}

// RemoveOrphanTeamSites moves sites whose teams have gone to Recently deleted.
func (s *Service) RemoveOrphanTeamSites(ctx context.Context, ev model.Event, trash func(context.Context, string, string) error) {
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

// ActiveSiteVersion reads the active version.
func (s *Service) ActiveSiteVersion(ctx context.Context, siteID string) (model.Version, error) {
	return siterepo.GetActiveSiteVersion(ctx, s.db, siteID)
}

// TeamSiteChanger updates the site's organiser takedown state.
type TeamSiteChanger interface {
	SetTeamSiteTakenDown(context.Context, string, string, string, bool, string) error
}

// SetSiteTakenDown changes a site's state and sends the existing team notices.
func (s *Service) SetSiteTakenDown(ctx context.Context, sites TeamSiteChanger, event model.Event, team model.EventTeam, down bool, reason string) error {
	if err := sites.SetTeamSiteTakenDown(ctx, event.AccountID, team.ID, team.Slug, down, reason); err != nil {
		return err
	}
	if down || team.SiteTakenDownAt.Valid {
		s.NoticeSiteChange(ctx, event.ID, team.ID, down, reason)
	}
	return nil
}
