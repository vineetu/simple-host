package teams

import (
	"context"
	"database/sql"

	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// findingWrite refuses a team-finding write outside team building or with
// team finding off.
func findingWrite(ev model.Event) error {
	if err := participantStage(ev.Stage); err != nil {
		return err
	}
	if !ev.TeamFinding {
		return ErrFindingOff
	}
	return nil
}

// SetTeamFinding is the deprecated finding-note write: the caller's team,
// admin only, before its deadline.
func (s *Service) SetTeamFinding(ctx context.Context, ev model.Event, caller model.EventMember, userID, rawNote string) error {
	if err := findingWrite(ev); err != nil {
		return err
	}
	if !caller.TeamID.Valid {
		return errEventNotFound()
	}
	note, err := checkLine(eventapp.StripInvisible(rawNote), "looking_for", 0, 120)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if err = setFindingNote(ctx, tx, ev.ID, userID, note); err != nil {
		return err
	}
	return commit(tx)
}

// setFindingNote rechecks membership and admin under the event lock, then
// takes the same team-row lock and frozen check as PatchTeam (this
// deprecated alias writes the same column) before writing the note.
func setFindingNote(ctx context.Context, tx *sql.Tx, eventID, userID, note string) error {
	if err := teamrepo.LockFindingEvent(ctx, tx, eventID); err != nil {
		return err
	}
	var team string
	var admin bool
	if err := teamrepo.LockFindingMembership(ctx, tx, eventID, userID).Scan(&team, &admin); err != nil {
		return err
	}
	if !admin {
		return teamrepo.ErrHackTeamAdminOnly
	}
	if _, err := teamrepo.LockFindingTeam(ctx, tx, team); err != nil {
		return err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, team, false)
	if err != nil {
		return err
	}
	if st.Frozen() {
		return teamrepo.ErrHackTeamFrozen
	}
	_, err = teamrepo.SetFindingNote(ctx, tx, team, note)
	return err
}

func (s *Service) SetLooking(ctx context.Context, ev model.Event, caller model.EventMember, userID string, looking bool, rawIntro string) error {
	if err := findingWrite(ev); err != nil {
		return err
	}
	if caller.Role != "participant" {
		return errEventNotFound()
	}
	intro, err := checkLine(rawIntro, "intro", 0, 120)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if err = teamrepo.LockFindingEvent(ctx, tx, ev.ID); err != nil {
		return err
	}
	res, err := teamrepo.SetMemberLooking(ctx, tx, ev.ID, userID, looking, intro)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return teamrepo.ErrHackFindingForbidden
	}
	return commit(tx)
}

// CreateTeamRequest asks to join teamSlug, or, with teamSlug empty,
// invites invitee to the caller's own team. It returns the request state
// ("created" for a new one).
func (s *Service) CreateTeamRequest(ctx context.Context, ev model.Event, caller model.EventMember, actorID, teamSlug, invitee string) (string, error) {
	if err := findingWrite(ev); err != nil {
		return "", err
	}
	team, userID, kind, err := s.requestTarget(ctx, caller, actorID, teamSlug, invitee)
	if err != nil {
		return "", err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", internalError(err)
	}
	defer tx.Rollback()
	if err = teamrepo.LockFindingEvent(ctx, tx, ev.ID); err != nil {
		return "", err
	}
	state, err := teamrepo.NewTeamRequest(ctx, tx, ev, actorID, team, userID, kind)
	if err == nil {
		err = noticeTeamRequest(ctx, tx, ev.ID, team, userID, kind, state)
	}
	if err == nil && state == "joined" {
		err = eventapp.SyncAccess(ctx, tx, ev.ID)
	}
	if err != nil {
		return "", err
	}
	return state, commit(tx)
}

// requestTarget is the team, person and kind of a new request: the caller
// asking to join teamSlug, or (teamSlug empty) the caller inviting
// invitee to their own team.
func (s *Service) requestTarget(ctx context.Context, caller model.EventMember, actorID, teamSlug, invitee string) (string, string, string, error) {
	if teamSlug != "" {
		return teamSlug, actorID, "request", nil
	}
	if !isID(invitee) || !caller.TeamID.Valid {
		return "", "", "", errEventNotFound()
	}
	team, err := teamrepo.GetEventTeamByID(ctx, s.db, caller.TeamID.String)
	if err != nil {
		return "", "", "", internalError(err)
	}
	return team.Slug, invitee, "invite", nil
}

var resolveActions = map[string]bool{"accept": true, "decline": true, "withdraw": true}

// ResolveTeamRequest accepts, declines or withdraws request id.
func (s *Service) ResolveTeamRequest(ctx context.Context, ev model.Event, actorID, id, action string) error {
	if err := findingWrite(ev); err != nil {
		return err
	}
	if !isID(id) || !resolveActions[action] {
		return errEventNotFound()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if err = resolveRequest(ctx, tx, ev, actorID, id, action); err != nil {
		return err
	}
	if err = eventapp.SyncAccess(ctx, tx, ev.ID); err != nil {
		return err
	}
	return commit(tx)
}

// resolveRequest resolves the request under the event lock and, when a
// team's admin answered someone's request to join, tells that person. An
// invite's answer and a withdrawal are the actor's own news.
func resolveRequest(ctx context.Context, tx *sql.Tx, ev model.Event, actorID, id, action string) error {
	if err := teamrepo.LockFindingEvent(ctx, tx, ev.ID); err != nil {
		return err
	}
	// Read before it is resolved: resolving deletes the row.
	var teamID, userID, kind string
	if err := teamrepo.ReadTeamRequest(ctx, tx, ev.ID, id).Scan(&teamID, &userID, &kind); err != nil {
		return err
	}
	if err := teamrepo.ResolveTeamRequest(ctx, tx, ev, actorID, id, action); err != nil {
		return err
	}
	if kind != "request" || action == "withdraw" {
		return nil
	}
	detail := "declined"
	if action == "accept" {
		detail = "accepted"
	}
	return eventrepo.NotifyUsers(ctx, tx, ev.ID, eventrepo.NoticeRequestAnswered, teamID, "", detail, userID)
}

// noticeTeamRequest queues the notice for a new request (to admins), invite (to the invitee) or completed invite (accepted).
func noticeTeamRequest(ctx context.Context, tx *sql.Tx, eventID, teamSlug, userID, kind, state string) error {
	if state != "created" && !(state == "joined" && kind == "invite") {
		return nil
	}
	var teamID, requestID string
	if err := teamrepo.TeamIDBySlug(ctx, tx, eventID, teamSlug).Scan(&teamID); err != nil {
		return err
	}
	if state == "joined" {
		return eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeRequestAnswered, teamID, "", "accepted", userID)
	}
	if err := teamrepo.TeamRequestID(ctx, tx, teamID, userID).Scan(&requestID); err != nil {
		return err
	}
	if kind == "request" {
		return eventrepo.NotifyTeamAdmins(ctx, tx, eventID, teamID, eventrepo.NoticeJoinRequest, requestID, "")
	}
	return eventrepo.NotifyUsers(ctx, tx, eventID, eventrepo.NoticeInvite, teamID, requestID, "", userID)
}
