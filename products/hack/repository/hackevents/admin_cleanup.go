package hackevents

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/host/sqlkit"
)

// ListEventsForSiteCleanup is every archived event whose team sites are still due to be warned or removed.
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

func durationSeconds(d time.Duration) int64 {
	return int64(d / time.Second)
}

// MarkEventRemovalWarned stamps removal_warned_at, and only while the event still qualifies.
func MarkEventRemovalWarned(ctx context.Context, tx *sql.Tx, eventID string, now time.Time, keepFor, warnFor time.Duration) error {
	res, err := tx.ExecContext(ctx, `
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

// MarkEventSitesRemoved stamps sites_removed_at, and only while the event still qualifies.
func MarkEventSitesRemoved(ctx context.Context, tx *sql.Tx, eventID string, now time.Time, keepFor time.Duration) error {
	res, err := tx.ExecContext(ctx, `
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
