package teams

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackevents"
)

func isUnique(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "23505") || strings.Contains(strings.ToLower(err.Error()), "unique"))
}

// rotateTeamCode stops removed members rejoining with the old code. An empty,
// deleted team has nothing to rotate.
func rotateTeamCode(ctx context.Context, q db.Querier, teamID string) error {
	for attempt := 0; attempt < 8; attempt++ {
		code, err := hackevents.RandomCode(teamCodeLength)
		if err != nil {
			return err
		}
		err = trySavepoint(ctx, q, func() error {
			_, err := q.ExecContext(ctx, organiserSQL2, teamID, code)
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
	if _, err = db.GetEventForShare(ctx, tx, eventID); err != nil {
		return internalError(err)
	}
	oldTeam := memberTeamID(ctx, tx, eventID, userID)
	if err = db.RemovePersonFromEvent(ctx, tx, eventID, userID); err != nil {
		return err
	}
	if oldTeam != "" {
		// Empty teams are already deleted; rotate only a remaining team.
		var still bool
		if err = tx.QueryRowContext(ctx, organiserSQL3, oldTeam).Scan(&still); err == nil && still {
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
		// Removal no longer requires team finding. Recheck admin authority under
		// locks: both member rows sorted by user ID, then the team row.
		if _, err = tx.ExecContext(ctx, organiserSQL4, eventID, actorID, userID); err != nil {
			return internalError(err)
		}
		var teamID string
		var admin bool
		if err = tx.QueryRowContext(ctx, organiserSQL5, eventID, actorID).Scan(&teamID, &admin); err != nil {
			return sql.ErrNoRows
		}
		if !admin {
			return db.ErrHackTeamAdminOnly
		}
		var gotSlug string
		if err = tx.QueryRowContext(ctx, organiserSQL6, teamID).Scan(&gotSlug); err != nil || gotSlug != slug {
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
		_, err = tx.ExecContext(ctx, organiserSQL7, eventID, userID, "A teammate took you off "+teamName+".")
	}
	if err == nil {
		// Email uses the same words as the removal notice.
		var notice string
		if err = tx.QueryRowContext(ctx, organiserSQL8, eventID, userID).Scan(&notice); err == nil && notice != "" {
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
