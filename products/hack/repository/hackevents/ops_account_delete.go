package hackevents

import (
	"context"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// AnyRunning is whether any of these events has not ended and is not taken
// down (the events a person organises, for account deletion).
func AnyRunning(ctx context.Context, q sqlkit.Querier, eventIDs []string) (bool, error) {
	var running bool
	err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM events e WHERE e.id = ANY($1::uuid[]) AND e.stage <> 'archived'
		                  AND e.taken_down_at IS NULL)`, pq.Array(eventIDs)).Scan(&running)
	return running, err
}

// CleanupDeletedAccountEvents tidies up the events a person whose account is
// being deleted organises.
func CleanupDeletedAccountEvents(ctx context.Context, q sqlkit.Querier, organised []string) error {
	// A taken-down event of theirs ends: restoring it later must not bring back a running event nobody can manage.
	if _, err := q.ExecContext(ctx, `
		UPDATE events e SET stage = 'archived', closed_at = COALESCE(e.closed_at, now()), updated_at = now()
		 WHERE e.id = ANY($1::uuid[])
		   AND e.taken_down_at IS NOT NULL AND e.stage <> 'archived'`, pq.Array(organised)); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		UPDATE events e SET organiser_name = '', contact_email = '', updated_at = now()
		 WHERE e.id = ANY($1::uuid[])`, pq.Array(organised))
	return err
}

func AccountHoldsEvent(ctx context.Context, q sqlkit.Querier, userID string) (bool, error) {
	var held bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE account_id::text = lower($1))`, userID).Scan(&held)
	return held, err
}

func EraseAccountRows(ctx context.Context, q sqlkit.Querier, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_checklist_acknowledgements WHERE subject_id = $1`, userID)
	return err
}
