package hackevents

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

const (
	stageWorkflowSQL1 = `SELECT stage FROM events WHERE id = $1 FOR UPDATE`

	stageWorkflowSQL2 = `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`

	stageWorkflowSQL3 = `
				UPDATE events SET submission_deadline = clock_timestamp()
				 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`

	stageWorkflowSQL4 = `
				UPDATE events SET submission_deadline = NULL
				 WHERE id = $1 AND submission_deadline <= clock_timestamp()`

	stageWorkflowSQL5 = `
					UPDATE event_teams SET deadline_override = NULL
					 WHERE event_id = $1 AND deadline_override <= clock_timestamp()`

	stageWorkflowSQL6 = `SELECT e.stage, m.role, e.taken_down_at IS NOT NULL
		FROM events e JOIN event_members m ON m.event_id=e.id
		WHERE e.id=$1 AND m.user_id=$2 FOR UPDATE OF e FOR SHARE OF m`

	stageWorkflowSQL7 = `
			UPDATE events SET submission_deadline = clock_timestamp()
			 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`

	stageWorkflowSQL8 = `SELECT clock_timestamp()`
)

// savepointSQL starts a retryable operation.
const savepointSQL = "SAVEPOINT hack_try"

// rollbackSavepointSQL rolls back a failed attempt.
const rollbackSavepointSQL = "ROLLBACK TO SAVEPOINT hack_try"

// releaseSavepointSQL finishes a successful attempt.
const releaseSavepointSQL = "RELEASE SAVEPOINT hack_try"

// LockWorkflowStage locks the event and reads its stage.
func LockWorkflowStage(ctx context.Context, q *sql.Tx, eventID string) *sql.Row {
	return q.QueryRowContext(ctx, stageWorkflowSQL1, eventID)
}

// LockEventTeams locks the event’s team rows.
func LockEventTeams(ctx context.Context, q *sql.Tx, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL2, eventID)
}

// CloseSubmissionDeadline moves a future or missing submission deadline to now.
func CloseSubmissionDeadline(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL3, eventID)
}

// ClearPassedSubmissionDeadline clears a submission deadline that has passed.
func ClearPassedSubmissionDeadline(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL4, eventID)
}

// ClearPassedTeamDeadlines clears team deadline overrides that have passed.
func ClearPassedTeamDeadlines(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL5, eventID)
}

// LockStageAndMemberRole locks the event and shares the member lock while reading stage, role and takedown state.
func LockStageAndMemberRole(ctx context.Context, q *sql.Tx, eventID, userID string) *sql.Row {
	return q.QueryRowContext(ctx, stageWorkflowSQL6, eventID, userID)
}

// CloseDeadlineForStage moves a future or missing submission deadline to now during a stage move.
func CloseDeadlineForStage(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL7, eventID)
}

// DatabaseNow reads the database clock.
func DatabaseNow(ctx context.Context, q sqlkit.Querier) *sql.Row {
	return q.QueryRowContext(ctx, stageWorkflowSQL8)
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
