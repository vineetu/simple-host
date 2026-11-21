package hackmembers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
)

// Filter picks membership rows. Every set is one event's (EventID), a few
// events' (EventIDs), one team's (TeamID) or one person's (UserID); the rest
// narrow it.
type Filter struct {
	EventID, TeamID, UserID string
	// EventIDs picks several events' members at once.
	EventIDs []string
	// Role is organiser, participant or judge; "" is any.
	Role string
	// Status is the approval status (approved, pending, rejected); "" is any.
	Status string
	// Via is how they joined (link, application); "" is any.
	Via       string
	OnTeam    bool // on some team
	NoTeam    bool // on no team
	TeamAdmin bool // admins of their team
	Looking   bool // looking for a team
	// Except leaves this person out (the one who made the change).
	Except string
}

func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	arg := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.EventID != "" {
		arg("event_id = $%d", f.EventID)
	}
	if len(f.EventIDs) > 0 {
		arg("event_id = ANY($%d::uuid[])", pq.Array(f.EventIDs))
	}
	if f.TeamID != "" {
		arg("team_id = $%d", f.TeamID)
	}
	if f.UserID != "" {
		arg("user_id = $%d", f.UserID)
	}
	if f.Role != "" {
		arg("role = $%d", f.Role)
	}
	if f.Status != "" {
		arg("approval_status = $%d", f.Status)
	}
	if f.Via != "" {
		arg("joined_via = $%d", f.Via)
	}
	if f.Except != "" {
		arg("user_id::text <> $%d", f.Except)
	}
	if f.OnTeam {
		conds = append(conds, "team_id IS NOT NULL")
	}
	if f.NoTeam {
		conds = append(conds, "team_id IS NULL")
	}
	if f.TeamAdmin {
		conds = append(conds, "team_admin")
	}
	if f.Looking {
		conds = append(conds, "looking")
	}
	if len(conds) == 0 {
		// Never every membership on the box.
		conds = append(conds, "false")
	}
	return strings.Join(conds, " AND "), args
}

// Members lists the rows f picks, oldest member first.
func Members(ctx context.Context, q sqlkit.Querier, f Filter) ([]EventMember, error) {
	where, args := f.where()
	rows, err := q.QueryContext(ctx, `SELECT `+memberColumns+` FROM event_members WHERE `+where+` ORDER BY joined_at, user_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventMember
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// IDs is the user ids of Members.
func IDs(ctx context.Context, q sqlkit.Querier, f Filter) ([]string, error) {
	where, args := f.where()
	return ids(ctx, q, `SELECT user_id FROM event_members WHERE `+where+` ORDER BY joined_at, user_id`, args...)
}

// Count is how many rows f picks.
func Count(ctx context.Context, q sqlkit.Querier, f Filter) (int, error) {
	where, args := f.where()
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_members WHERE `+where, args...).Scan(&n)
	return n, err
}

// Judges is the event's judges (Judge: joined judges and the organisers in
// organiserJudges), in user id order.
func Judges(ctx context.Context, q sqlkit.Querier, eventID string, organiserJudges []string) ([]EventMember, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+memberColumns+` FROM event_members
		WHERE event_id = $1 AND `+fmt.Sprintf(isJudgeSQLFmt, "$2")+` ORDER BY user_id`, eventID, pq.Array(organiserJudges))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventMember
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// JudgeIDs is the user ids of Judges.
func JudgeIDs(ctx context.Context, q sqlkit.Querier, eventID string, organiserJudges []string) ([]string, error) {
	judges, err := Judges(ctx, q, eventID, organiserJudges)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(judges))
	for i, j := range judges {
		out[i] = j.UserID
	}
	return out, nil
}

// TeamIDsWithMembers is the event's teams anyone is on; with eventID "",
// every event's.
func TeamIDsWithMembers(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	return ids(ctx, q, `SELECT DISTINCT team_id FROM event_members
		WHERE team_id IS NOT NULL AND (NULLIF($1, '')::uuid IS NULL OR event_id = NULLIF($1, '')::uuid) ORDER BY team_id`, eventID)
}

// TeamByTenure is the team's members, longest on the team first (who
// inherits the admin role).
func TeamByTenure(ctx context.Context, q sqlkit.Querier, teamID string) ([]string, error) {
	return ids(ctx, q, `SELECT user_id FROM event_members WHERE team_id = $1 ORDER BY team_joined_at NULLS LAST, user_id`, teamID)
}

// SoleMemberTeams is the teams userID is the only member of.
func SoleMemberTeams(ctx context.Context, q sqlkit.Querier, userID string) ([]string, error) {
	return ids(ctx, q, `SELECT team_id FROM event_members
		WHERE team_id IN (SELECT team_id FROM event_members WHERE user_id = $1 AND team_id IS NOT NULL)
		GROUP BY team_id HAVING count(*) = 1 ORDER BY team_id`, userID)
}

// RoleCounts is one event's head count for the admin list.
type RoleCounts struct {
	Participants, Judges int
	Organisers           []string
}

// JudgeCount is Judges' size given the event's organiser_judges.
func (c RoleCounts) JudgeCount(organiserJudges []string) int {
	n := c.Judges
	for _, id := range c.Organisers {
		for _, j := range organiserJudges {
			if id == j {
				n++
				break
			}
		}
	}
	return n
}

// CountRoles counts participants (any approval), joined judges and lists
// organisers, per event.
func CountRoles(ctx context.Context, q sqlkit.Querier, eventIDs []string) (map[string]RoleCounts, error) {
	rows, err := q.QueryContext(ctx, `SELECT event_id::text,
		count(*) FILTER (WHERE role = 'participant'), count(*) FILTER (WHERE role = 'judge'),
		COALESCE(array_agg(user_id::text) FILTER (WHERE `+isOrganiserSQL+`), '{}')
		FROM event_members WHERE event_id = ANY($1::uuid[]) GROUP BY event_id`, pq.Array(eventIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]RoleCounts{}
	for rows.Next() {
		var id string
		var c RoleCounts
		if err := rows.Scan(&id, &c.Participants, &c.Judges, pq.Array(&c.Organisers)); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// OrganiserIDs is the event's organisers in user id order.
func OrganiserIDs(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	out, err := IDs(ctx, q, Filter{EventID: eventID, Role: "organiser"})
	sort.Strings(out)
	return out, err
}
