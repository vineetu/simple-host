package sites

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

// LockSiteTakedown locks the site row used for an organiser takedown.
func LockSiteTakedown(ctx context.Context, tx *sql.Tx, accountID, teamSlug string) (siteID, reason string, err error) {
	err = tx.QueryRowContext(ctx, `
		SELECT id, COALESCE(suspended_reason, '') FROM sites
		 WHERE user_id = $1 AND name = $2 AND deleted_at IS NULL FOR UPDATE`, accountID, teamSlug).Scan(&siteID, &reason)
	return
}
