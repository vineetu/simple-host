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
// fromTeam marks a message a team member wrote to the organisers.
func InsertTeamMessage(ctx context.Context, q sqlkit.Querier, eventID, teamID, senderID, senderName, body string, fromTeam bool) (TeamMessage, error) {
	var sender sql.NullString
	if senderID != "" {
		sender = sql.NullString{String: senderID, Valid: true}
	}
	var m TeamMessage
	err := q.QueryRowContext(ctx, `
		INSERT INTO event_team_messages (event_id, team_id, sender_id, sender_name, body, from_team)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, event_id, team_id, sender_id, sender_name, body, created_at, from_team`,
		eventID, teamID, sender, senderName, body, fromTeam).Scan(&m.ID, &m.EventID, &m.TeamID, &m.SenderID, &m.SenderName, &m.Body, &m.CreatedAt, &m.FromTeam)
	return m, err
}

// ListTeamMessages is one team's message history, newest first, at most
// limit rows.
func ListTeamMessages(ctx context.Context, q sqlkit.Querier, teamID string, limit int) ([]TeamMessage, error) {
	rows, err := queryContext(ctx, q, `
		SELECT id, event_id, team_id, sender_id, sender_name, body, created_at, from_team
		  FROM event_team_messages WHERE team_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, teamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMessage{}
	for rows.Next() {
		var m TeamMessage
		if err := rows.Scan(&m.ID, &m.EventID, &m.TeamID, &m.SenderID, &m.SenderName, &m.Body, &m.CreatedAt, &m.FromTeam); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountRecentTeamMessages counts the event's organiser-written message rows created since
// since, and the oldest such row's time (for the rate limit's Retry-After).
// The count itself is the DB-counted rate limit: it is the durable history,
// so it survives a restart.
func CountRecentTeamMessages(ctx context.Context, q sqlkit.Querier, eventID string, since time.Time) (int, sql.NullTime, error) {
	var n int
	var oldest sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT count(*), min(created_at) FROM event_team_messages WHERE event_id = $1 AND NOT from_team AND created_at > $2`,
		eventID, since).Scan(&n, &oldest)
	return n, oldest, err
}

// CountRecentTeamInbound counts what one team wrote to the organisers since
// since, and the oldest such row's time (for the rate limit's Retry-After).
func CountRecentTeamInbound(ctx context.Context, q sqlkit.Querier, teamID string, since time.Time) (int, sql.NullTime, error) {
	var n int
	var oldest sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT count(*), min(created_at) FROM event_team_messages WHERE team_id = $1 AND from_team AND created_at > $2`,
		teamID, since).Scan(&n, &oldest)
	return n, oldest, err
}

// InboundTeam is one team with messages the organiser has not opened yet.
type InboundTeam struct {
	TeamSlug, TeamName, Latest string
	Count                      int
}

// ListInboundTeams lists the teams whose messages to the organisers this
// organiser has not read (unseen team_inbound notices), one row per team with
// its latest message, newest first. A team that was deleted drops out.
func ListInboundTeams(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]InboundTeam, error) {
	rows, err := queryContext(ctx, q, `
		SELECT t.slug, t.name, g.n,
		       (SELECT m.body FROM event_team_messages m WHERE m.id = g.latest_ref)
		  FROM (SELECT n.team_id, count(*) AS n,
		               (array_agg(n.ref_id ORDER BY n.created_at DESC, n.id DESC))[1] AS latest_ref,
		               max(n.created_at) AS latest_at
		          FROM event_notices n
		         WHERE n.event_id = $1 AND n.user_id = $2 AND n.kind = 'team_inbound' AND n.seen_at IS NULL
		         GROUP BY n.team_id) g
		  JOIN event_teams t ON t.id = g.team_id
		 ORDER BY g.latest_at DESC, t.slug
		 LIMIT 100`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InboundTeam{}
	for rows.Next() {
		var it InboundTeam
		var body sql.NullString
		if err := rows.Scan(&it.TeamSlug, &it.TeamName, &it.Count, &body); err != nil {
			return nil, err
		}
		it.Latest = body.String
		out = append(out, it)
	}
	return out, rows.Err()
}

// MarkTeamInboundSeen marks this organiser's unread notices about one team's
// messages up to upTo seen: they opened the thread and were shown those.
func MarkTeamInboundSeen(ctx context.Context, q sqlkit.Querier, eventID, userID, teamID string, upTo time.Time) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_notices n SET seen_at = now()
		 WHERE n.event_id = $1 AND n.user_id = $2 AND n.team_id = $3 AND n.kind = 'team_inbound' AND n.seen_at IS NULL
		   AND EXISTS (SELECT 1 FROM event_team_messages m WHERE m.id = n.ref_id AND m.created_at <= $4)`,
		eventID, userID, teamID, upTo)
	return err
}
