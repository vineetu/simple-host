package hackteams

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

import "github.com/vsriram/simple-host/products/hack/repository/hackevents"

const (
	adminSQL1 = `UPDATE event_teams SET looking_for = $2 WHERE id = $1`

	createSQL1 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE event_id = $1 AND lower(name) = lower($2))`

	createSQL2 = `
		SELECT COALESCE(submission_deadline <= clock_timestamp(), false) FROM events WHERE id = $1 FOR SHARE`

	createSQL3 = `UPDATE event_teams SET looking_for=$2 WHERE id=$1`

	findingSQL1 = `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved' FOR UPDATE`

	findingSQL2 = `SELECT 1 FROM event_teams WHERE id=$1 FOR UPDATE`

	findingSQL3 = `UPDATE event_teams SET looking_for=$2 WHERE id=$1`

	findingSQL4 = `UPDATE event_members SET looking=$3,looking_intro=$4 WHERE event_id=$1 AND user_id=$2 AND role='participant' AND approval_status='approved' AND team_id IS NULL`

	findingSQL5 = `SELECT team_id, user_id, kind FROM event_team_requests WHERE event_id = $1 AND id = $2`

	findingSQL6 = `SELECT id FROM event_teams WHERE event_id = $1 AND slug = $2`

	findingSQL7 = `SELECT id FROM event_team_requests WHERE team_id = $1 AND user_id = $2`

	findingSQL8 = `SELECT 1 FROM event_members WHERE event_id=$1 ORDER BY user_id FOR UPDATE`

	findingSQL9 = `SELECT 1 FROM event_teams WHERE event_id=$1 ORDER BY id FOR UPDATE`

	locksSQL1 = `SELECT stage FROM events WHERE id = $1 FOR SHARE`

	locksSQL2 = `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved' FOR UPDATE`

	locksSQL3 = `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`

	locksSQL4 = `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`

	locksSQL5 = `SELECT 1 FROM event_members WHERE event_id = $1 AND user_id IN ($2, $3) ORDER BY user_id FOR UPDATE`

	locksSQL6 = `
		SELECT team_id, team_admin FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND approval_status = 'approved'`

	locksSQL7 = `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`

	membershipSQL1 = `DELETE FROM api_keys WHERE id IN (SELECT key_id FROM event_team_keys WHERE team_id = $1)`

	membershipSQL2 = `UPDATE event_members SET team_removal_notice=$2 WHERE team_id=$1`

	openSQL1 = `SELECT t.id,t.slug,t.name,t.looking_for,
 COALESCE(x.id::text,''),COALESCE(x.slug,''),COALESCE(x.name,''),COALESCE(x.challenge,''),COALESCE(x.prize,'')
 FROM event_teams t JOIN events e ON e.id=t.event_id LEFT JOIN event_tracks x ON x.id=t.track_id
 WHERE t.event_id=$1 AND t.locked_at IS NULL
   AND e.stage NOT IN ('judging','results','archived')
   AND NOT COALESCE(` + hackevents.EffectiveDeadlineSQL + ` <= clock_timestamp(), false)
 ORDER BY t.created_at`

	openSQL2 = `SELECT m.team_id,COALESCE(u.display_name,'') FROM event_members m JOIN users u ON u.id=m.user_id JOIN event_teams t ON t.id=m.team_id WHERE t.event_id=$1 AND t.locked_at IS NULL AND m.approval_status='approved' ORDER BY m.joined_at,m.user_id`

	openSQL3 = `SELECT m.user_id,COALESCE(u.display_name,''),looking_intro FROM event_members m JOIN users u ON u.id=m.user_id WHERE event_id=$1 AND role='participant' AND approval_status='approved' AND team_id IS NULL AND looking ORDER BY joined_at,user_id`

	openSQL4 = `SELECT r.id,r.team_id,t.slug,t.name,r.user_id,COALESCE(u.display_name,''),r.kind,r.created_at FROM event_team_requests r JOIN event_teams t ON t.id=r.team_id JOIN event_members m ON m.event_id=r.event_id AND m.user_id=r.user_id JOIN users u ON u.id=m.user_id WHERE r.event_id=$1 AND (r.user_id=$2 OR r.team_id=$3 OR $4) ORDER BY r.created_at`

	openSQL5 = `SELECT looking,looking_intro FROM event_members WHERE event_id=$1 AND user_id=$2`

	openSQL6 = `
			SELECT t.locked_at IS NOT NULL, t.looking_for, m.team_admin
			  FROM event_teams t JOIN event_members m ON m.team_id = t.id
			 WHERE t.id = $1 AND m.event_id = $2 AND m.user_id = $3`

	organiserSQL1 = `SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 FOR UPDATE`

	organiserSQL2 = `UPDATE event_teams SET code = $2 WHERE id = $1`

	organiserSQL3 = `SELECT EXISTS (SELECT 1 FROM event_teams WHERE id = $1)`

	organiserSQL4 = `SELECT 1 FROM event_members WHERE event_id=$1 AND user_id IN ($2,$3) ORDER BY user_id FOR UPDATE`

	organiserSQL5 = `SELECT team_id, team_admin FROM event_members WHERE event_id=$1 AND user_id=$2 AND approval_status='approved'`

	organiserSQL6 = `SELECT slug FROM event_teams WHERE id=$1 FOR UPDATE`

	organiserSQL7 = `UPDATE event_members SET team_removal_notice=$3 WHERE event_id=$1 AND user_id=$2`

	organiserSQL8 = `SELECT team_removal_notice FROM event_members WHERE event_id=$1 AND user_id=$2`

	peopleSQL1 = `SELECT looking_for FROM event_teams WHERE id = $1`
)

