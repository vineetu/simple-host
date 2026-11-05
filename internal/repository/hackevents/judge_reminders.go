package hackevents

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// JudgeReminderDue aliases the shared model record
// (0003-hack-notices-judges-messages.sql).
type JudgeReminderDue = model.JudgeReminderDue

// DueJudgeReminders lists events due a judging-close reminder now, from
// judging_ends_at and the event's own reminder_offsets (the same due-offset
// rule as DueEntryReminders, generalised across both audiences): events in
// stage judging, judging not locked, not taken down, with judging_ends_at
// set and at least one configured offset. The claim (who has already been
// reminded) is per judge, not per event, so this only says which offset is
// due for the event; the caller decides which judges still need it.
func DueJudgeReminders(ctx context.Context, q sqlkit.Querier) ([]JudgeReminderDue, error) {
	rows, err := queryContext(ctx, q, `
		WITH c AS (
		  SELECT e.id AS event_id, e.reminder_offsets AS offsets, e.judging_ends_at AS deadline
		    FROM events e
		   WHERE e.stage = 'judging' AND e.judging_locked_at IS NULL AND e.taken_down_at IS NULL
		     AND e.judging_ends_at IS NOT NULL AND cardinality(e.reminder_offsets) > 0
		), off AS (
		  SELECT c.event_id, c.deadline, MIN(o) AS offset_min
		    FROM c, unnest(c.offsets) AS o
		   WHERE c.deadline > now() AND (c.deadline - now()) <= (o * interval '1 minute')
		   GROUP BY c.event_id, c.deadline
		)
		SELECT event_id, `+reminderOffsetKindSQL+`, deadline FROM off
		 ORDER BY deadline, event_id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JudgeReminderDue
	for rows.Next() {
		var d JudgeReminderDue
		if err := rows.Scan(&d.EventID, &d.Kind, &d.Deadline); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimJudgeReminder records that this judge's reminder of this kind goes
// out for this deadline. claimed=false when it already went out for the
// same deadline (another sweeper won, or nothing changed). A moved deadline
// (judging_ends_at changed) lets it go out once more -- same shape as
// ClaimEntryReminder.
func ClaimJudgeReminder(ctx context.Context, q sqlkit.Querier, eventID, userID, kind string, deadline time.Time) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, `
		INSERT INTO event_judge_reminders (event_id, user_id, kind, deadline) VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id, user_id, kind) DO UPDATE SET deadline = EXCLUDED.deadline, sent_at = now()
		 WHERE event_judge_reminders.deadline <> EXCLUDED.deadline
		RETURNING 1`, eventID, userID, kind, deadline).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
