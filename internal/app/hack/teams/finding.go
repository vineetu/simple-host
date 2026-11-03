package teams

// TODO(4.2): move inline SQL to repository/hackteams.

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

// SetTeamFinding performs the team finding workflow in one transaction.
func (s *Service) SetTeamFinding(ctx context.Context, eventID, userID, note string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if err = db.LockFindingEvent(ctx, tx, eventID); err == nil {
		// Membership is rechecked while locked, before editing the team.
		var team string
		var admin bool
		err = tx.QueryRowContext(ctx, `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved' FOR UPDATE`, eventID, userID).Scan(&team, &admin)
		if err == nil && !admin {
			err = db.ErrHackTeamAdminOnly
		}
		// Same team-row lock and frozen-team check as patchTeam (Grok
		// security review, 2026-11-01): this deprecated alias writes the
		// same column and deserves the same guard, not a looser one just
		// because it predates patchTeam.
		if err == nil {
			_, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`, team)
		}
		if err == nil {
			if st, serr := db.TeamWriteStateFor(ctx, tx, team, false); serr != nil {
				err = serr
			} else if st.Frozen() {
				err = db.ErrHackTeamFrozen
			}
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE event_teams SET looking_for=$2 WHERE id=$1`, team, note)
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
	if err = db.LockFindingEvent(ctx, tx, eventID); err == nil {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `UPDATE event_members SET looking=$3,looking_intro=$4 WHERE event_id=$1 AND user_id=$2 AND role='participant' AND approval_status='approved' AND team_id IS NULL`, eventID, userID, looking, intro)
		if err == nil {
			n, _ := res.RowsAffected()
			if n == 0 {
				err = db.ErrHackFindingForbidden
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
func (s *Service) CreateTeamRequest(ctx context.Context, event db.Event, actorID, team, userID, kind string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", internalError(err)
	}
	defer tx.Rollback()
	state := ""
	if err = db.LockFindingEvent(ctx, tx, event.ID); err == nil {
		state, err = db.NewTeamRequest(ctx, tx, event, actorID, team, userID, kind)
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
func (s *Service) ResolveTeamRequest(ctx context.Context, event db.Event, actorID, id, action string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	var answered struct{ teamID, userID, kind string }
	if err = db.LockFindingEvent(ctx, tx, event.ID); err == nil {
		// Read before it is resolved: resolving deletes the row.
		err = tx.QueryRowContext(ctx, `SELECT team_id, user_id, kind FROM event_team_requests WHERE event_id = $1 AND id = $2`,
			event.ID, id).Scan(&answered.teamID, &answered.userID, &answered.kind)
		if err == nil {
			err = db.ResolveTeamRequest(ctx, tx, event, actorID, id, action)
		}
	}
	// A team's admin answered someone's request to join: tell that person.
	// An invite's answer and a withdrawal are the actor's own news.
	if err == nil && answered.kind == "request" && action != "withdraw" {
		detail := "declined"
		if action == "accept" {
			detail = "accepted"
		}
		err = db.NotifyUsers(ctx, tx, event.ID, db.NoticeRequestAnswered, answered.teamID, "", detail, answered.userID)
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
	if err := tx.QueryRowContext(ctx, `SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`, eventID, teamSlug).Scan(&teamID); err != nil {
		return err
	}
	if state == "joined" {
		return db.NotifyUsers(ctx, tx, eventID, db.NoticeRequestAnswered, teamID, "", "accepted", userID)
	}
	if err := tx.QueryRowContext(ctx, `SELECT id FROM event_team_requests WHERE team_id = $1 AND user_id = $2`, teamID, userID).Scan(&requestID); err != nil {
		return err
	}
	if kind == "request" {
		return db.NotifyTeamAdmins(ctx, tx, eventID, teamID, db.NoticeJoinRequest, requestID, "")
	}
	return db.NotifyUsers(ctx, tx, eventID, db.NoticeInvite, teamID, requestID, "", userID)
}

// FillTeams plans or applies the organiser's team placements under the same locks.
func (s *Service) FillTeams(ctx context.Context, eventID, userID string, requestedSize *int, apply bool) ([]db.FillPlacement, []db.FillSkippedTeam, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	if ev.Stage != "open" && ev.Stage != "building" {
		return nil, nil, 0, &Error{Code: "teams_locked"}
	}
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
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id=$1 ORDER BY user_id FOR UPDATE`, ev.ID); err != nil {
		return nil, nil, 0, internalError(err)
	}
	if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE event_id=$1 ORDER BY id FOR UPDATE`, ev.ID); err != nil {
		return nil, nil, 0, internalError(err)
	}
	plan, skipped, err := db.PlanFillTeams(ctx, tx, ev, size)
	if err != nil {
		return nil, nil, 0, internalError(err)
	}
	if apply {
		for _, p := range plan {
			if p.New {
				code, e := hackevents.RandomCode(8)
				if e != nil {
					return nil, nil, 0, internalError(e)
				}
				if _, err = db.InsertEventTeam(ctx, tx, ev.ID, p.Team, p.Name, code, userID); err != nil {
					return nil, nil, 0, internalError(err)
				}
				if err = db.RecordUsedTeamName(ctx, tx, ev.Slug, p.Team); err != nil {
					return nil, nil, 0, internalError(err)
				}
			}
			for _, m := range p.Members {
				if _, err = db.MoveParticipantToTeam(ctx, tx, ev.ID, m.UserID, p.Team, ev.TeamSizeMax); err != nil {
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