// teamDeadlinePassedSQL reads whether the effective deadline passed.
const teamDeadlinePassedSQL = `
		SELECT COALESCE(` + hackevents.EffectiveDeadlineSQL + ` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`

// savepointSQL starts a retryable operation.
const savepointSQL = "SAVEPOINT hack_try"

// rollbackSavepointSQL rolls back a failed attempt.
const rollbackSavepointSQL = "ROLLBACK TO SAVEPOINT hack_try"

// releaseSavepointSQL finishes a successful attempt.
const releaseSavepointSQL = "RELEASE SAVEPOINT hack_try"

// LockFindingMembership locks an approved membership and reads its team and admin state.
func LockFindingMembership(ctx context.Context, q *sql.Tx, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL1, eventID, userID)
}

// LockFindingTeam locks the team before editing its finding note.
func LockFindingTeam(ctx context.Context, q *sql.Tx, teamID string) (sql.Result, error) {
	return q.ExecContext(ctx, findingSQL2, teamID)
}

// SetFindingNote updates the team’s finding note.
func SetFindingNote(ctx context.Context, q sqlkit.Querier, teamID, note string) (sql.Result, error) {
	return q.ExecContext(ctx, findingSQL3, teamID, note)
}

// SetMemberLooking updates an approved solo participant’s finding preferences.
func SetMemberLooking(ctx context.Context, q sqlkit.Querier, eventID, userID string, looking bool, intro string) (sql.Result, error) {
	return q.ExecContext(ctx, findingSQL4, eventID, userID, looking, intro)
}

// ReadTeamRequest reads the team, user and kind of a request.
func ReadTeamRequest(ctx context.Context, q sqlkit.Querier, eventID, requestID string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL5, eventID, requestID)
}

// TeamIDBySlug reads a team ID by event and slug.
func TeamIDBySlug(ctx context.Context, q sqlkit.Querier, eventID, slug string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL6, eventID, slug)
}

// TeamRequestID reads the request ID for a team and user.
func TeamRequestID(ctx context.Context, q sqlkit.Querier, teamID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, findingSQL7, teamID, userID)
}

