package teams

import (
	"context"
	"database/sql"

	model "github.com/vsriram/simple-host/internal/model"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackteams"
)

// OrganiserLock sets a team's lock and returns cleared request counts.
func (s *Service) OrganiserLock(ctx context.Context, eventID, slug, actor string, locked bool) (int, int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, internalError(err)
	}
	defer tx.Rollback()
	team, err := teamrepo.GetEventTeamBySlug(ctx, tx, eventID, slug)
	if err != nil {
		return 0, 0, err
	}
	requests, invites, err := teamrepo.SetTeamLock(ctx, tx, team.ID, actor, locked)
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
	team, err := teamrepo.GetEventTeamBySlug(ctx, tx, eventID, slug)
	if err != nil {
		return err
	}
	if err = teamrepo.SetTeamAdmin(ctx, tx, eventID, team.ID, userID, admin); err != nil {
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
func (s *Service) OrganiserLink(ctx context.Context, eventID, slug string) (model.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
	}
	defer tx.Rollback()
	team, err := teamrepo.GetEventTeamBySlug(ctx, tx, eventID, slug)
	if err != nil {
		return team, err
	}
	if err = rotateTeamCode(ctx, tx, team.ID); err != nil {
		return team, internalError(err)
	}
	fresh, err := teamrepo.GetEventTeamByID(ctx, tx, team.ID)
	if err != nil {
		return team, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return fresh, nil
}

func participantTeam(ctx context.Context, tx *sql.Tx, eventID, userID string) (model.EventTeam, error) {
	team, admin, stage, err := LockCallerTeam(ctx, tx, eventID, userID)
	if err != nil {
		return team, err
	}
	// Recheck under the event lock; the handler's pre-BEGIN check only
	// rejects obviously invalid requests early.
	if !hackteams.ParticipantTeamStages(stage) {
		return team, &Error{Code: "teams_locked"}
	}
	if !admin {
		return team, teamrepo.ErrHackTeamAdminOnly
	}
	return team, nil
}
func checkFrozen(ctx context.Context, tx *sql.Tx, teamID string) error {
	st, err := teamrepo.TeamWriteStateFor(ctx, tx, teamID, false)
	if err != nil {
		return internalError(err)
	}
	if st.Frozen() {
		return &Error{Code: "submissions_closed"}
	}
	return nil
}

// PatchTeam changes the caller's team name or finding note.
func (s *Service) PatchTeam(ctx context.Context, eventID, userID string, name, note *string) (model.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
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
		if err = eventrepo.RenameEventTeam(ctx, tx, eventID, team.Slug, *name); err != nil {
			if isUnique(err) {
				return team, &Error{Code: "team_name_taken"}
			}
			return team, internalError(err)
		}
	}
	if note != nil {
		if _, err = teamrepo.SetTeamLookingFor(ctx, tx, team.ID, *note); err != nil {
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
	requests, invites, err := teamrepo.SetTeamLock(ctx, tx, team.ID, userID, locked)
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
	if !hackteams.ParticipantTeamStages(stage) {
		return &Error{Code: "teams_locked"}
	}
	if !callerAdmin {
		return teamrepo.ErrHackTeamAdminOnly
	}
	if err = checkFrozen(ctx, tx, teamID); err != nil {
		return err
	}
	if err = teamrepo.SetTeamAdmin(ctx, tx, eventID, teamID, target, admin); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return internalError(err)
	}
	return nil
}

// ParticipantLink rotates the caller's team link.
func (s *Service) ParticipantLink(ctx context.Context, eventID, userID string) (model.EventTeam, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.EventTeam{}, internalError(err)
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
	fresh, err := teamrepo.GetEventTeamByID(ctx, tx, team.ID)
	if err != nil {
		return team, internalError(err)
	}
	if err = tx.Commit(); err != nil {
		return team, internalError(err)
	}
	return fresh, nil
}
