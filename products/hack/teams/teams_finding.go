package teams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/products/hack/app/teams"

	eventapp "github.com/vsriram/simple-host/products/hack/app/events"
	model "github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// findingWrite refuses a team-finding write outside team building or with
// team finding off.
func findingWrite(ev model.Event) error {
	if err := teams.ParticipantStage(ev.Stage); err != nil {
		return err
	}
	if !ev.TeamFinding {
		return teams.ErrFindingOff
	}
	return nil
}

// SetTeamFinding is the deprecated finding-note write: the caller's team,
// admin only, before its deadline.
func (h *handler) SetTeamFinding(ctx context.Context, ev model.Event, caller model.EventMember, userID, rawNote string) error {
	if err := findingWrite(ev); err != nil {
		return err
	}
	if !caller.TeamID.Valid {
		return teams.ErrEventNotFound()
	}
	note, err := teams.CheckLine(eventapp.StripInvisible(rawNote), "looking_for", 0, 120)
	if err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return teams.InternalError(err)
	}
	defer tx.Rollback()
	if err = setFindingNote(ctx, tx, ev.ID, userID, note); err != nil {
		return err
	}
	return teams.Commit(tx)
}

// setFindingNote rechecks membership and admin under the event lock, then
// takes the same team-row lock and frozen check as PatchTeam (this
// deprecated alias writes the same column) before writing the note.
func setFindingNote(ctx context.Context, tx *sql.Tx, eventID, userID, note string) error {
	if err := teamrepo.LockFindingEvent(ctx, tx, eventID); err != nil {
		return err
	}
	m, err := hackmembers.Member(ctx, tx, eventID, userID, hackmembers.ForUpdate)
	if err == nil && !m.Approved() {
		err = sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if !m.TeamID.Valid {
		return errors.New("team finding note: caller is on no team")
	}
	team, admin := m.TeamID.String, m.TeamAdmin
	if !admin {
		return teamrepo.ErrHackTeamAdminOnly
	}
	if err := teamrepo.LockTeamRow(ctx, tx, team); err != nil {
		return err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, eventID, team, false)
	if err != nil {
		return err
	}
	if st.Frozen() {
		return teamrepo.ErrHackTeamFrozen
	}
	_, err = teamrepo.SetTeamLookingFor(ctx, tx, team, note)
	return err
}

func (h *handler) SetLooking(ctx context.Context, ev model.Event, caller model.EventMember, userID string, looking bool, rawIntro string) error {
	if err := findingWrite(ev); err != nil {
		return err
	}
	if caller.Role != "participant" {
		return teams.ErrEventNotFound()
	}
	intro, err := teams.CheckLine(rawIntro, "intro", 0, 120)
	if err != nil {
		return err
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return teams.InternalError(err)
	}
	defer tx.Rollback()
	if err = teamrepo.LockFindingEvent(ctx, tx, ev.ID); err != nil {
		return err
	}
	set, err := hackmembers.SetLooking(ctx, tx, ev.ID, userID, looking, intro)
	if err != nil {
		return err
	}
	if !set {
		return teamrepo.ErrHackFindingForbidden
	}
	return teams.Commit(tx)
}

// CreateTeamRequest asks to join teamSlug, or, with teamSlug empty,
// invites invitee to the caller's own team. It returns the request state
// ("created" for a new one).
func (h *handler) CreateTeamRequest(ctx context.Context, ev model.Event, caller model.EventMember, actorID, teamSlug, invitee string) (string, error) {
	if err := findingWrite(ev); err != nil {
		return "", err
	}
	team, userID, kind, err := h.requestTarget(ctx, caller, actorID, teamSlug, invitee)
	if err != nil {
		return "", err
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", teams.InternalError(err)
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
	return state, teams.Commit(tx)
}

// requestTarget is the team, person and kind of a new request: the caller
// asking to join teamSlug, or (teamSlug empty) the caller inviting
// invitee to their own team.
func (h *handler) requestTarget(ctx context.Context, caller model.EventMember, actorID, teamSlug, invitee string) (string, string, string, error) {
	if teamSlug != "" {
		return teamSlug, actorID, "request", nil
	}
	if !teams.IsID(invitee) || !caller.TeamID.Valid {
		return "", "", "", teams.ErrEventNotFound()
	}
	team, err := teamrepo.GetEventTeamByID(ctx, h.DB, caller.TeamID.String)
	if err != nil {
		return "", "", "", teams.InternalError(err)
	}
	return team.Slug, invitee, "invite", nil
}

var resolveActions = map[string]bool{"accept": true, "decline": true, "withdraw": true}

// ResolveTeamRequest accepts, declines or withdraws request id.
func (h *handler) ResolveTeamRequest(ctx context.Context, ev model.Event, actorID, id, action string) error {
	if err := findingWrite(ev); err != nil {
		return err
	}
	if !teams.IsID(id) || !resolveActions[action] {
		return teams.ErrEventNotFound()
	}
	tx, err := h.DB.BeginTx(ctx, nil)
	if err != nil {
		return teams.InternalError(err)
	}
	defer tx.Rollback()
	if err = resolveRequest(ctx, tx, ev, actorID, id, action); err != nil {
		return err
	}
	if err = eventapp.SyncAccess(ctx, tx, ev.ID); err != nil {
		return err
	}
	return teams.Commit(tx)
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
	return hackmembers.NotifyUsers(ctx, tx, ev.ID, hackmembers.NoticeRequestAnswered, teamID, "", detail, userID)
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
		return hackmembers.NotifyUsers(ctx, tx, eventID, hackmembers.NoticeRequestAnswered, teamID, "", "accepted", userID)
	}
	if err := teamrepo.TeamRequestID(ctx, tx, teamID, userID).Scan(&requestID); err != nil {
		return err
	}
	if kind == "request" {
		return hackmembers.NotifyTeamAdmins(ctx, tx, eventID, teamID, hackmembers.NoticeJoinRequest, requestID, "")
	}
	return hackmembers.NotifyUsers(ctx, tx, eventID, hackmembers.NoticeInvite, teamID, requestID, "", userID)
}
