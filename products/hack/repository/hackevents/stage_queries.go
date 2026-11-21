package hackevents

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/host/sqlkit"
)

const (
	stageWorkflowSQL1 = `SELECT stage FROM events WHERE id = $1 FOR UPDATE`

	stageWorkflowSQL3 = `
				UPDATE events SET submission_deadline = clock_timestamp()
				 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`

	stageWorkflowSQL4 = `
				UPDATE events SET submission_deadline = NULL
				 WHERE id = $1 AND submission_deadline <= clock_timestamp()`

	stageWorkflowSQL6 = `SELECT stage, taken_down_at IS NOT NULL FROM events WHERE id=$1 FOR UPDATE`

	stageWorkflowSQL7 = `
			UPDATE events SET submission_deadline = clock_timestamp()
			 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`

	stageWorkflowSQL8 = `SELECT clock_timestamp()`
)

const savepointSQL = "SAVEPOINT hack_try"

const rollbackSavepointSQL = "ROLLBACK TO SAVEPOINT hack_try"

const releaseSavepointSQL = "RELEASE SAVEPOINT hack_try"

func LockWorkflowStage(ctx context.Context, q *sql.Tx, eventID string) *sql.Row {
	return q.QueryRowContext(ctx, stageWorkflowSQL1, eventID)
}

func CloseSubmissionDeadline(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL3, eventID)
}

func ClearPassedSubmissionDeadline(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL4, eventID)
}

// LockStageTakenDown locks the event and reads its stage and takedown state.
func LockStageTakenDown(ctx context.Context, q *sql.Tx, eventID string) *sql.Row {
	return q.QueryRowContext(ctx, stageWorkflowSQL6, eventID)
}

func CloseDeadlineForStage(ctx context.Context, q sqlkit.Querier, eventID string) (sql.Result, error) {
	return q.ExecContext(ctx, stageWorkflowSQL7, eventID)
}

func DatabaseNow(ctx context.Context, q sqlkit.Querier) *sql.Row {
	return q.QueryRowContext(ctx, stageWorkflowSQL8)
}

func StartWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, savepointSQL)
}

func RollbackWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, rollbackSavepointSQL)
}

func ReleaseWorkflowSavepoint(ctx context.Context, q sqlkit.Querier) (sql.Result, error) {
	return q.ExecContext(ctx, releaseSavepointSQL)
}
