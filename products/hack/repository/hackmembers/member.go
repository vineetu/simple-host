package hackmembers

import (
	"context"
	"database/sql"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
)

type EventMember = model.EventMember

type EventPerson = model.EventPerson

// Lock is the row lock Member takes on the membership row.
type Lock int

const (
	NoLock Lock = iota
	ForShare
	ForUpdate
)

// memberColumns is the scan order of scanMember.
const memberColumns = `event_id, user_id, role, team_id, coc_accepted_at, joined_at,
	approval_status, joined_via, team_admin, looking, looking_intro, team_removal_notice`

// The role predicates, each stated once (unaliased columns of event_members).
const (
	isOrganiserSQL           = `role = 'organiser'`
	isParticipantSQL         = `role = 'participant'`
	isApprovedSQL            = `approval_status = 'approved'`
	isApprovedParticipantSQL = isParticipantSQL + ` AND ` + isApprovedSQL
	// isJudgeSQL takes the event's organiser_judges as the parameter it names.
	isJudgeSQLFmt = `(role = 'judge' OR (role = 'organiser' AND user_id = ANY(%s::uuid[])))`
)

func scanMember(row interface{ Scan(...any) error }) (EventMember, error) {
	var m EventMember
	err := row.Scan(&m.EventID, &m.UserID, &m.Role, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt,
		&m.ApprovalStatus, &m.JoinedVia, &m.TeamAdmin, &m.Looking, &m.LookingIntro, &m.TeamRemovalNotice)
	return m, err
}

// Member is who userID is in eventID, read under lock; sql.ErrNoRows when
// they are not a member. DisplayName is left empty (WithDisplayName fills it).
func Member(ctx context.Context, q sqlkit.Querier, eventID, userID string, lock Lock) (EventMember, error) {
	query := `SELECT ` + memberColumns + ` FROM event_members WHERE event_id = $1 AND user_id = $2`
	switch lock {
	case ForShare:
		query += ` FOR SHARE`
	case ForUpdate:
		query += ` FOR UPDATE`
	}
	return scanMember(q.QueryRowContext(ctx, query, eventID, userID))
}

// LockMembers locks these people's membership rows in one event, in user id
// order (the order every multi-row member lock takes).
func LockMembers(ctx context.Context, q sqlkit.Querier, eventID string, userIDs ...string) error {
	_, err := q.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = ANY($2::uuid[]) ORDER BY user_id FOR UPDATE`,
		eventID, pq.Array(userIDs))
	return err
}

// LockEventMembers locks every membership row of the event in user id order
// (filling teams).
func LockEventMembers(ctx context.Context, tx *sql.Tx, eventID string) error {
	_, err := tx.ExecContext(ctx, `SELECT 1 FROM event_members WHERE event_id = $1 ORDER BY user_id FOR UPDATE`, eventID)
	return err
}

// LockTeamMembers locks the team's membership rows in user id order and
// returns how many there are.
func LockTeamMembers(ctx context.Context, q sqlkit.Querier, teamID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT count(*) FROM (SELECT 1 FROM event_members WHERE team_id = $1 ORDER BY user_id FOR UPDATE) x`, teamID).Scan(&n)
	return n, err
}

// WithDisplayName fills a member's display name from its account;
// sql.ErrNoRows when the account is gone. It passes err through.
func WithDisplayName(ctx context.Context, q sqlkit.Querier, m EventMember, err error) (EventMember, error) {
	if err != nil {
		return m, err
	}
	u, err := accounts.UserByID(ctx, q, m.UserID)
	if err != nil {
		return EventMember{}, err
	}
	m.DisplayName = u.DisplayName.String
	return m, nil
}

// WithAccounts fills each person's sign-in address and display name, leaving
// out anyone whose account is gone.
func WithAccounts(ctx context.Context, q sqlkit.Querier, people []EventPerson) ([]EventPerson, error) {
	ids := make([]string, len(people))
	for i, p := range people {
		ids[i] = p.UserID
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	var out []EventPerson
	for _, p := range people {
		if u, ok := users[p.UserID]; ok {
			p.Email, p.DisplayName = u.Username, u.DisplayName.String
			out = append(out, p)
		}
	}
	return out, nil
}

// ids runs a query of one id column and returns the ids in order, never nil
// (a nil slice is SQL NULL, and NOT (x = ANY(NULL)) matches nothing).
func ids(ctx context.Context, q sqlkit.Querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MembersByIDs is which of these people are in the event, by user id.
func MembersByIDs(ctx context.Context, q sqlkit.Querier, eventID string, userIDs []string) (map[string]EventMember, error) {
	out := map[string]EventMember{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT `+memberColumns+` FROM event_members WHERE event_id = $1 AND user_id = ANY($2::uuid[])`, eventID, pq.Array(userIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out[m.UserID] = m
	}
	return out, rows.Err()
}
