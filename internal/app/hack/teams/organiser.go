package teams

// TODO(4.2): move inline SQL to repository/hackteams.
import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func memberTeamID(ctx context.Context, q db.Querier, eventID, userID string) string {
	var team sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`, eventID, userID).Scan(&team); err != nil || !team.Valid {
		return ""
	}
	return team.String
}

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "23505") || strings.Contains(strings.ToLower(err.Error()), "unique"))
}

func rotateTeamCode(ctx context.Context, q db.Querier, teamID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := hackevents.RandomCode(8)
		if err != nil {
			return err
		}
		if _, err = q.ExecContext(ctx, "SAVEPOINT hack_try"); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE event_teams SET code = $2 WHERE id = $1`, teamID, code)
		if err != nil {
			if _, rollbackErr := q.ExecContext(ctx, "ROLLBACK TO SAVEPOINT hack_try"); rollbackErr != nil {
				return rollbackErr
			}
		} else {
			_, err = q.ExecContext(ctx, "RELEASE SAVEPOINT hack_try")
		}
		if err == nil || !isUnique(err) {
			return err
		}
	}
	return errors.New("could not find a free team code")
}

// RemovePerson removes a member and rotates the remaining team's code atomically.
func (s *Service) RemovePerson(ctx context.Context, eventID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	// Lock the event before the member, matching self-leave's lock order.
	if _, err = db.GetEventForShare(ctx, tx, eventID); err != nil {
		return internalError(err)
	}
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	if err = db.RemovePersonFromEvent(ctx, tx, eventID, userID); err != nil {
		return err
	}
	if oldTeam != "" {
		var still bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`, oldTeam).Scan(&still); err == nil && still {
			err = rotateTeamCode(ctx, tx, oldTeam)
		}
		if err != nil {
			return internalError(err)
		}
	}
	if err = db.RevokeStaleTeamKeys(ctx, tx, eventID); err != nil {
		return internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// MoveMember moves a participant and rotates the old team's code atomically.
func (s *Service) MoveMember(ctx context.Context, eventID, userID, slug string, max int) (db.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	team, err := db.MoveParticipantToTeam(ctx, tx, eventID, userID, slug, max)
	if err != nil {
		return team, err
	}
	if oldTeam != "" && oldTeam != team.ID {
		if err = rotateTeamCode(ctx, tx, oldTeam); err != nil {
			return team, internalError(err)
		}
	}
	if err = db.RevokeStaleTeamKeys(ctx, tx, eventID); err != nil {
		return team, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return team, nil
}

// RemoveTeamMember removes a member, sends the notice and rotates the team code.
func (s *Service) RemoveTeamMember(ctx context.Context, eventID, actorID, userID, slug, teamName string, participant bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	if participant {
		if _, err = tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id=$1 AND user_id IN ($2,$3) ORDER BY user_id FOR UPDATE`, eventID, actorID, userID); err != nil {
			return internalError(err)
		}
		var teamID string
		var admin bool
		if err = tx.QueryRowContext(ctx, `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved'`, eventID, actorID).Scan(&teamID, &admin); err != nil {
			return sql.ErrNoRows
		}
		if !admin {
			return db.ErrHackTeamAdminOnly
		}
		var gotSlug string
		if err = tx.QueryRowContext(ctx, `SELECT slug FROM event_teams WHERE id=$1 FOR UPDATE`, teamID).Scan(&gotSlug); err != nil || gotSlug != slug {
			return sql.ErrNoRows
		}
		st, err := db.TeamWriteStateFor(ctx, tx, teamID, true)
		if err != nil {
			return internalError(err)
		}
		if st.Frozen() {
			return db.ErrHackTeamFrozen
		}
	}
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	if err = db.RemoveParticipantFromTeam(ctx, tx, eventID, slug, userID); err != nil {
		return err
	}
	if participant {
		_, err = tx.ExecContext(ctx, `UPDATE event_members SET team_removal_notice=$3 WHERE event_id=$1 AND user_id=$2`, eventID, userID, "A teammate took you off "+teamName+".")
	}
	if err == nil {
		var notice string
		if err = tx.QueryRowContext(ctx, `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`, eventID, userID).Scan(&notice); err == nil && notice != "" {
			err = db.NotifyUsers(ctx, tx, eventID, db.NoticeTeamRemoved, oldTeam, "", notice, userID)
		}
	}
	if err == nil && oldTeam != "" {
		err = rotateTeamCode(ctx, tx, oldTeam)
	}
	if err != nil {
		return internalError(err)
	}
	if err = db.RevokeStaleTeamKeys(ctx, tx, eventID); err != nil {
		return internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}
