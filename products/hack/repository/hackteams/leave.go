package hackteams

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

// LeaveMembership is the shared core of a person losing their event membership, whether they chose it.
func LeaveMembership(ctx context.Context, q sqlkit.Querier, eventID, userID string, selfLeave bool) (string, error) {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return "", err
	}
	if m.TeamID.Valid {
		if _, err := LockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if err := hackevents.DropMembership(ctx, q, eventID, userID); err != nil || !m.TeamID.Valid {
		return "", err
	}
	team := m.TeamID.String
	if err := DeleteTeamIfEmpty(ctx, q, team); err != nil {
		return "", err
	}
	if selfLeave {
		if err := hackevents.MarkTeamKeptAfterLeave(ctx, q, team); err != nil {
			return "", err
		}
	}
	if err := EnsureTeamAdmin(ctx, q, team); err != nil {
		return "", err
	}
	return team, nil
}

func RemoveCoOrganiser(ctx context.Context, q *sql.Tx, eventID, actorID, userID string) error {
	// Serialise removals so a legacy event without its creator cannot lose both remaining organisers through concurrent requests.
	var createdBy sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT created_by FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&createdBy); err != nil {
		return err
	}
	var role string
	err := q.QueryRowContext(ctx, `SELECT role FROM event_members WHERE event_id=$1 AND user_id=$2 FOR UPDATE`, eventID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if role != "organiser" || (createdBy.Valid && createdBy.String == userID) {
		return sql.ErrNoRows
	}
	var actorIsOrganiser, anotherOrganiser bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND user_id=$2 AND role='organiser'),
		       EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND role='organiser' AND user_id<>$3)`,
		eventID, actorID, userID).Scan(&actorIsOrganiser, &anotherOrganiser); err != nil {
		return err
	}
	if !actorIsOrganiser || !anotherOrganiser {
		return sql.ErrNoRows
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, userID); err != nil {
		return err
	}
	_, err = LeaveMembership(ctx, q, eventID, userID, false)
	return err
}

func RemovePersonFromEvent(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	m, err := LockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if m.Role == "organiser" {
		return hackevents.ErrHackCannotRemoveOrganiser
	}
	if m.Role == "judge" {
		if err := hackevents.RecordFormerJudge(ctx, q, eventID, userID, m.Role); err != nil {
			return err
		}
	}
	_, err = LeaveMembership(ctx, q, eventID, userID, false)
	return err
}
