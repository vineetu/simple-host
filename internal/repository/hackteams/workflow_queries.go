package hackteams

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

import "github.com/vsriram/simple-host/internal/repository/hackevents"

const (
	// AdminSQL1 is a team workflow query.
	AdminSQL1 = `UPDATE event_teams SET looking_for = $2 WHERE id = $1`
	// CreateSQL1 is a team workflow query.
	CreateSQL1 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND lower(name) = lower($2))`
	// CreateSQL2 is a team workflow query.
	CreateSQL2 = `
		SELECT COALESCE(submission_deadline <= clock_timestamp(), false) FROM events WHERE id = $1 FOR SHARE`
	// CreateSQL3 is a team workflow query.
	CreateSQL3 = `UPDATE event_teams SET looking_for=$2 WHERE id=$1`
	// FindingSQL1 is a team workflow query.
	FindingSQL1 = `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved' FOR UPDATE`
	// FindingSQL2 is a team workflow query.
	FindingSQL2 = `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`
	// FindingSQL3 is a team workflow query.
	FindingSQL3 = `UPDATE event_teams SET looking_for=$2 WHERE id=$1`
	// FindingSQL4 is a team workflow query.
	FindingSQL4 = `UPDATE event_members SET looking=$3,looking_intro=$4 WHERE event_id=$1 AND user_id=$2 AND role='participant' AND approval_status='approved' AND team_id IS NULL`
	// FindingSQL5 is a team workflow query.
	FindingSQL5 = `SELECT team_id, user_id, kind FROM event_team_requests WHERE event_id = $1 AND id = $2`
	// FindingSQL6 is a team workflow query.
	FindingSQL6 = `SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`
	// FindingSQL7 is a team workflow query.
	FindingSQL7 = `SELECT id FROM event_team_requests WHERE team_id = $1 AND user_id = $2`
	// FindingSQL8 is a team workflow query.
	FindingSQL8 = `SELECT 1 FROM event_members WHERE event_id=$1 ORDER BY user_id FOR UPDATE`
	// FindingSQL9 is a team workflow query.
	FindingSQL9 = `SELECT 1 FROM event_teams WHERE event_id=$1 ORDER BY id FOR UPDATE`
	// LocksSQL1 is a team workflow query.
	LocksSQL1 = `SELECT stage FROM events WHERE id = $1 FOR SHARE`
	// LocksSQL2 is a team workflow query.
	LocksSQL2 = `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved' FOR UPDATE`
	// LocksSQL3 is a team workflow query.
	LocksSQL3 = `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`
	// LocksSQL4 is a team workflow query.
	LocksSQL4 = `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`
	// LocksSQL5 is a team workflow query.
	LocksSQL5 = `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id IN ($2, $3) ORDER BY user_id FOR UPDATE`
	// LocksSQL6 is a team workflow query.
	LocksSQL6 = `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved'`
	// LocksSQL7 is a team workflow query.
	LocksSQL7 = `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`
	// MembershipSQL1 is a team workflow query.
	MembershipSQL1 = `DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE team_id = $1)`
	// MembershipSQL2 is a team workflow query.
	MembershipSQL2 = `UPDATE event_members SET team_removal_notice=$2 WHERE team_id=$1`
	// OpenSQL1 is a team workflow query.
	OpenSQL1 = `SELECT t.id,t.slug,t.name,t.looking_for,
 COALESCE(x.id::text,''),COALESCE(x.slug,''),COALESCE(x.name,''),COALESCE(x.challenge,''),COALESCE(x.prize,'')
 FROM event_teams t JOIN events e ON e.id=t.event_id LEFT JOIN event_tracks x ON x.id=t.track_id
 WHERE t.event_id=$1 AND t.locked_at IS NULL
   AND e.stage NOT IN ('judging','results','archived')
   AND NOT COALESCE(` + hackevents.EffectiveDeadlineSQL + ` <= clock_timestamp(), false)
 ORDER BY t.created_at`
	// OpenSQL2 is a team workflow query.
	OpenSQL2 = `SELECT m.team_id,m.display_name FROM event_members m JOIN event_teams t ON t.id=m.team_id WHERE t.event_id=$1 AND t.locked_at IS NULL AND m.approval_status='approved' ORDER BY m.joined_at,m.user_id`
	// OpenSQL3 is a team workflow query.
	OpenSQL3 = `SELECT user_id,display_name,looking_intro FROM event_members WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL AND looking ORDER BY joined_at,user_id`
	// OpenSQL4 is a team workflow query.
	OpenSQL4 = `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,m.display_name,r.kind,r.created_at FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id JOIN event_members m ON m.event_id=r.event_id AND m.user_id=r.user_id WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`
	// OpenSQL5 is a team workflow query.
	OpenSQL5 = `SELECT looking,looking_intro FROM event_members WHERE event_id=$1 AND user_id=$2`
	// OpenSQL6 is a team workflow query.
	OpenSQL6 = `
			SELECT t.locked_at IS NOT NULL, t.looking_for, m.team_admin
			  FROM event_teams t JOIN event_members m ON m.team_id = t.id
			 WHERE t.id = $1 AND m.event_id = $2 AND m.user_id = $3`
	// OrganiserSQL1 is a team workflow query.
	OrganiserSQL1 = `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`
	// OrganiserSQL2 is a team workflow query.
	OrganiserSQL2 = `UPDATE event_teams SET code = $2 WHERE id = $1`
	// OrganiserSQL3 is a team workflow query.
	OrganiserSQL3 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`
	// OrganiserSQL4 is a team workflow query.
	OrganiserSQL4 = `SELECT 1 FROM event_members WHERE event_id=$1 AND user_id IN ($2,$3) ORDER BY user_id FOR UPDATE`
	// OrganiserSQL5 is a team workflow query.
	OrganiserSQL5 = `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved'`
	// OrganiserSQL6 is a team workflow query.
	OrganiserSQL6 = `SELECT slug FROM event_teams WHERE id=$1 FOR UPDATE`
	// OrganiserSQL7 is a team workflow query.
	OrganiserSQL7 = `UPDATE event_members SET team_removal_notice=$3 WHERE event_id=$1 AND user_id=$2`
	// OrganiserSQL8 is a team workflow query.
	OrganiserSQL8 = `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`
	// PeopleSQL1 is a team workflow query.
	PeopleSQL1 = `SELECT looking_for FROM event_teams WHERE id = $1`
)

