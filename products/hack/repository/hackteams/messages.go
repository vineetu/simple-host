package hackteams

import (
	"context"
	"database/sql"
	"sort"
	"time"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type TeamMessage = model.TeamMessage

// InsertTeamMessage stores one message of the team's thread. The sender's
// name is not stored: readers take it from the account (sender_id), so the
// returned message's SenderName is empty for the caller to fill.
func InsertTeamMessage(ctx context.Context, q sqlkit.Querier, eventID, teamID, senderID, body string, fromTeam bool) (TeamMessage, error) {
	var sender sql.NullString
	if senderID != "" {
		sender = sql.NullString{String: senderID, Valid: true}
	}
	var m TeamMessage
	err := q.QueryRowContext(ctx, `
		INSERT INTO event_team_messages (event_id, team_id, sender_id, body, from_team)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, event_id, team_id, sender_id, body, created_at, from_team`,
		eventID, teamID, sender, body, fromTeam).Scan(&m.ID, &m.EventID, &m.TeamID, &m.SenderID, &m.Body, &m.CreatedAt, &m.FromTeam)
	return m, err
}

// ListTeamMessages is the team's thread, newest first, each with its
// sender's current display name ("" once the account is gone).
func ListTeamMessages(ctx context.Context, q sqlkit.Querier, teamID string, limit int) ([]TeamMessage, error) {
	rows, err := queryContext(ctx, q, `
		SELECT id, event_id, team_id, sender_id, body, created_at, from_team
		  FROM event_team_messages WHERE team_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2`, teamID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TeamMessage{}
	var senders []string
	for rows.Next() {
		var m TeamMessage
		if err := rows.Scan(&m.ID, &m.EventID, &m.TeamID, &m.SenderID, &m.Body, &m.CreatedAt, &m.FromTeam); err != nil {
			return nil, err
		}
		out = append(out, m)
		if m.SenderID.Valid {
			senders = append(senders, m.SenderID.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, senders)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].SenderName = users[out[i].SenderID.String].DisplayName.String
	}
	return out, nil
}

// CountRecentTeamMessages counts the event's organiser-written message rows created since since, and the oldest such row's time.
func CountRecentTeamMessages(ctx context.Context, q sqlkit.Querier, eventID string, since time.Time) (int, sql.NullTime, error) {
	var n int
	var oldest sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT count(*), min(created_at) FROM event_team_messages WHERE event_id = $1 AND NOT from_team AND created_at > $2`,
		eventID, since).Scan(&n, &oldest)
	return n, oldest, err
}

// CountRecentTeamInbound counts what one team wrote to the organisers since since, and the oldest such row's time (for the rate limit's Retry-After).
func CountRecentTeamInbound(ctx context.Context, q sqlkit.Querier, teamID string, since time.Time) (int, sql.NullTime, error) {
	var n int
	var oldest sql.NullTime
	err := q.QueryRowContext(ctx, `
		SELECT count(*), min(created_at) FROM event_team_messages WHERE team_id = $1 AND from_team AND created_at > $2`,
		teamID, since).Scan(&n, &oldest)
	return n, oldest, err
}

type InboundTeam struct {
	TeamSlug, TeamName, Latest string
	Count                      int
}

// ListInboundTeams names each team behind an organiser's unread notices
// (hackmembers.InboundGroups) and the text of its newest message, newest
// first, at most 100.
func ListInboundTeams(ctx context.Context, q sqlkit.Querier, groups []model.InboundGroup) ([]InboundTeam, error) {
	teamIDs, refs := make([]string, 0, len(groups)), make([]string, 0, len(groups))
	for _, g := range groups {
		teamIDs = append(teamIDs, g.TeamID)
		if g.LatestRef != "" {
			refs = append(refs, g.LatestRef)
		}
	}
	type team struct{ slug, name string }
	teams := map[string]team{}
	rows, err := q.QueryContext(ctx, `SELECT id, slug, name FROM event_teams WHERE id = ANY($1::uuid[])`, pq.Array(teamIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var t team
		if err := rows.Scan(&id, &t.slug, &t.name); err != nil {
			return nil, err
		}
		teams[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	bodies := map[string]string{}
	mrows, err := q.QueryContext(ctx, `SELECT id, body FROM event_team_messages WHERE id = ANY($1::uuid[])`, pq.Array(refs))
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var id, body string
		if err := mrows.Scan(&id, &body); err != nil {
			return nil, err
		}
		bodies[id] = body
	}
	if err := mrows.Err(); err != nil {
		return nil, err
	}
	type found struct {
		InboundTeam
		at time.Time
	}
	var list []found
	for _, g := range groups {
		t, ok := teams[g.TeamID]
		if !ok {
			continue
		}
		list = append(list, found{InboundTeam{TeamSlug: t.slug, TeamName: t.name, Latest: bodies[g.LatestRef], Count: g.Count}, g.LatestAt})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if !list[i].at.Equal(list[j].at) {
			return list[i].at.After(list[j].at)
		}
		return list[i].TeamSlug < list[j].TeamSlug
	})
	out := []InboundTeam{}
	for i, f := range list {
		if i == 100 {
			break
		}
		out = append(out, f.InboundTeam)
	}
	return out, nil
}

// TeamMessagesUpTo is the ids of what the team wrote to the organisers up to upTo.
func TeamMessagesUpTo(ctx context.Context, q sqlkit.Querier, teamID string, upTo time.Time) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM event_team_messages WHERE team_id = $1 AND created_at <= $2`, teamID, upTo)
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

// MailInvite is the team name of an invitation waiting on userID, for its email line.
func MailInvite(ctx context.Context, q sqlkit.Querier, refID, userID string) (string, error) {
	var team string
	err := q.QueryRowContext(ctx, `
			SELECT t.name FROM event_team_requests r JOIN event_teams t ON t.id = r.team_id
			 WHERE r.id = $1 AND r.user_id = $2 AND r.kind = 'invite'`, refID, userID).Scan(&team)
	return team, err
}

// NoticeTeamMessageBody reads a team message's text for the notice line.
func NoticeTeamMessageBody(ctx context.Context, q sqlkit.Querier, id, teamID string) (string, error) {
	var body string
	err := q.QueryRowContext(ctx, `SELECT body FROM event_team_messages WHERE id = $1 AND team_id = $2`, id, teamID).Scan(&body)
	return body, err
}

// NoticeTeamInboundLine reads what a team wrote to the organisers, with the team's name, for the email line.
func NoticeTeamInboundLine(ctx context.Context, q sqlkit.Querier, id, teamID string) (string, string, error) {
	var name, body string
	err := q.QueryRowContext(ctx, `SELECT t.name, m.body FROM event_team_messages m JOIN event_teams t ON t.id = m.team_id
		WHERE m.id = $1 AND m.team_id = $2 AND m.from_team`, id, teamID).Scan(&name, &body)
	return name, body, err
}
