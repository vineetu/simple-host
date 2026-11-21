package hackevents

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

const NoticeEntryReminder = "entry_reminder"

type EntryReminder = model.EntryReminder

// reminderOffsetKindSQL is the label for an offset of M minutes, the same rule Go's ReminderLabel uses.
const reminderOffsetKindSQL = `CASE WHEN offset_min % 60 = 0 THEN (offset_min/60)::text || 'h' ELSE offset_min::text || 'm' END`

// DueEntryReminders lists teams due a reminder now, from their own event's reminder_offsets.
func DueEntryReminders(ctx context.Context, q sqlkit.Querier) ([]EntryReminder, error) {
	rows, err := queryContext(ctx, q, `
		WITH c AS (
		  SELECT e.id AS event_id, t.id AS team_id, e.reminder_offsets AS offsets, `+EffectiveDeadlineSQL+` AS deadline
		    FROM event_teams t JOIN events e ON e.id = t.event_id
		   WHERE e.stage IN ('open','building','closed') AND e.taken_down_at IS NULL
		     AND t.site_taken_down_at IS NULL
		     AND cardinality(e.reminder_offsets) > 0
		     AND NOT EXISTS (SELECT 1 FROM event_entries en WHERE en.team_id = t.id AND en.submitted_at IS NOT NULL)
		     AND EXISTS (SELECT 1 FROM event_members m WHERE m.event_id = e.id AND m.team_id = t.id)
		), off AS (
		  SELECT c.event_id, c.team_id, c.deadline, MIN(o) AS offset_min
		    FROM c, unnest(c.offsets) AS o
		   WHERE c.deadline > now() AND (c.deadline - now()) <= (o * interval '1 minute')
		   GROUP BY c.event_id, c.team_id, c.deadline
		), k AS (
		  SELECT event_id, team_id, deadline, `+reminderOffsetKindSQL+` AS kind FROM off
		)
		SELECT k.event_id, k.team_id, k.kind, k.deadline FROM k
		 WHERE NOT EXISTS (SELECT 1 FROM event_entry_reminders r
		        WHERE r.event_id = k.event_id AND r.team_id = k.team_id AND r.kind = k.kind AND r.deadline = k.deadline)
		 ORDER BY k.deadline, k.team_id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EntryReminder
	for rows.Next() {
		var r EntryReminder
		if err := rows.Scan(&r.EventID, &r.TeamID, &r.Kind, &r.Deadline); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimEntryReminder records that this team's reminder of this kind goes out for this deadline.
func ClaimEntryReminder(ctx context.Context, q sqlkit.Querier, r EntryReminder) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `
		INSERT INTO event_entry_reminders (event_id, team_id, kind, deadline) VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id, team_id, kind) DO UPDATE SET deadline = EXCLUDED.deadline, sent_at = now()
		 WHERE event_entry_reminders.deadline <> EXCLUDED.deadline
		RETURNING 1`, r.EventID, r.TeamID, r.Kind, r.Deadline).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
