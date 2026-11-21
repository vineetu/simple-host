package hackevents

import (
	"context"
	"database/sql"
	"errors"
	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackentries"
)

// OtherOrganiser aliases the shared model record.
type OtherOrganiser = model.OtherOrganiser

// FormerJudge aliases the shared model record.
type FormerJudge = model.FormerJudge

// LeaveOperations connects membership cleanup to the transitional team operations.
type LeaveOperations struct {
	LockMember        func(context.Context, sqlkit.Querier, string, string) (model.EventMember, error)
	LockTeam          func(context.Context, sqlkit.Querier, string) (model.EventTeam, error)
	DeleteTeamIfEmpty func(context.Context, sqlkit.Querier, string) error
	EnsureTeamAdmin   func(context.Context, sqlkit.Querier, string) error
}

// EffectiveDeadlineSQL is the team deadline expression over events e and event_teams t.
const EffectiveDeadlineSQL = `CASE WHEN e.submission_deadline IS NULL THEN NULL ELSE GREATEST(e.submission_deadline, t.deadline_override) END`

// TeamKeepableSQL is the predicate for deleting an empty team.
const TeamKeepableSQL = `t.pinned_at IS NULL AND (` + EffectiveDeadlineSQL + ` IS NULL OR ` + EffectiveDeadlineSQL + ` > clock_timestamp())`

// LeaveSelf loads one membership row plus its approval status, for GET/POST .../leave.
func LeaveSelf(ctx context.Context, q sqlkit.Querier, eventID, userID string, lock bool) (EventMember, string, error) {
	query := `SELECT m.event_id, m.user_id, m.role, COALESCE(u.display_name,''), m.team_id, m.coc_accepted_at, m.joined_at, approval_status
	            FROM event_members m JOIN users u ON u.id=m.user_id WHERE m.event_id = $1 AND m.user_id = $2`
	if lock {
		query += ` FOR UPDATE OF m`
	}
	var m EventMember
	var status string
	err := q.QueryRowContext(ctx, query, eventID, userID).Scan(
		&m.EventID, &m.UserID, &m.Role, &m.DisplayName, &m.TeamID, &m.CocAcceptedAt, &m.JoinedAt, &status)
	return m, status, err
}

