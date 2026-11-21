package hackevents

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type JudgeReminderDue = model.JudgeReminderDue

// judgeReminderEventsSQL is the events whose judges may be due a
// judging-close reminder.
const judgeReminderEventsSQL = `e.stage = 'judging' AND e.judging_locked_at IS NULL AND e.taken_down_at IS NULL
		     AND e.judging_ends_at IS NOT NULL AND cardinality(e.reminder_offsets) > 0`

// JudgeReminderEvents is the ids of the events whose judges may be due a
// judging-close reminder (DueJudgeReminders takes those judges).
func JudgeReminderEvents(ctx context.Context, q sqlkit.Querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT e.id FROM events e WHERE `+judgeReminderEventsSQL+` ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DueJudgeReminders lists (event, judge) pairs due a judging-close reminder now, from judging_ends_at and the event's own reminder_offsets; judges are the joined judges of JudgeReminderEvents; hackentries.UnsentJudgeReminders drops those already sent.
func DueJudgeReminders(ctx context.Context, q sqlkit.Querier, judges []model.EventMember) ([]JudgeReminderDue, error) {
	eventIDs, userIDs := make([]string, len(judges)), make([]string, len(judges))
	for i, j := range judges {
		eventIDs[i], userIDs[i] = j.EventID, j.UserID
	}
	rows, err := q.QueryContext(ctx, `
		WITH c AS (
		  SELECT e.id AS event_id, e.reminder_offsets AS offsets, e.judging_ends_at AS deadline
		    FROM events e
		   WHERE `+judgeReminderEventsSQL+`
		), off AS (
		  SELECT c.event_id, c.deadline, MIN(o) AS offset_min
		    FROM c, unnest(c.offsets) AS o
		   WHERE c.deadline > now() AND (c.deadline - now()) <= (o * interval '1 minute')
		   GROUP BY c.event_id, c.deadline
		), k AS (
		  SELECT event_id, deadline, `+reminderOffsetKindSQL+` AS kind FROM off
		)
		SELECT k.event_id, m.user_id, k.kind, k.deadline
		  FROM k JOIN unnest($1::uuid[], $2::uuid[]) AS m(event_id, user_id) ON m.event_id = k.event_id
		 ORDER BY k.deadline, k.event_id, m.user_id`, pq.Array(eventIDs), pq.Array(userIDs))
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
