package teams

// TODO(4.2): move inline SQL to repository/hackteams.
import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/db"
)

// OrganiserLock sets a team's lock and returns cleared request counts.
func (s *Service) OrganiserLock(ctx context.Context, eventID, slug, actor string, locked bool) (int, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, internalError(err)
	}
	defer tx.Rollback()
	team, err := db.GetEventTeamBySlug(ctx, tx, eventID, slug)
	if err != nil {
		return 0, 0, err
	}
	requests, invites, err := db.SetTeamLock(ctx, tx, team.ID, actor, locked)
	if err == sql.ErrNoRows {
		return 0, 0, &Error{Code: "member_not_found"}
	}
	if err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, internalError(err)
	}
	return requests, invites, nil
}

// OrganiserAdmin changes a team member's admin flag.
func (s *Service) OrganiserAdmin(ctx context.Context, eventID, slug, userID string, admin bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	team, err := db.GetEventTeamBySlug(ctx, tx, eventID, slug)
	if err != nil {
		return err
	}
	if err = db.SetTeamAdmin(ctx, tx, eventID, team.ID, userID, admin); err != nil {
		if err == sql.ErrNoRows {
			return &Error{Code: "member_not_found"}
		}
		return err
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// OrganiserLink rotates a team's link.
func (s *Service) OrganiserLink(ctx context.Context, eventID, slug string) (db.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	team, err := db.GetEventTeamBySlug(ctx, tx, eventID, slug)
	if err != nil {
		return team, err
	}
	if err = rotateTeamCode(ctx, tx, team.ID); err != nil {
		return team, internalError(err)
	}
	fresh, err := db.GetEventTeamByID(ctx, tx, team.ID)
	if err != nil {
		return team, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return fresh, nil
}

func participantTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (db.EventTeam, error) {
	team, admin, stage, err := LockCallerTeam(ctx, tx, eventID, userID)
	if err != nil {
		return team, err
	}
	if stage != "open" && stage != "building" {
		return team, &Error{Code: "teams_locked"}
	}
	if !admin {
		return team, db.ErrHackTeamAdminOnly
	}
	return team, nil
}
func checkFrozen(ctx context.Context, tx *sql.Tx, teamID string) error {
	st, err := db.TeamWriteStateFor(ctx, tx, teamID, false)
	if err != nil {
		return internalError(err)
	}
	if st.Frozen() {
		return &Error{Code: "submissions_closed"}
	}
	return nil
}

// PatchTeam changes the caller's team name or finding note.
func (s *Service) PatchTeam(ctx context.Context, eventID, userID string, name, note *string) (db.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	team, err := participantTeam(ctx, tx, eventID, userID)
	if err != nil {
		return team, err
	}
	if name != nil || note != nil {
		if err = checkFrozen(ctx, tx, team.ID); err != nil {
			return team, err
		}
	}
	if name != nil {
		if err = db.RenameEventTeam(ctx, tx, eventID, team.Slug, *name); err != nil {
			if isUnique(err) {
				return team, &Error{Code: "team_name_taken"}
			}
			return team, internalError(err)
		}
	}
	if note != nil {
		if _, err = tx.ExecContext(ctx, `UPDATE event_teams SET looking_for = $2 WHERE id = $1`, team.ID, *note); err != nil {
			return team, internalError(err)
		}
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return team, nil
}

// ParticipantLock sets the caller's team lock.
func (s *Service) ParticipantLock(ctx context.Context, eventID, userID string, locked bool) (int, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, internalError(err)
	}
	defer tx.Rollback()
	team, err := participantTeam(ctx, tx, eventID, userID)
	if err != nil {
		return 0, 0, err
	}
	if err = checkFrozen(ctx, tx, team.ID); err != nil {
		return 0, 0, err
	}
	requests, invites, err := db.SetTeamLock(ctx, tx, team.ID, userID, locked)
	if err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, internalError(err)
	}
	return requests, invites, nil
}

// ParticipantAdmin sets a teammate's admin flag under sorted member locks.
func (s *Service) ParticipantAdmin(ctx context.Context, eventID, userID, target string, admin bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return internalError(err)
	}
	defer tx.Rollback()
	teamID, callerAdmin, stage, err := LockCallerAndTargetTeam(ctx, tx, eventID, userID, target)
	if err != nil {
		return err
	}
	if stage != "open" && stage != "building" {
		return &Error{Code: "teams_locked"}
	}
	if !callerAdmin {
		return db.ErrHackTeamAdminOnly
	}
	if err = checkFrozen(ctx, tx, teamID); err != nil {
		return err
	}
	if err = db.SetTeamAdmin(ctx, tx, eventID, teamID, target, admin); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// ParticipantLink rotates the caller's team link.
func (s *Service) ParticipantLink(ctx context.Context, eventID, userID string) (db.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return db.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	team, err := participantTeam(ctx, tx, eventID, userID)
	if err != nil {
		return team, err
	}
	if err = checkFrozen(ctx, tx, team.ID); err != nil {
		return team, err
	}
	if err = rotateTeamCode(ctx, tx, team.ID); err != nil {
		return team, internalError(err)
	}
	fresh, err := db.GetEventTeamByID(ctx, tx, team.ID)
	if err != nil {
		return team, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return fresh, nil
}
