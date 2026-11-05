package hackevents

import (
	"context"
	"database/sql"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// TeamMessage aliases the shared model record (0003-hack-notices-judges-messages.sql).
type TeamMessage = model.TeamMessage

// InsertTeamMessage writes one event_team_messages row: the durable history.
// Delivery is the notice (kind team_message), queued by the caller in the
// same transaction.
func InsertTeamMessage(ctx context.Context, q sqlkit.Querier, eventID, teamID, senderID, senderName, body string) (TeamMessage, error) {
	var sender sql.NullString
	if senderID != "" {
		sender = sql.NullString{String: senderID, Valid: true}
	}
	var m TeamMessage
	err := q.QueryRowContext(ctx, `
		INSERT INTO event_team_messages (event_id, team_id, sender_id, sender_name, body)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, event_id, team_id, sender_id, sender_name, body, created_at`,
		eventID, teamID, sender, senderName, body).Scan(&m.ID, &m.EventID, &m.TeamID, &m.SenderID, &m.SenderName, &m.Body, &m.CreatedAt)
	return m, err
}

// ListTeamMessages is one team's message history, newest first, at most
// limit rows.
func ListTeamMessages(ctx context.Context, q sqlkit.Querier, teamID string, limit int) ([]TeamMessage, error) {
	rows, err := queryContext(ctx, q, `
		SELECT id, event_id, team_id, sender_id, sender_name, body, created_at
		  FROM event_team_messages WHERE team_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, teamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMessage{}
	for rows.Next() {
		var m TeamMessage
		if err := rows.Scan(&m.ID, &m.EventID, &m.TeamID, &m.SenderID, &m.SenderName, &m.Body, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountRecentTeamMessages counts the event's message rows created since
// since, and the oldest such row's time (for the rate limit's Retry-After).
// The count itself is the DB-counted rate limit: it is the durable history,
// so it survives a restart.
func CountRecentTeamMessages(ctx context.Context, q sqlkit.Querier, eventID string, since time.Time) (int, sql.NullTime, error) {
	var n int
	var oldest sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT count(*), min(created_at) FROM event_team_messages WHERE event_id = $1 AND created_at > $2`,
		eventID, since).Scan(&n, &oldest)
	return n, oldest, err
}
