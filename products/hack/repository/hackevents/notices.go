package hackevents

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// NoticeMemberStatus reads the member's current approval and team.
func NoticeMemberStatus(ctx context.Context, q sqlkit.Querier, eventID, userID string) (string, sql.NullString, bool, error) {
	var status string
	var team sql.NullString
	var admin bool
	err := q.QueryRowContext(ctx, `SELECT approval_status, team_id, team_admin FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, userID).Scan(&status, &team, &admin)
	return status, team, admin, err
}

// NoticeTeamAdmin checks current team administration.
func NoticeTeamAdmin(ctx context.Context, q sqlkit.Querier, eventID, userID, teamID string) (bool, error) {
	var admin bool
	err := q.QueryRowContext(ctx, `SELECT team_admin FROM event_members WHERE event_id = $1 AND user_id = $2 AND team_id = $3`, eventID, userID, teamID).Scan(&admin)
	return admin, err
}

// MailMember reads the recipient's current event membership.
func MailMember(ctx context.Context, q sqlkit.Querier, eventID, userID string) (string, string, sql.NullString, bool, string, error) {
	var role, status, stage string
	var team sql.NullString
	var admin bool
	err := q.QueryRowContext(ctx, `
		SELECT m.role, m.approval_status, m.team_id, m.team_admin, e.stage FROM event_members m JOIN events e ON e.id = m.event_id
		 WHERE m.event_id = $1 AND m.user_id = $2 AND e.taken_down_at IS NULL`, eventID, userID).Scan(&role, &status, &team, &admin, &stage)
	return role, status, team, admin, stage, err
}

// MailRequest reads a pending join request.
func MailRequest(ctx context.Context, q sqlkit.Querier, refID, teamID string) (string, string, error) {
	var name, team string
	err := q.QueryRowContext(ctx, `
			SELECT COALESCE(u.display_name,''), t.name FROM event_team_requests r
			  JOIN event_teams t ON t.id = r.team_id
			  JOIN event_members m ON m.event_id = r.event_id AND m.user_id = r.user_id JOIN users u ON u.id=m.user_id
			 WHERE r.id = $1 AND r.team_id = $2 AND r.kind = 'request'`, refID, teamID).Scan(&name, &team)
	return name, team, err
}

// MailInvite reads a pending invitation's team.
func MailInvite(ctx context.Context, q sqlkit.Querier, refID, userID string) (string, error) {
	var team string
	err := q.QueryRowContext(ctx, `
			SELECT t.name FROM event_team_requests r JOIN event_teams t ON t.id = r.team_id
			 WHERE r.id = $1 AND r.user_id = $2 AND r.kind = 'invite'`, refID, userID).Scan(&team)
	return team, err
}

// NoticeTeamMessageBody reads a team message's text for the notice line
// (0003-hack-notices-judges-messages.sql). sql.ErrNoRows when the message or
// the team is gone, or the message is no longer that team's.
func NoticeTeamMessageBody(ctx context.Context, q sqlkit.Querier, id, teamID string) (string, error) {
	var body string
	err := q.QueryRowContext(ctx, `SELECT body FROM event_team_messages WHERE id = $1 AND team_id = $2`, id, teamID).Scan(&body)
	return body, err
}

// MailApplicationPending checks whether an application still awaits a decision.
func MailApplicationPending(ctx context.Context, q sqlkit.Querier, eventID, userID string) (bool, error) {
	var pending bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2
			AND role = 'participant' AND approval_status = 'pending')`, eventID, userID).Scan(&pending)
	return pending, err
}

// NoticeTeamInboundLine reads what a team wrote to the organisers, with the
// team's name, for the email line. sql.ErrNoRows when either is gone.
func NoticeTeamInboundLine(ctx context.Context, q sqlkit.Querier, id, teamID string) (string, string, error) {
	var name, body string
	err := q.QueryRowContext(ctx, `SELECT t.name, m.body FROM event_team_messages m JOIN event_teams t ON t.id = m.team_id
		WHERE m.id = $1 AND m.team_id = $2 AND m.from_team`, id, teamID).Scan(&name, &body)
	return name, body, err
}
