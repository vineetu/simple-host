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

// DueJudgeReminders lists (event, judge) pairs due a judging-close reminder
// now, from judging_ends_at and the event's own reminder_offsets (the same
// due-offset rule as DueEntryReminders, generalised across both
// audiences): events in stage judging, judging not locked, not taken down,
// with judging_ends_at set and at least one configured offset, joined out
// to every current judge member. The NOT EXISTS against
// event_judge_reminders (the same shape DueEntryReminders uses against
// event_entry_reminders) drops a judge who already has this kind claimed
// for this exact deadline, so a judge whose reminder already went out --
// or who simply has nothing left to score, which never claims -- cannot
// sit in the LIMIT 500 batch forever and crowd out judges still due.
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
		), k AS (
		  SELECT event_id, deadline, `+reminderOffsetKindSQL+` AS kind FROM off
		)
		SELECT k.event_id, m.user_id, k.kind, k.deadline
		  FROM k JOIN event_members m ON m.event_id = k.event_id AND m.role = 'judge'
		 WHERE NOT EXISTS (SELECT 1 FROM event_judge_reminders r
		        WHERE r.event_id = k.event_id AND r.user_id = m.user_id AND r.kind = k.kind AND r.deadline = k.deadline)
		 ORDER BY k.deadline, k.event_id, m.user_id LIMIT 500`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JudgeReminderDue
	for rows.Next() {
		var d JudgeReminderDue
		if err := rows.Scan(&d.EventID, &d.UserID, &d.Kind, &d.Deadline); err != nil {
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