// OtherEventOrganisers is every organiser of the event except userID, ordered by display name.
func OtherEventOrganisers(ctx context.Context, q sqlkit.Querier, eventID, userID string) ([]OtherOrganiser, error) {
	rows, err := queryContext(ctx, q, `
		SELECT m.user_id, COALESCE(u.display_name,''), u.username FROM event_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.event_id = $1 AND m.role = 'organiser' AND m.user_id <> $2
		 ORDER BY COALESCE(u.display_name,''), m.user_id`, eventID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OtherOrganiser{}
	for rows.Next() {
		var o OtherOrganiser
		if err := rows.Scan(&o.UserID, &o.DisplayName, &o.Email); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// TeamKeptIfEmptied reports whether, if leaving emptied this team right now, it would be kept rather than deleted.
func TeamKeptIfEmptied(ctx context.Context, q sqlkit.Querier, teamID string) (bool, error) {
	var kept bool
	err := q.QueryRowContext(ctx, `
		SELECT NOT (`+TeamKeepableSQL+`)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).Scan(&kept)
	return kept, err
}

// LeaveMembership is the shared core of a person losing their event membership, whether they chose it.
func LeaveMembership(ctx context.Context, q sqlkit.Querier, eventID, userID string, selfLeave bool, ops LeaveOperations) (string, error) {
	m, err := ops.LockMember(ctx, q, eventID, userID)
	if err != nil {
		return "", err
	}
	if m.TeamID.Valid {
		if _, err := ops.LockTeam(ctx, q, m.TeamID.String); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if err := dropMembership(ctx, q, eventID, userID); err != nil || !m.TeamID.Valid {
		return "", err
	}
	team := m.TeamID.String
	if err := ops.DeleteTeamIfEmpty(ctx, q, team); err != nil {
		return "", err
	}
	if selfLeave {
		if err := MarkTeamKeptAfterLeave(ctx, q, team); err != nil {
			return "", err
		}
	}
	if err := ops.EnsureTeamAdmin(ctx, q, team); err != nil {
		return "", err
	}
	return team, nil
}

// leaveQueuedSQL drops what was waiting on the leaver, each taking.
var leaveQueuedSQL = []string{
	`DELETE FROM event_team_requests WHERE event_id=$1 AND user_id=$2`,
	`DELETE FROM event_announcement_deliveries d USING event_announcements a
		WHERE d.announcement_id = a.id AND a.event_id = $1 AND d.user_id = $2 AND d.sent_at IS NULL`,
	`DELETE FROM event_entry_receipts WHERE event_id=$1 AND user_id=$2 AND sent_at IS NULL`,
	`DELETE FROM event_notices WHERE event_id=$1 AND user_id=$2`,
}

// leaveMemberSQL removes the leaver as an organiser judge and as a member.
var leaveMemberSQL = []string{
	`UPDATE events SET organiser_judges=array_remove(organiser_judges,$2::uuid) WHERE id=$1`,
	`DELETE FROM event_members WHERE event_id = $1 AND user_id = $2`,
}

// dropMembership reserves the event's name, drops everything queued for the leaver and their checklist acknowledgements, then the membership itself.
func dropMembership(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO event_used_names (event_slug, team_slug)
		SELECT slug, '' FROM events WHERE id=$1 ON CONFLICT DO NOTHING`, eventID); err != nil {
		return err
	}
	if err := execForMember(ctx, q, leaveQueuedSQL, eventID, userID); err != nil {
		return err
	}
	if err := DeletePersonalChecklistAcknowledgements(ctx, q, eventID, userID); err != nil {
		return err
	}
	return execForMember(ctx, q, leaveMemberSQL, eventID, userID)
}

// execForMember runs each statement with (event, user), stopping at the first error.
func execForMember(ctx context.Context, q sqlkit.Querier, stmts []string, eventID, userID string) error {
	for _, stmt := range stmts {
		if _, err := q.ExecContext(ctx, stmt, eventID, userID); err != nil {
			return err
		}
	}
	return nil
}

// markTeamKeptAfterLeave sets kept_after_leave on a team LeaveMembership's own deleteTeamIfEmpty call just kept instead of deleting.
func MarkTeamKeptAfterLeave(ctx context.Context, q sqlkit.Querier, teamID string) error {
	_, err := q.ExecContext(ctx, `
		UPDATE event_teams SET kept_after_leave = true, locked_at = COALESCE(locked_at, clock_timestamp())
		 WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM event_members WHERE team_id = $1)`, teamID)
	return err
}

// RecordFormerJudge freezes the teams a leaving judge scored and was eligible for, so leaving does not change what their scores count toward.
func RecordFormerJudge(ctx context.Context, q sqlkit.Querier, eventID, userID, role string) error {
	eligible, err := hackentries.EligibleJudgeTeamsIgnoringConflicts(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	rows, err := queryContext(ctx, q, `SELECT DISTINCT team_id FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	counted := []string{}
	for rows.Next() {
		var teamID string
		if err := rows.Scan(&teamID); err != nil {
			return err
		}
		if eligible[teamID] {
			counted = append(counted, teamID)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(counted) == 0 {
		return nil
	}
	_, err = q.ExecContext(ctx, `
		INSERT INTO event_former_judges (event_id, user_id, role, counted_team_ids, left_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (event_id, user_id) DO UPDATE SET
			role = EXCLUDED.role,
			counted_team_ids = EXCLUDED.counted_team_ids, left_at = EXCLUDED.left_at`,
		eventID, userID, role, pq.Array(counted))
	return err
}

// ListFormerJudges is every former judge of the event, oldest departure first.
func ListFormerJudges(ctx context.Context, q sqlkit.Querier, eventID string) ([]FormerJudge, error) {
	rows, err := queryContext(ctx, q, `
		SELECT f.user_id, COALESCE(u.display_name,''), f.role, f.left_at FROM event_former_judges f JOIN users u ON u.id=f.user_id
		 WHERE event_id = $1 ORDER BY left_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FormerJudge{}
	for rows.Next() {
		var f FormerJudge
		if err := rows.Scan(&f.UserID, &f.DisplayName, &f.Role, &f.LeftAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FormerJudgeAssignedCount is how many of today's judge-eligible teams this former judge would be assigned under the event's current mode, ignoring that their role is gone.
func FormerJudgeAssignedCount(ctx context.Context, q sqlkit.Querier, eventID, judgeID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM event_teams t JOIN events e ON e.id = t.event_id
		 WHERE e.id = $1 AND (EXISTS (SELECT 1 FROM event_members em WHERE em.team_id = t.id) OR t.kept_after_leave)
		   AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id = e.id AND c.judge_id = $2 AND c.team_id = t.id)
		   AND (e.judge_assignment_mode = 'open'
		    OR (e.judge_assignment_mode IN ('automatic', 'manual') AND EXISTS (SELECT 1 FROM event_assignments a WHERE a.event_id = e.id AND a.judge_id = $2 AND a.team_id = t.id))
		    OR (e.judge_assignment_mode = 'panel' AND EXISTS (SELECT 1 FROM event_track_panels p WHERE p.event_id = e.id AND p.track_id = t.track_id AND p.judge_id = $2)))`,
		eventID, judgeID).Scan(&n)
	return n, err
}

// DeleteFormerJudgeScores removes a departed judge's score rows and their former-judge record.
func DeleteFormerJudgeScores(ctx context.Context, q *sql.Tx, eventID, userID string) (bool, error) {
	var id string
	err := q.QueryRowContext(ctx, `
		SELECT user_id FROM event_former_judges WHERE event_id = $1 AND user_id = $2 FOR UPDATE`, eventID, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id = $1 AND judge_id = $2`, eventID, userID); err != nil {
		return false, err
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_former_judges WHERE event_id = $1 AND user_id = $2`, eventID, userID); err != nil {
		return false, err
	}
	return true, nil
}

// RemoveCoOrganiser drops an organiser.
func RemoveCoOrganiser(ctx context.Context, q *sql.Tx, eventID, actorID, userID string, ops LeaveOperations) error {
	// Serialise removals so a legacy event without its creator cannot lose both remaining organisers through concurrent requests.
	var createdBy sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT created_by FROM events WHERE id=$1 FOR UPDATE`, eventID).Scan(&createdBy); err != nil {
		return err
	}
	var role string
	err := q.QueryRowContext(ctx, `SELECT role FROM event_members WHERE event_id=$1 AND user_id=$2 FOR UPDATE`, eventID, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if role != "organiser" || (createdBy.Valid && createdBy.String == userID) {
		return sql.ErrNoRows
	}
	var actorIsOrganiser, anotherOrganiser bool
	if err := q.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND user_id=$2 AND role='organiser'),
		       EXISTS (SELECT 1 FROM event_members WHERE event_id=$1 AND role='organiser' AND user_id<>$3)`,
		eventID, actorID, userID).Scan(&actorIsOrganiser, &anotherOrganiser); err != nil {
		return err
	}
	if !actorIsOrganiser || !anotherOrganiser {
		return sql.ErrNoRows
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id=$1 AND judge_id=$2`, eventID, userID); err != nil {
		return err
	}
	_, err = LeaveMembership(ctx, q, eventID, userID, false, ops)
	return err
}

// ErrHackCannotRemoveOrganiser prevents removal through the participant path.
var ErrHackCannotRemoveOrganiser = errors.New("cannot remove organiser")

// RemovePersonFromEvent deletes a participant or judge.
func RemovePersonFromEvent(ctx context.Context, q sqlkit.Querier, eventID, userID string, ops LeaveOperations) error {
	m, err := ops.LockMember(ctx, q, eventID, userID)
	if err != nil {
		return err
	}
	if m.Role == "organiser" {
		return ErrHackCannotRemoveOrganiser
	}
	if m.Role == "judge" {
		if err := RecordFormerJudge(ctx, q, eventID, userID, m.Role); err != nil {
			return err
		}
	}
	_, err = LeaveMembership(ctx, q, eventID, userID, false, ops)
	return err
}

// UserEmail is users.username for a person (the address they signed in with).
func UserEmail(ctx context.Context, q sqlkit.Querier, userID string) (string, error) {
	var email string
	err := q.QueryRowContext(ctx, `SELECT username FROM users WHERE id = $1`, userID).Scan(&email)
	return email, err
}
