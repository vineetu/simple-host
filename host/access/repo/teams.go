package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/model"
	siterules "github.com/vsriram/simple-host/host/rules/sites"
)

// Teams (0025-teams.sql): a team is a users row with is_team set; people
// belong to it through team_members. Triggers keep keys off teams and keep
// membership rows pointing from a team to a person.

// GetTeamByHandle loads the team whose handle is handle (sql.ErrNoRows when
// no team has it, including when a person does). A value no handle could
// have (a NUL byte, a slash) is sql.ErrNoRows too, without a query.
func GetTeamByHandle(ctx context.Context, q Querier, handle string) (model.User, error) {
	var u model.User
	handle = strings.ToLower(strings.TrimSpace(handle))
	if !siterules.VisitorHandle.MatchString(handle) {
		return u, sql.ErrNoRows
	}
	err := q.QueryRowContext(ctx, `
		SELECT id, username, created_at, handle, display_name, suspended_at IS NOT NULL, COALESCE(suspended_reason, '')
		FROM users WHERE handle = $1 AND is_team`, handle).Scan(
		&u.ID, &u.Username, &u.CreatedAt, &u.Handle, &u.DisplayName, &u.Suspended, &u.SuspendedReason)
	return u, err
}

// LockTeam locks the team's row for a membership or lifecycle change, so two
// changes to one team run one after the other.
func LockTeam(ctx context.Context, tx *sql.Tx, teamID string) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 AND is_team FOR UPDATE`, teamID).Scan(&id)
}

// LockPerson locks a person's row while their team count is checked and
// changed (the 10-team limit).
func LockPerson(ctx context.Context, tx *sql.Tx, userID string) error {
	var id string
	return tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 AND NOT is_team FOR UPDATE`, userID).Scan(&id)
}

// InsertTeamAccount creates the account row of a new team: no key, no
// sign-in email (an address that can never receive mail), event_account set
// so it gets no mail, sign-in alerts or idle cleanup.
func InsertTeamAccount(ctx context.Context, tx *sql.Tx) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `
		INSERT INTO users (username, is_team, event_account)
		VALUES ('team+' || gen_random_uuid()::text || '@teams.invalid', TRUE, TRUE)
		RETURNING id`).Scan(&id)
	return id, err
}

