package teams

// TODO(4.2): move inline SQL to repository/hackteams.
import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
)

// LeaveTeam removes a participant and revokes stale keys atomically.
func (s *Service) LeaveTeam(ctx context.Context, eventID, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = db.LeaveTeam(ctx, tx, eventID, userID); err != nil {
		return err
	}
	if err = db.RevokeStaleTeamKeys(ctx, tx, eventID); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = tx.Commit(); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	return nil
}

// DeleteTeam deletes a team and revokes its keys and queues member notices.
func (s *Service) DeleteTeam(ctx context.Context, eventID, slug string) error {
	team, err := db.GetEventTeamBySlug(ctx, s.db, eventID, slug)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE team_id = $1)`, team.ID); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE event_members SET team_removal_notice=$2 WHERE team_id=$1`, team.ID, team.Name+" was removed by the organiser."); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = db.NotifyTeamMembers(ctx, tx, eventID, team.ID, db.NoticeTeamRemoved, team.Name+" was removed by the organiser.", ""); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = db.DeleteEventTeam(ctx, tx, team.ID); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	if err = tx.Commit(); err != nil {
		return &Error{Code: "internal", Message: err.Error()}
	}
	return nil
}

// JoinTeam joins a team under the event lock and checks its deadline.
func (s *Service) JoinTeam(ctx context.Context, eventID, userID, code string, max int) (db.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	stage, err := LockEventStage(ctx, tx, eventID)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	if stage != "open" && stage != "building" {
		return db.EventTeam{}, &Error{Code: "teams_locked"}
	}
	team, err := db.JoinTeamByCode(ctx, tx, eventID, userID, code, max)
	if err != nil {
		return team, err
	}
	st, err := db.TeamWriteStateFor(ctx, tx, team.ID, true)
	if err != nil {
		return team, internalError(err)
	}
	if st.Frozen() {
		return team, &Error{Code: "submissions_closed"}
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return team, nil
}
