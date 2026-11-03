package teams

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/db"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func isUnique(err error) bool { return hackevents.UniqueError(err) }

// rotateTeamCode stops removed members rejoining with the old code. An empty,
// deleted team has nothing to rotate.
func rotateTeamCode(ctx context.Context, q db.Querier, teamID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := hackevents.RandomCode(teamCodeLength)
		if err != nil {
			return err
		}
		err = trySavepoint(ctx, q, func() error {
			_, err := teamrepo.SetTeamCode(ctx, q, teamID, code)
			return err
		})
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
	// Lock the event first, matching self-leave. Removing a judge locks the
	// member then inserts a former-judge row, whose foreign key takes an event
	// key-share lock. Self-leave can hold the event update lock while waiting
	// for that member, causing a deadlock unless this locks the event first.
	if _, err = eventrepo.GetEventForShare(ctx, tx, eventID); err != nil {
		return internalError(err)
	}
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	if err = eventrepo.RemovePersonFromEvent(ctx, tx, eventID, userID, eventLeaveOperations()); err != nil {
		return err
	}
	if oldTeam != "" {
		// Empty teams are already deleted; rotate only a remaining team.
		var still bool
		if err = teamrepo.TeamRowExists(ctx, tx, oldTeam).Scan(&still); err == nil && still {
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
	team, err := teamrepo.MoveParticipantToTeam(ctx, tx, eventID, userID, slug, max, db.EnsureTeamAdmin)
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
		// Removal no longer requires team finding. Recheck admin authority under
		// locks: both member rows sorted by user ID, then the team row.
		if _, err = teamrepo.LockRemovalMembers(ctx, tx, eventID, actorID, userID); err != nil {
			return internalError(err)
		}
		var teamID string
		var admin bool
		if err = teamrepo.ReadRemovalActor(ctx, tx, eventID, actorID).Scan(&teamID, &admin); err != nil {
			return sql.ErrNoRows
		}
		if !admin {
			return teamrepo.ErrHackTeamAdminOnly
		}
		var gotSlug string
		if err = teamrepo.LockTeamSlug(ctx, tx, teamID).Scan(&gotSlug); err != nil || gotSlug != slug {
			return sql.ErrNoRows
		}
		st, err := db.TeamWriteStateFor(ctx, tx, teamID, true)
		if err != nil {
			return internalError(err)
		}
		if st.Frozen() {
			return teamrepo.ErrHackTeamFrozen
		}
	}
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	if err = teamrepo.RemoveParticipantFromTeam(ctx, tx, eventID, slug, userID, db.EnsureTeamAdmin); err != nil {
		return err
	}
	if participant {
		_, err = teamrepo.SetMemberRemovalNotice(ctx, tx, eventID, userID, "A teammate took you off "+teamName+".")
	}
	if err == nil {
		// Email uses the same words as the removal notice.
		var notice string
		if err = teamrepo.ReadMemberRemovalNotice(ctx, tx, eventID, userID).Scan(&notice); err == nil && notice != "" {
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

func (s *Service) teamDeadlinePassed(ctx context.Context, teamID string) (bool, error) {
	var passed bool
	err := teamrepo.TeamDeadlinePassed(ctx, s.db, teamID).Scan(&passed)
	return passed, err
}

// teamNameTaken checks for another team with the same name, ignoring case.
func teamNameTaken(ctx context.Context, q db.Querier, eventID, name string) (bool, error) {
	var taken bool
	err := teamrepo.TeamNameExists(ctx, q, eventID, name).Scan(&taken)
	return taken, err
}

// trySavepoint permits a retry after a unique violation, which otherwise aborts
// the whole transaction.
func trySavepoint(ctx context.Context, q db.Querier, fn func() error) error {
	if _, err := teamrepo.StartWorkflowSavepoint(ctx, q); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if _, rerr := teamrepo.RollbackWorkflowSavepoint(ctx, q); rerr != nil {
			return rerr
		}
		return err
	}
	_, err := teamrepo.ReleaseWorkflowSavepoint(ctx, q)
	return err
}

// memberTeamID locks the old membership so a concurrent move waits and the
// team actually left is the one read.
func memberTeamID(ctx context.Context, q *sql.Tx, eventID, userID string) string {
	var team sql.NullString
	if err := teamrepo.LockMemberTeamID(ctx, q, eventID, userID).Scan(&team); err != nil || !team.Valid {
		return ""
	}
	return team.String
}

func eventLeaveOperations() eventrepo.LeaveOperations {
	return eventrepo.LeaveOperations{LockMember: teamrepo.LockMember, LockTeam: teamrepo.LockTeam, DeleteTeamIfEmpty: teamrepo.DeleteTeamIfEmpty, EnsureTeamAdmin: db.EnsureTeamAdmin}
}