// TeamDeadlinePassedSQL reads whether the effective deadline passed.
const TeamDeadlinePassedSQL = `
		SELECT COALESCE(` + hackevents.EffectiveDeadlineSQL + ` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`

// SavepointSQL starts a retryable operation.
const SavepointSQL = "SAVEPOINT hack_try"

// RollbackSavepointSQL rolls back a failed attempt.
const RollbackSavepointSQL = "ROLLBACK TO SAVEPOINT hack_try"

// ReleaseSavepointSQL finishes a successful attempt.
const ReleaseSavepointSQL = "RELEASE SAVEPOINT hack_try"

// RunFindingSQL1 executes the corresponding workflow query.
func RunFindingSQL1(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, FindingSQL1, args...)
}

// RunFindingSQL2 executes the corresponding workflow query.
func RunFindingSQL2(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, FindingSQL2, args...)
}

// RunFindingSQL3 executes the corresponding workflow query.
func RunFindingSQL3(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, FindingSQL3, args...)
}

// RunFindingSQL4 executes the corresponding workflow query.
func RunFindingSQL4(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, FindingSQL4, args...)
}

// RunFindingSQL5 executes the corresponding workflow query.
func RunFindingSQL5(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, FindingSQL5, args...)
}

// RunFindingSQL6 executes the corresponding workflow query.
func RunFindingSQL6(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, FindingSQL6, args...)
}

// RunFindingSQL7 executes the corresponding workflow query.
func RunFindingSQL7(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, FindingSQL7, args...)
}

// RunFindingSQL8 executes the corresponding workflow query.
func RunFindingSQL8(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, FindingSQL8, args...)
}

// RunFindingSQL9 executes the corresponding workflow query.
func RunFindingSQL9(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, FindingSQL9, args...)
}

// RunAdminSQL1 executes the corresponding workflow query.
func RunAdminSQL1(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, AdminSQL1, args...)
}

// RunOpenSQL1 executes the corresponding workflow query.
func RunOpenSQL1(ctx context.Context, q *sql.DB, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, OpenSQL1, args...)
}

