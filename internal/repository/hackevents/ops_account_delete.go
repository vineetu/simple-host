package hackevents

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

func AccountHasRunningEvents(ctx context.Context, q sqlkit.Querier, userID string) (bool, error) {
	var running bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_members m JOIN events e ON e.id = m.event_id
		                WHERE m.user_id = $1 AND m.role = 'organiser' AND e.stage <> 'archived'
		                  AND e.taken_down_at IS NULL)`, userID).Scan(&running); err != nil {
		return false, err
	}
	return running, nil
}
func CleanupDeletedAccountEvents(ctx context.Context, q sqlkit.Querier, userID string) error {
	// A taken-down event of theirs ends: restoring it later must not bring
	// back a running event nobody can manage.
	if _, err := q.ExecContext(ctx, `
		UPDATE events e SET stage = 'archived', closed_at = COALESCE(e.closed_at, now()), updated_at = now()
		  FROM event_members m
		 WHERE m.event_id = e.id AND m.user_id = $1 AND m.role = 'organiser'
		   AND e.taken_down_at IS NOT NULL AND e.stage <> 'archived'`, userID); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE events e SET organiser_name = '', contact_email = '', updated_at = now()
		  FROM event_members m
		 WHERE m.event_id = e.id AND m.user_id = $1 AND m.role = 'organiser'`, userID); err != nil {
		return err
	}
	// A team this person is alone on goes with them (its code would
	// otherwise stay live on an empty team).
	_, err := q.ExecContext(ctx, `
		DELETE FROM event_teams t USING events e
		 WHERE e.id = t.event_id
		   AND t.id IN (SELECT team_id FROM event_members WHERE user_id = $1 AND team_id IS NOT NULL)
		   AND (SELECT count(*) FROM event_members m WHERE m.team_id = t.id) = 1
		   AND `+TeamKeepableSQL, userID)
	return err
}

func AccountHoldsEvent(ctx context.Context, q sqlkit.Querier, userID string) (bool, error) {
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE account_id::text = lower($1))`, userID).Scan(&held)
	return held, err
}