// AddTeamMember adds userID to the team; false when they already belong.
func AddTeamMember(ctx context.Context, q Querier, teamID, userID, addedBy string) (bool, error) {
	res, err := q.ExecContext(ctx, `
		INSERT INTO team_members (team_id, user_id, added_by) VALUES ($1, $2, NULLIF($3, '')::uuid)
		ON CONFLICT DO NOTHING`, teamID, userID, addedBy)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// RemoveTeamMember removes userID from the team, and the invitations they
// sent that still wait for an answer; false when they did not belong.
func RemoveTeamMember(ctx context.Context, q Querier, teamID, userID string) (bool, error) {
	res, err := q.ExecContext(ctx, `WITH gone AS (DELETE FROM team_invitations WHERE team_id = $1 AND invited_by = $2 AND state = 'pending')
		DELETE FROM team_members WHERE team_id = $1 AND user_id = $2`, teamID, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// CountTeamMembers counts the team's members.
func CountTeamMembers(ctx context.Context, q Querier, teamID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM team_members WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

// CountTeamsOf counts the teams userID belongs to.
func CountTeamsOf(ctx context.Context, q Querier, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM team_members WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

// CountLiveSites counts the account's sites that are not in Recently
// deleted.
func CountLiveSites(ctx context.Context, q Querier, accountID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE user_id = $1 AND deleted_at IS NULL`, accountID).Scan(&n)
	return n, err
}

// ListTeamsOf lists the teams userID belongs to, by handle.
func ListTeamsOf(ctx context.Context, q Querier, userID string) ([]model.Team, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.handle, ''), u.created_at,
		       (SELECT count(*) FROM team_members x WHERE x.team_id = u.id),
		       (SELECT count(*) FROM sites s WHERE s.user_id = u.id AND s.deleted_at IS NULL)
		FROM team_members m JOIN users u ON u.id = m.team_id
		WHERE m.user_id = $1
		ORDER BY u.handle`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Team{}
	for rows.Next() {
		var t model.Team
		if err := rows.Scan(&t.ID, &t.Handle, &t.CreatedAt, &t.Members, &t.Sites); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTeamMembers lists the team's members, earliest first.
func ListTeamMembers(ctx context.Context, q Querier, teamID string) ([]model.TeamMember, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT u.id, COALESCE(u.handle, ''), COALESCE(u.display_name, ''), COALESCE(a.handle, ''), m.added_at
		FROM team_members m
		JOIN users u ON u.id = m.user_id
		LEFT JOIN users a ON a.id = m.added_by
		WHERE m.team_id = $1
		ORDER BY m.added_at, u.handle`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.TeamMember{}
	for rows.Next() {
		var m model.TeamMember
		var at time.Time
		if err := rows.Scan(&m.ID, &m.Handle, &m.DisplayName, &m.AddedBy, &at); err != nil {
			return nil, err
		}
		m.AddedAt = &at
		out = append(out, m)
	}
	return out, rows.Err()
}

// InsertTeamAudit records a team's creation or deletion, a member's removal,
// or an invitation being sent, cancelled, accepted or declined. subjectID is
// the person removed or joining ("" for none); invitationID the invitation
// ("" for none).
func InsertTeamAudit(ctx context.Context, q Querier, teamID, teamHandle, actorID, action, subjectID, invitationID string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO team_audit (team_id, team_handle, actor_id, action, subject_id, invitation_id)
		VALUES ($1, $2, NULLIF($3, '')::uuid, $4, NULLIF($5, '')::uuid, NULLIF($6, '')::uuid)`, teamID, teamHandle, actorID, action, subjectID, invitationID)
	return err
}

// TeamIDsOf lists the ids of the teams userID belongs to, in id order (the
// order their rows are locked in when the account is deleted).
func TeamIDsOf(ctx context.Context, q Querier, userID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT team_id FROM team_members WHERE user_id = $1 ORDER BY team_id`, userID)
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

// IsTeamAccount reports whether the account id is a team.
func IsTeamAccount(ctx context.Context, q Querier, id string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id::text = $1 AND is_team)`, id).Scan(&ok)
	return ok, err
}

// Invitations (team_invitations). An invitation is live while it is pending
// and unexpired; every read and answer below uses that one condition. A
// member who leaves takes their pending ones with them (RemoveTeamMember).
const liveInvitation = `i.state = 'pending' AND i.expires_at > now()`

// CountLiveInvitations counts the team's live invitations.
func CountLiveInvitations(ctx context.Context, q Querier, teamID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM team_invitations i WHERE i.team_id = $1 AND `+liveInvitation, teamID).Scan(&n)
	return n, err
}

// PutInvitation records an invitation to addressedTo, or renews the pending
// one already there (new inviter, new expiry), and returns its id.
func PutInvitation(ctx context.Context, q Querier, teamID, invitedBy, addressedTo string, ttl time.Duration) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		INSERT INTO team_invitations (team_id, invited_by, addressed_to, expires_at)
		VALUES ($1, $2, $3, now() + make_interval(secs => $4))
		ON CONFLICT (team_id, addressed_to) WHERE state = 'pending'
		DO UPDATE SET invited_by = EXCLUDED.invited_by, created_at = now(), expires_at = EXCLUDED.expires_at
		RETURNING id`, teamID, invitedBy, addressedTo, ttl.Seconds()).Scan(&id)
	return id, err
}

// ListTeamInvitations lists the team's live invitations, newest first.
func ListTeamInvitations(ctx context.Context, q Querier, teamID string) ([]model.TeamInvitation, error) {
	return scanInvitations(q.QueryContext(ctx, `
		SELECT i.id, t.handle, i.addressed_to, COALESCE(b.handle, ''), i.created_at, i.expires_at
		FROM team_invitations i JOIN users t ON t.id = i.team_id JOIN users b ON b.id = i.invited_by
		WHERE i.team_id = $1 AND `+liveInvitation+`
		ORDER BY i.created_at DESC`, teamID))
}

// InvitationsFor lists the live invitations addressed to email, a person's
// verified email ("" matches none; addressed_to is left out: it is them).
func InvitationsFor(ctx context.Context, q Querier, email string) ([]model.TeamInvitation, error) {
	return scanInvitations(q.QueryContext(ctx, `
		SELECT i.id, t.handle, '', COALESCE(b.handle, ''), i.created_at, i.expires_at
		FROM team_invitations i JOIN users t ON t.id = i.team_id JOIN users b ON b.id = i.invited_by
		WHERE i.addressed_to = $1 AND $1 <> '' AND `+liveInvitation+`
		ORDER BY i.created_at DESC`, email))
}

func scanInvitations(rows *sql.Rows, err error) ([]model.TeamInvitation, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.TeamInvitation{}
	for rows.Next() {
		var v model.TeamInvitation
		if err := rows.Scan(&v.ID, &v.Team, &v.AddressedTo, &v.InvitedBy, &v.CreatedAt, &v.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// InvitationFor loads, for an answer, the live invitation id addressed to
// email, a person's verified email: its team and inviter. sql.ErrNoRows when
// there is none (unknown, someone else's, answered, cancelled, expired, or
// its inviter has left).
func InvitationFor(ctx context.Context, q Querier, id, email string) (teamID, invitedBy string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT i.team_id, i.invited_by FROM team_invitations i
		WHERE i.id::text = $1 AND i.addressed_to = $2 AND $2 <> '' AND `+liveInvitation, id, email).Scan(&teamID, &invitedBy)
	return teamID, invitedBy, err
}

// SetInvitationState ends a pending invitation of the team (accepted,
// declined or cancelled); false when there was no pending one of that id.
func SetInvitationState(ctx context.Context, q Querier, teamID, id, state string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE team_invitations SET state = $3 WHERE id::text = $2 AND team_id = $1 AND state = 'pending'`, teamID, id, state)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// ReplaceTeamMembers makes the team's members exactly persons (a product's
// team, whose membership the product keeps).
func ReplaceTeamMembers(ctx context.Context, q Querier, teamID string, persons []string) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM team_members WHERE team_id = $1 AND NOT (user_id::text = ANY($2::text[]))`, teamID, pq.Array(persons)); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `
		INSERT INTO team_members (team_id, user_id) SELECT $1, p::uuid FROM unnest($2::text[]) p
		ON CONFLICT DO NOTHING`, teamID, pq.Array(persons))
	return err
}
