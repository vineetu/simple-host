package hackevents

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

const (
	// StageWorkflowSQL1 is an event workflow query.
	StageWorkflowSQL1 = `SELECT stage FROM events WHERE id = $1 FOR UPDATE`
	// StageWorkflowSQL2 is an event workflow query.
	StageWorkflowSQL2 = `SELECT 1 FROM event_teams WHERE event_id = $1 FOR UPDATE`
	// StageWorkflowSQL3 is an event workflow query.
	StageWorkflowSQL3 = `
				UPDATE events SET submission_deadline = clock_timestamp()
				 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`
	// StageWorkflowSQL4 is an event workflow query.
	StageWorkflowSQL4 = `
				UPDATE events SET submission_deadline = NULL
				 WHERE id = $1 AND submission_deadline <= clock_timestamp()`
	// StageWorkflowSQL5 is an event workflow query.
	StageWorkflowSQL5 = `
					UPDATE event_teams SET deadline_override = NULL
					 WHERE event_id = $1 AND deadline_override <= clock_timestamp()`
	// StageWorkflowSQL6 is an event workflow query.
	StageWorkflowSQL6 = `SELECT e.stage, m.role, e.taken_down_at IS NOT NULL
		FROM events e JOIN event_members m ON m.event_id=e.id
		WHERE e.id=$1 AND m.user_id=$2 FOR UPDATE OF e FOR SHARE OF m`
	// StageWorkflowSQL7 is an event workflow query.
	StageWorkflowSQL7 = `
			UPDATE events SET submission_deadline = clock_timestamp()
			 WHERE id = $1 AND (submission_deadline IS NULL OR submission_deadline > clock_timestamp())`
	// StageWorkflowSQL8 is an event workflow query.
	StageWorkflowSQL8 = `SELECT clock_timestamp()`
)

// SavepointSQL starts a retryable operation.
const SavepointSQL = "SAVEPOINT hack_try"

// RollbackSavepointSQL rolls back a failed attempt.
const RollbackSavepointSQL = "ROLLBACK TO SAVEPOINT hack_try"

// ReleaseSavepointSQL finishes a successful attempt.
const ReleaseSavepointSQL = "RELEASE SAVEPOINT hack_try"

// RunStageWorkflowSQL1 executes the corresponding workflow query.
func RunStageWorkflowSQL1(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, StageWorkflowSQL1, args...)
}

// RunStageWorkflowSQL2 executes the corresponding workflow query.
func RunStageWorkflowSQL2(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, StageWorkflowSQL2, args...)
}

// RunStageWorkflowSQL3 executes the corresponding workflow query.
func RunStageWorkflowSQL3(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, StageWorkflowSQL3, args...)
}

// RunStageWorkflowSQL4 executes the corresponding workflow query.
func RunStageWorkflowSQL4(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, StageWorkflowSQL4, args...)
}

// RunStageWorkflowSQL5 executes the corresponding workflow query.
func RunStageWorkflowSQL5(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, StageWorkflowSQL5, args...)
}

// RunStageWorkflowSQL6 executes the corresponding workflow query.
func RunStageWorkflowSQL6(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, StageWorkflowSQL6, args...)
}

// RunStageWorkflowSQL7 executes the corresponding workflow query.
func RunStageWorkflowSQL7(ctx context.Context, q sqlkit.Querier, args ...any) (sql.Result, error) {
	return q.ExecContext(ctx, StageWorkflowSQL7, args...)
}

// RunStageWorkflowSQL8 executes the corresponding workflow query.
func RunStageWorkflowSQL8(ctx context.Context, q sqlkit.Querier, args ...any) *sql.Row {
	return q.QueryRowContext(ctx, StageWorkflowSQL8, args...)
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