// RunOpenSQL2 executes the corresponding workflow query.
func RunOpenSQL2(ctx context.Context, q *sql.DB, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, OpenSQL2, args...)
}

// RunOpenSQL3 executes the corresponding workflow query.
func RunOpenSQL3(ctx context.Context, q *sql.DB, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, OpenSQL3, args...)
}

// RunOpenSQL4 executes the corresponding workflow query.
func RunOpenSQL4(ctx context.Context, q *sql.DB, args ...any) (*sql.Rows, error) {
	return q.QueryContext(ctx, OpenSQL4, args...)
}

// RunOpenSQL5 executes the corresponding workflow query.
func RunOpenSQL5(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OpenSQL5, args...)
}

// RunOpenSQL6 executes the corresponding workflow query.
func RunOpenSQL6(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OpenSQL6, args...)
}

// RunMembershipSQL1 executes the corresponding workflow query.
func RunMembershipSQL1(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, MembershipSQL1, args...)
}

// RunMembershipSQL2 executes the corresponding workflow query.
func RunMembershipSQL2(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, MembershipSQL2, args...)
}

// RunLocksSQL1 executes the corresponding workflow query.
func RunLocksSQL1(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, LocksSQL1, args...)
}

// RunLocksSQL2 executes the corresponding workflow query.
func RunLocksSQL2(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, LocksSQL2, args...)
}

// RunLocksSQL3 executes the corresponding workflow query.
func RunLocksSQL3(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, LocksSQL3, args...)
}

// RunLocksSQL4 executes the corresponding workflow query.
func RunLocksSQL4(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, LocksSQL4, args...)
}

// RunLocksSQL5 executes the corresponding workflow query.
func RunLocksSQL5(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, LocksSQL5, args...)
}

// RunLocksSQL6 executes the corresponding workflow query.
func RunLocksSQL6(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, LocksSQL6, args...)
}

// RunLocksSQL7 executes the corresponding workflow query.
func RunLocksSQL7(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, LocksSQL7, args...)
}

// RunOrganiserSQL2 executes the corresponding workflow query.
func RunOrganiserSQL2(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, OrganiserSQL2, args...)
}

// RunOrganiserSQL3 executes the corresponding workflow query.
func RunOrganiserSQL3(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OrganiserSQL3, args...)
}

// RunOrganiserSQL4 executes the corresponding workflow query.
func RunOrganiserSQL4(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, OrganiserSQL4, args...)
}

// RunOrganiserSQL5 executes the corresponding workflow query.
func RunOrganiserSQL5(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OrganiserSQL5, args...)
}

// RunOrganiserSQL6 executes the corresponding workflow query.
func RunOrganiserSQL6(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OrganiserSQL6, args...)
}

// RunOrganiserSQL7 executes the corresponding workflow query.
func RunOrganiserSQL7(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, OrganiserSQL7, args...)
}

// RunOrganiserSQL8 executes the corresponding workflow query.
func RunOrganiserSQL8(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OrganiserSQL8, args...)
}

// RunTeamDeadlinePassedSQL executes the corresponding workflow query.
func RunTeamDeadlinePassedSQL(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, TeamDeadlinePassedSQL, args...)
}

// RunCreateSQL1 executes the corresponding workflow query.
func RunCreateSQL1(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, CreateSQL1, args...)
}

// RunSavepointSQL executes the corresponding workflow query.
func RunSavepointSQL(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, SavepointSQL, args...)
}

// RunRollbackSavepointSQL executes the corresponding workflow query.
func RunRollbackSavepointSQL(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, RollbackSavepointSQL, args...)
}

// RunReleaseSavepointSQL executes the corresponding workflow query.
func RunReleaseSavepointSQL(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, ReleaseSavepointSQL, args...)
}

// RunOrganiserSQL1 executes the corresponding workflow query.
func RunOrganiserSQL1(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, OrganiserSQL1, args...)
}

// RunPeopleSQL1 executes the corresponding workflow query.
func RunPeopleSQL1(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, PeopleSQL1, args...)
}

// RunCreateSQL2 executes the corresponding workflow query.
func RunCreateSQL2(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, CreateSQL2, args...)
}

// RunCreateSQL3 executes the corresponding workflow query.
func RunCreateSQL3(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, CreateSQL3, args...)
}
