package hackentries

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

// UnsentEntryReminders keeps the due reminders (hackevents.DueEntryReminders)
// whose team has not submitted and has not had this one for this deadline,
// at most 500, soonest deadline first.
func UnsentEntryReminders(ctx context.Context, q sqlkit.Querier, due []model.EntryReminder) ([]model.EntryReminder, error) {
	n := len(due)
	events, teams, kinds, deadlines := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, r := range due {
		events[i], teams[i], kinds[i], deadlines[i] = r.EventID, r.TeamID, r.Kind, r.Deadline.UTC().Format(time.RFC3339Nano)
	}
	rows, err := q.QueryContext(ctx, `
		SELECT u.event_id, u.team_id, u.kind, d.deadline
		  FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::text[]) AS u(event_id, team_id, kind, deadline_text),
		       LATERAL (SELECT u.deadline_text::timestamptz AS deadline) d
		 WHERE NOT EXISTS (SELECT 1 FROM event_entries en WHERE en.team_id = u.team_id AND en.submitted_at IS NOT NULL)
		   AND NOT EXISTS (SELECT 1 FROM event_entry_reminders r
		        WHERE r.event_id = u.event_id AND r.team_id = u.team_id AND r.kind = u.kind AND r.deadline = d.deadline)
		 ORDER BY d.deadline, u.team_id LIMIT 500`, pq.Array(events), pq.Array(teams), pq.Array(kinds), pq.Array(deadlines))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.EntryReminder
	for rows.Next() {
		var r model.EntryReminder
		if err := rows.Scan(&r.EventID, &r.TeamID, &r.Kind, &r.Deadline); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimEntryReminder records that this team's reminder of this kind goes out for this deadline.
func ClaimEntryReminder(ctx context.Context, q sqlkit.Querier, r model.EntryReminder) (bool, error) {
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

// UnsentJudgeReminders keeps the due judge reminders
// (hackevents.DueJudgeReminders) not yet claimed for this deadline, at most
// 500, soonest deadline first.
func UnsentJudgeReminders(ctx context.Context, q sqlkit.Querier, due []model.JudgeReminderDue) ([]model.JudgeReminderDue, error) {
	n := len(due)
	events, users, kinds, deadlines := make([]string, n), make([]string, n), make([]string, n), make([]string, n)
	for i, d := range due {
		events[i], users[i], kinds[i], deadlines[i] = d.EventID, d.UserID, d.Kind, d.Deadline.UTC().Format(time.RFC3339Nano)
	}
	rows, err := q.QueryContext(ctx, `
		SELECT u.event_id, u.user_id, u.kind, d.deadline
		  FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::text[]) AS u(event_id, user_id, kind, deadline_text),
		       LATERAL (SELECT u.deadline_text::timestamptz AS deadline) d
		 WHERE NOT EXISTS (SELECT 1 FROM event_judge_reminders r
		        WHERE r.event_id = u.event_id AND r.user_id = u.user_id AND r.kind = u.kind AND r.deadline = d.deadline)
		 ORDER BY d.deadline, u.event_id, u.user_id LIMIT 500`, pq.Array(events), pq.Array(users), pq.Array(kinds), pq.Array(deadlines))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.JudgeReminderDue
	for rows.Next() {
		var d model.JudgeReminderDue
		if err := rows.Scan(&d.EventID, &d.UserID, &d.Kind, &d.Deadline); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ClaimJudgeReminder records that this judge's reminder of this kind goes out for this deadline.
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
