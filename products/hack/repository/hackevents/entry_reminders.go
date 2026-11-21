package hackevents

import (
	"context"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type EntryReminder = model.EntryReminder

// reminderOffsetKindSQL is the label for an offset of M minutes, the same rule Go's ReminderLabel uses.
const reminderOffsetKindSQL = `CASE WHEN offset_min % 60 = 0 THEN (offset_min/60)::text || 'h' ELSE offset_min::text || 'm' END`

// DueEntryReminders lists the teams due a reminder now, from their own
// event's reminder_offsets; teams are the candidates (anyone is on them, site
// not taken down), from hackteams. hackentries.UnsentEntryReminders drops the
// submitted teams and the reminders already sent.
func DueEntryReminders(ctx context.Context, q sqlkit.Querier, teams []model.EventTeam) ([]EntryReminder, error) {
	ids, events, overrides := make([]string, len(teams)), make([]string, len(teams)), make([]string, len(teams))
	for i, t := range teams {
		ids[i], events[i] = t.ID, t.EventID
		if t.DeadlineOverride.Valid {
			overrides[i] = t.DeadlineOverride.Time.UTC().Format(time.RFC3339Nano)
		}
	}
	rows, err := q.QueryContext(ctx, `
		WITH c AS (
		  SELECT e.id AS event_id, t.id AS team_id, e.reminder_offsets AS offsets, `+effectiveDeadlineSQL+` AS deadline
		    FROM (SELECT u.id, u.event_id, NULLIF(u.override, '')::timestamptz AS deadline_override
		            FROM unnest($1::uuid[], $2::uuid[], $3::text[]) AS u(id, event_id, override)) t
		    JOIN events e ON e.id = t.event_id
		   WHERE e.stage IN ('open','building','closed') AND e.taken_down_at IS NULL
		     AND cardinality(e.reminder_offsets) > 0
		), off AS (
		  SELECT c.event_id, c.team_id, c.deadline, MIN(o) AS offset_min
		    FROM c, unnest(c.offsets) AS o
		   WHERE c.deadline > now() AND (c.deadline - now()) <= (o * interval '1 minute')
		   GROUP BY c.event_id, c.team_id, c.deadline
		), k AS (
		  SELECT event_id, team_id, deadline, `+reminderOffsetKindSQL+` AS kind FROM off
		)
		SELECT k.event_id, k.team_id, k.kind, k.deadline FROM k ORDER BY k.deadline, k.team_id`, pq.Array(ids), pq.Array(events), pq.Array(overrides))
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
