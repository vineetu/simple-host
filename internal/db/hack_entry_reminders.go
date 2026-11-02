package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// NoticeEntryReminder is the entry deadline reminder (entries slice 3,
// db/migrations/zx-hack-entry-reminders.sql): 24 hours and 1 hour before a
// team's effective deadline, every current member of a team whose entry is
// not submitted gets an email through the notices queue. It is email only
// (born seen): Home already shows the countdown and the checklist.
const NoticeEntryReminder = "entry_reminder"

// EntryReminder is one team that is due a reminder now.
type EntryReminder struct {
	EventID, TeamID string
	Kind            string // "24h" or "1h"
	Deadline        time.Time
}

// DueEntryReminders lists teams whose effective deadline is still ahead and
// at most 24 hours away, whose entry is not submitted (or not started), in
// an event that still takes entries, and that have not yet had this
// reminder for this deadline. Inside the last hour only the 1-hour reminder
// is due, so a team first seen 40 minutes out gets one email, not two.
func DueEntryReminders(ctx context.Context, q Querier) ([]EntryReminder, error) {
	rows, err := queryContext(ctx, q, `
		WITH c AS (
		  SELECT e.id AS event_id, t.id AS team_id, `+EffectiveDeadlineSQL+` AS deadline
		    FROM event_teams t JOIN events e ON e.id = t.event_id
		   WHERE e.stage IN ('open','building','closed') AND e.taken_down_at IS NULL
		     AND t.site_taken_down_at IS NULL
		     AND NOT EXISTS (SELECT 1 FROM event_entries en WHERE en.team_id = t.id AND en.submitted_at IS NOT NULL)
		     AND EXISTS (SELECT 1 FROM event_members m WHERE m.event_id = e.id AND m.team_id = t.id)
		), k AS (
		  SELECT event_id, team_id, deadline,
		         CASE WHEN deadline <= now() + interval '1 hour' THEN '1h' ELSE '24h' END AS kind
		    FROM c WHERE deadline > now() AND deadline <= now() + interval '24 hours'
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

// ClaimEntryReminder records that this team's reminder of this kind goes out
// for this deadline. claimed=false when it already went out for the same
// deadline (another sweeper won, or nothing changed). A moved deadline
// (an extension, a later event deadline) lets it go out once more.
func ClaimEntryReminder(ctx context.Context, q Querier, r EntryReminder) (bool, error) {
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
