package hackevents

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"time"
)

// ListEventsForSiteCleanup is every archived event whose team sites are still
// due to be warned or removed: keep_sites is off, nothing has been removed
// yet, and closed_at is set (SetEventStage stamps it when the stage becomes
// archived). The caller decides warn versus remove from closed_at.
func ListEventsForSiteCleanup(ctx context.Context, q sqlkit.Querier) ([]Event, error) {
	rows, err := queryContext(ctx, q, `
		SELECT `+EventColumns+`
		  FROM events
		 WHERE stage = 'archived'
		   AND keep_sites = FALSE
		   AND sites_removed_at IS NULL
		   AND closed_at IS NOT NULL
		   AND taken_down_at IS NULL
		 ORDER BY closed_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(ScanEventFields(&e)...); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// durationSeconds is a duration as whole seconds, for `n * interval '1 second'`.
func durationSeconds(d time.Duration) int64 {
	return int64(d / time.Second)
}

// MarkEventRemovalWarned stamps removal_warned_at, and only while the event
// still qualifies: archived, sites not kept, not yet removed, not yet warned,
// and now is in the warning window (at or after warnAt, before removeAt).
// sql.ErrNoRows when it no longer qualifies. The caller sends the email
// before committing, so the stamp is on record only once the email was accepted.
func MarkEventRemovalWarned(ctx context.Context, q sqlkit.Querier, eventID string, now time.Time, keepFor, warnFor time.Duration) error {
	res, err := q.ExecContext(ctx, `
		UPDATE events
		   SET removal_warned_at = $2
		 WHERE id = $1
		   AND stage = 'archived'
		   AND keep_sites = FALSE
		   AND sites_removed_at IS NULL
		   AND removal_warned_at IS NULL
		   AND closed_at IS NOT NULL
		   AND taken_down_at IS NULL
		   AND closed_at + ($3 * interval '1 second') > $2
		   AND closed_at + ($3 * interval '1 second') - ($4 * interval '1 second') <= $2`,
		eventID, now.UTC(), durationSeconds(keepFor), durationSeconds(warnFor))
	return cleanupOneRow(res, err)
}

// MarkEventSitesRemoved stamps sites_removed_at, and only while the event
// still qualifies: archived, sites not kept, not yet removed, and closed_at
// plus keepFor is at or before now. sql.ErrNoRows when it no longer qualifies.
// It changes nothing else on the row.
func MarkEventSitesRemoved(ctx context.Context, q sqlkit.Querier, eventID string, now time.Time, keepFor time.Duration) error {
	res, err := q.ExecContext(ctx, `
		UPDATE events
		   SET sites_removed_at = $2
		 WHERE id = $1
		   AND stage = 'archived'
		   AND keep_sites = FALSE
		   AND sites_removed_at IS NULL
		   AND closed_at IS NOT NULL
		   AND taken_down_at IS NULL
		   AND closed_at + ($3 * interval '1 second') <= $2`,
		eventID, now.UTC(), durationSeconds(keepFor))
	return cleanupOneRow(res, err)
}

// SetEventKeepSites sets whether an archived event's team sites stay up.
func SetEventKeepSites(ctx context.Context, q sqlkit.Querier, eventID string, keep bool) (Event, error) {
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET keep_sites = $2, updated_at = now()
		 WHERE id = $1
		 RETURNING `+EventColumns, eventID, keep))
}

func cleanupOneRow(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetUserSuspended suspends an account (reason non-empty) or re-enables it
// (reason empty). Keys and connector grants are kept; the checks that read
// users.suspended_at refuse them while it is set. Suspending also ends every
// site sign-in of the account (EndVisitorSessions): nothing re-enables them.
func SetUserSuspended(ctx context.Context, q sqlkit.Querier, userID, reason string) error {
	var res sql.Result
	var err error
	if reason != "" {
		res, err = q.ExecContext(ctx,
			`UPDATE users SET suspended_at = COALESCE(suspended_at, now()), suspended_reason = $2 WHERE id::text = $1`,
			userID, reason)
	} else {
		res, err = q.ExecContext(ctx,
			`UPDATE users SET suspended_at = NULL, suspended_reason = NULL WHERE id::text = $1`, userID)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	if reason != "" {
		_, err = q.ExecContext(ctx, `DELETE FROM visitor_sessions WHERE user_id = $1`, userID)
		return err
	}
	return nil
}
