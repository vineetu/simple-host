package hackteams

import (
	"context"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackmembers"
)

type PendingJoinRequest = model.PendingJoinRequest

type PendingInvite = model.PendingInvite

// ListPendingJoinRequests lists the requests from members waiting on the team's admins, oldest first.
func ListPendingJoinRequests(ctx context.Context, q sqlkit.Querier, teamID string) ([]PendingJoinRequest, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.id, r.user_id, r.event_id FROM event_team_requests r
		 WHERE r.team_id = $1 AND r.kind = 'request' ORDER BY r.created_at, r.id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []PendingJoinRequest
	var ids []string
	var eventID string
	for rows.Next() {
		var p PendingJoinRequest
		var id string
		if err := rows.Scan(&p.ID, &id, &eventID); err != nil {
			return nil, err
		}
		found, ids = append(found, p), append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	members, err := hackmembers.MembersByIDs(ctx, q, eventID, ids)
	if err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := []PendingJoinRequest{}
	for i, p := range found {
		u, ok := users[ids[i]]
		if _, member := members[ids[i]]; ok && member {
			p.DisplayName = u.DisplayName.String
			out = append(out, p)
		}
	}
	return out, nil
}

// MailRequest is the requester's name and the team's for a join request's
// email line; sql.ErrNoRows once the request or the requester's membership is gone.
func MailRequest(ctx context.Context, q sqlkit.Querier, refID, teamID string) (string, string, error) {
	var eventID, userID, team string
	err := q.QueryRowContext(ctx, `
			SELECT r.event_id, r.user_id, t.name FROM event_team_requests r
			  JOIN event_teams t ON t.id = r.team_id
			 WHERE r.id = $1 AND r.team_id = $2 AND r.kind = 'request'`, refID, teamID).Scan(&eventID, &userID, &team)
	if err != nil {
		return "", "", err
	}
	if _, err := hackmembers.Member(ctx, q, eventID, userID, hackmembers.NoLock); err != nil {
		return "", "", err
	}
	u, err := accounts.UserByID(ctx, q, userID)
	if err != nil {
		return "", "", err
	}
	return u.DisplayName.String, team, nil
}

// ListPendingInvites lists the invites waiting on this person, oldest first.
func ListPendingInvites(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]PendingInvite, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT r.id, t.name FROM event_team_requests r JOIN event_teams t ON t.id = r.team_id
		 WHERE r.event_id = $1 AND r.user_id = $2 AND r.kind = 'invite' ORDER BY r.created_at, r.id`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PendingInvite{}
	for rows.Next() {
		var p PendingInvite
		if err := rows.Scan(&p.ID, &p.TeamName); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
