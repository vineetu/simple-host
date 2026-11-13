package teams

import (
	"context"
	"database/sql"

	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackteams"
)

// SetTeamFinding performs the team finding workflow in one transaction.
func (s *Service) SetTeamFinding(ctx context.Context, eventID, userID, note string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if err = teamrepo.LockFindingEvent(ctx, tx, eventID); err == nil {
		// Membership is rechecked while locked, before editing the team.
		var team string
		var admin bool
		err = teamrepo.LockFindingMembership(ctx, tx, eventID, userID).Scan(&team, &admin)
		if err == nil && !admin {
			err = teamrepo.ErrHackTeamAdminOnly
		}
		// Same team-row lock and frozen-team check as PatchTeam (Grok
		// security review, 2026-11-01): this deprecated alias writes the
		// same column and deserves the same guard, not a looser one just
		// because it predates PatchTeam.
		if err == nil {
			_, err = teamrepo.LockFindingTeam(ctx, tx, team)
		}
		if err == nil {
			if st, serr := teamrepo.TeamWriteStateFor(ctx, tx, team, false); serr != nil {
				err = serr
			} else if st.Frozen() {
				err = teamrepo.ErrHackTeamFrozen
			}
		}
		if err == nil {
			_, err = teamrepo.SetFindingNote(ctx, tx, team, note)
		}
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// SetLooking performs the team finding workflow in one transaction.
func (s *Service) SetLooking(ctx context.Context, eventID, userID string, looking bool, intro string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if err = teamrepo.LockFindingEvent(ctx, tx, eventID); err == nil {
		var res sql.Result
		res, err = teamrepo.SetMemberLooking(ctx, tx, eventID, userID, looking, intro)
		if err == nil {
			n, _ := res.RowsAffected()
			if n == 0 {
				err = teamrepo.ErrHackFindingForbidden
			}
		}
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// CreateTeamRequest performs the team finding workflow in one transaction.
func (s *Service) CreateTeamRequest(ctx context.Context, event model.Event, actorID, team, userID, kind string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", internalError(err)
	}
	defer tx.Rollback()
	state := ""
	if err = teamrepo.LockFindingEvent(ctx, tx, event.ID); err == nil {
		state, err = teamrepo.NewTeamRequest(ctx, tx, event, actorID, team, userID, kind)
	}
	if err == nil {
		err = noticeTeamRequest(ctx, tx, event.ID, team, userID, kind, state)
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", internalError(err)
	}
	return state, nil
}

// ResolveTeamRequest performs the team finding workflow in one transaction.
func (s *Service) ResolveTeamRequest(ctx context.Context, event model.Event, actorID, id, action string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	var answered struct{ teamID, userID, kind string }
	if err = teamrepo.LockFindingEvent(ctx, tx, event.ID); err == nil {
		// Read before it is resolved: resolving deletes the row.
		err = teamrepo.ReadTeamRequest(ctx, tx,
			event.ID, id).Scan(&answered.teamID, &answered.userID, &answered.kind)
		if err == nil {
			err = teamrepo.ResolveTeamRequest(ctx, tx, event, actorID, id, action)
		}
	}
	// A team's admin answered someone's request to join: tell that person.
	// An invite's answer and a withdrawal are the actor's own news.
	if err == nil && answered.kind == "request" && action != "withdraw" {
		detail := "declined"
		if action == "accept" {
			detail = "accepted"
		}
		err = eventrepo.NotifyUsers(ctx, tx, event.ID, eventrepo.NoticeRequestAnswered, answered.teamID, "", detail, answered.userID)
	}
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// noticeTeamRequest queues the notice for a new request or invite, inside
// the transaction that made it and after its event, member and team locks: a
// request reaches the team's admins, an invite the invited person. When the
// call instead completed an earlier offer ("joined"), a teammate's invite
// answered someone's waiting request, so that person hears they're in.
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

// FillTeams plans or applies the organiser's team placements under the same locks.
func (s *Service) FillTeams(ctx context.Context, eventID, userID string, requestedSize *int, apply bool) ([]model.FillPlacement, []model.FillSkippedTeam, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	defer tx.Rollback()
	// Fill teams does not require team finding. Lock the event and recheck
	// stage, then lock all prospective members before teams, as joining does.
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	if !hackteams.ParticipantTeamStages(ev.Stage) {
		return nil, nil, 0, &Error{Code: "teams_locked"}
	}
	// Default to min(4, team_size_max), avoiding a 20-person team of strangers.
	size := 4
	if ev.TeamSizeMax < size {
		size = ev.TeamSizeMax
	}
	if requestedSize != nil {
		size = *requestedSize
	}
	if size < 1 || size > ev.TeamSizeMax {
		return nil, nil, 0, &Error{Code: "invalid_team_size"}
	}
	if _, err = teamrepo.LockEventMembersForFill(ctx, tx, ev.ID); err != nil {
		return nil, nil, 0, internalError(err)
	}
	if _, err = teamrepo.LockEventTeamsForFill(ctx, tx, ev.ID); err != nil {
		return nil, nil, 0, internalError(err)
	}
	plan, skipped, err := teamrepo.PlanFillTeams(ctx, tx, ev, size)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	if apply {
		for _, p := range plan {
			if p.New {
				code, e := hackevents.RandomCode(teamCodeLength)
				if e != nil {
					return nil, nil, 0, internalError(e)
				}
				if _, err = teamrepo.InsertEventTeam(ctx, tx, ev.ID, p.Team, p.Name, code, userID); err != nil {
					return nil, nil, 0, internalError(err)
				}
				// event_teams.open is no longer written (deprecated 2026-10-31).
				if err = teamrepo.RecordUsedTeamName(ctx, tx, ev.Slug, p.Team); err != nil {
					return nil, nil, 0, internalError(err)
				}
			}
			for _, m := range p.Members {
				// MoveParticipantToTeam ensures the first person placed is an admin.
				if _, err = teamrepo.MoveParticipantToTeam(ctx, tx, ev.ID, m.UserID, p.Team, ev.TeamSizeMax, teamrepo.EnsureTeamAdmin); err != nil {
					return nil, nil, 0, err
				}
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, 0, internalError(err)
	}
	return plan, skipped, size, nil
}