// LockEventMembersForFill locks event members in user ID order for team filling.
func LockEventMembersForFill(ctx context.Context, q *sql.Tx, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, findingSQL8, eventID)
}

// LockEventTeamsForFill locks event teams in ID order for team filling.
func LockEventTeamsForFill(ctx context.Context, q *sql.Tx, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, findingSQL9, eventID)
}

// SetTeamLookingFor updates what a team is looking for.
func SetTeamLookingFor(ctx context.Context, q sqlkit.Querier, teamID, note string) (sql.Result, error) {
	return q.ExecContext(ctx, adminSQL1, teamID, note)
}

// ListFindingTeams lists unlocked teams eligible for the finding directory.
func ListFindingTeams(ctx context.Context, q *sql.DB, eventID string) (*sql.Rows, error) {
	return q.QueryContext(ctx, openSQL1, eventID)
}

// ListFindingTeamMembers lists approved members of unlocked teams.
func ListFindingTeamMembers(ctx context.Context, q *sql.DB, eventID string) (*sql.Rows, error) {
	return q.QueryContext(ctx, openSQL2, eventID)
}

// ListLookingParticipants lists approved solo participants seeking a team.
func ListLookingParticipants(ctx context.Context, q *sql.DB, eventID string) (*sql.Rows, error) {
	return q.QueryContext(ctx, openSQL3, eventID)
}

// ListVisibleTeamRequests lists requests visible to the member or organiser.
func ListVisibleTeamRequests(ctx context.Context, q *sql.DB, eventID, userID string, teamID sql.NullString, organiser bool) (*sql.Rows, error) {
	return q.QueryContext(ctx, openSQL4, eventID, userID, teamID, organiser)
}

// ReadMemberLooking reads a member’s finding preferences.
func ReadMemberLooking(ctx context.Context, q sqlkit.Querier, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, openSQL5, eventID, userID)
}

// ReadFindingTeamState reads the team’s lock and finding state and the member’s admin state.
func ReadFindingTeamState(ctx context.Context, q sqlkit.Querier, teamID sql.NullString, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, openSQL6, teamID, eventID, userID)
}

// DeleteTeamAPIKeys deletes API keys belonging to a team.
func DeleteTeamAPIKeys(ctx context.Context, q sqlkit.Querier, teamID string) (sql.Result, error) {
	return q.ExecContext(ctx, membershipSQL1, teamID)
}

// SetTeamRemovalNotices sets the removal notice for team members.
func SetTeamRemovalNotices(ctx context.Context, q sqlkit.Querier, teamID, notice string) (sql.Result, error) {
	return q.ExecContext(ctx, membershipSQL2, teamID, notice)
}

// LockEventStageShared takes a shared event lock and reads its stage.
func LockEventStageShared(ctx context.Context, q *sql.Tx, eventID string) *sql.Row {
	return q.QueryRowContext(ctx, locksSQL1, eventID)
}

// LockApprovedMemberTeam locks an approved membership and reads its team and admin state.
func LockApprovedMemberTeam(ctx context.Context, q *sql.Tx, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, locksSQL2, eventID, userID)
}

// LockCallerTeamRow locks the caller’s team row.
func LockCallerTeamRow(ctx context.Context, q *sql.Tx, teamID string) (sql.Result, error) {
	return q.ExecContext(ctx, locksSQL3, teamID)
}

// LockSingleMemberRow locks one event member row.
func LockSingleMemberRow(ctx context.Context, q *sql.Tx, eventID, userID string) (sql.Result, error) {
	return q.ExecContext(ctx, locksSQL4, eventID, userID)
}

// LockTwoMemberRows locks two event member rows in user ID order.
func LockTwoMemberRows(ctx context.Context, q *sql.Tx, eventID, firstUserID, secondUserID string) (sql.Result, error) {
	return q.ExecContext(ctx, locksSQL5, eventID, firstUserID, secondUserID)
}

