package hackteams

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

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

// LockTeamSiteSuspension reads the team's current take-down after the caller
// locks its site row. A personal site or an event website has no team row.
func LockTeamSiteSuspension(ctx context.Context, tx *sql.Tx, accountID, name string) (down bool, reason string, err error) {
	ev, err := hackevents.GetEventByAccount(ctx, tx, accountID)
	if err != nil {
		return false, "", err
	}
	err = tx.QueryRowContext(ctx, `SELECT t.site_taken_down_at IS NOT NULL, t.site_taken_down_reason
		FROM event_teams t
		WHERE t.event_id = $1 AND t.slug = $2 FOR SHARE OF t`, ev.ID, name).Scan(&down, &reason)
	return
}
