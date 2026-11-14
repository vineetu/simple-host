package hackteams

import (
	"context"
	"database/sql"
)

// LockEventOrganiser reads and locks an event and its organiser membership.
func LockEventOrganiser(ctx context.Context, tx *sql.Tx, eventID, slug, accountID, organiserID string) (role, stage string, takenDown bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT m.role, e.stage, e.taken_down_at IS NOT NULL
			FROM events e JOIN event_members m ON m.event_id = e.id
			WHERE e.id = $1 AND e.slug = $2 AND e.account_id = $3 AND m.user_id = $4
			FOR SHARE OF e, m`, eventID, slug, accountID, organiserID).Scan(&role, &stage, &takenDown)
	return
}

// LockApprovedTeamMember locks an approved participant's membership.
func LockApprovedTeamMember(ctx context.Context, tx *sql.Tx, memberID, teamID string) error {
	var found string
	return tx.QueryRowContext(ctx, `SELECT user_id FROM event_members WHERE user_id = $1 AND team_id = $2 AND role = 'participant' AND approval_status = 'approved' FOR SHARE`, memberID, teamID).Scan(&found)
}

// SetTeamSiteTakedown records or clears the team's site takedown.
func SetTeamSiteTakedown(ctx context.Context, tx *sql.Tx, teamID string, on bool, reason string) error {
	var err error
	if on {
		_, err = tx.ExecContext(ctx, `UPDATE event_teams SET site_taken_down_at = COALESCE(site_taken_down_at, now()), site_taken_down_reason = $2 WHERE id = $1`, teamID, reason)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE event_teams SET site_taken_down_at = NULL, site_taken_down_reason = '' WHERE id = $1`, teamID)
	}
	return err
}