// ReadApprovedMemberTeam reads an approved member’s team and admin state.
func ReadApprovedMemberTeam(ctx context.Context, q sqlkit.Querier, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, locksSQL6, eventID, userID)
}

// LockTargetTeamRow locks the caller’s team after the member rows.
func LockTargetTeamRow(ctx context.Context, q *sql.Tx, teamID string) (sql.Result, error) {
	return q.ExecContext(ctx, locksSQL7, teamID)
}

// SetTeamCode updates a team’s join code.
func SetTeamCode(ctx context.Context, q sqlkit.Querier, teamID, code string) (sql.Result, error) {
	return q.ExecContext(ctx, organiserSQL2, teamID, code)
}

// TeamRowExists checks whether a team still exists.
func TeamRowExists(ctx context.Context, q sqlkit.Querier, teamID string) *sql.Row {
	return q.QueryRowContext(ctx, organiserSQL3, teamID)
}

// LockRemovalMembers locks the actor and target member rows in user ID order.
func LockRemovalMembers(ctx context.Context, q *sql.Tx, eventID, actorID, userID string) (sql.Result, error) {
	return q.ExecContext(ctx, organiserSQL4, eventID, actorID, userID)
}

// ReadRemovalActor reads the approved actor’s team and admin state.
func ReadRemovalActor(ctx context.Context, q sqlkit.Querier, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, organiserSQL5, eventID, userID)
}

// LockTeamSlug locks a team and reads its slug.
func LockTeamSlug(ctx context.Context, q *sql.Tx, teamID string) *sql.Row {
	return q.QueryRowContext(ctx, organiserSQL6, teamID)
}

// SetMemberRemovalNotice sets a member’s removal notice.
func SetMemberRemovalNotice(ctx context.Context, q sqlkit.Querier, eventID, userID, notice string) (sql.Result, error) {
	return q.ExecContext(ctx, organiserSQL7, eventID, userID, notice)
}

// ReadMemberRemovalNotice reads a member’s removal notice.
func ReadMemberRemovalNotice(ctx context.Context, q sqlkit.Querier, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, organiserSQL8, eventID, userID)
}

// TeamDeadlinePassed reads whether the team’s effective deadline has passed.
func TeamDeadlinePassed(ctx context.Context, q sqlkit.Querier, teamID string) *sql.Row {
	return q.QueryRowContext(ctx, teamDeadlinePassedSQL, teamID)
}

// TeamNameExists checks for a case-insensitive team name match.
func TeamNameExists(ctx context.Context, q sqlkit.Querier, eventID, name string) *sql.Row {
	return q.QueryRowContext(ctx, createSQL1, eventID, name)
}

// StartWorkflowSavepoint starts the workflow retry savepoint.
func StartWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, savepointSQL)
}

// RollbackWorkflowSavepoint rolls back the workflow retry savepoint.
func RollbackWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, rollbackSavepointSQL)
}

// ReleaseWorkflowSavepoint releases the workflow retry savepoint.
func ReleaseWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, releaseSavepointSQL)
}

// LockMemberTeamID locks a membership and reads its team ID.
func LockMemberTeamID(ctx context.Context, q *sql.Tx, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, organiserSQL1, eventID, userID)
}

// ReadTeamLookingFor reads what a team is looking for.
func ReadTeamLookingFor(ctx context.Context, q sqlkit.Querier, teamID string) *sql.Row {
	return q.QueryRowContext(ctx, peopleSQL1, teamID)
}

// LockEventDeadlinePassed takes a shared event lock and reads whether its deadline has passed.
func LockEventDeadlinePassed(ctx context.Context, q *sql.Tx, eventID string) *sql.Row {
	return q.QueryRowContext(ctx, createSQL2, eventID)
}

// SetCreatedTeamLookingFor sets the finding note on a newly created team.
func SetCreatedTeamLookingFor(ctx context.Context, q sqlkit.Querier, teamID, note string) (sql.Result, error) {
	return q.ExecContext(ctx, createSQL3, teamID, note)
}
