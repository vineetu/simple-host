package hackentries

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// Keep this expression in sync with hackevents.EffectiveDeadlineSQL.
const effectiveDeadlineSQL = `CASE WHEN e.submission_deadline IS NULL THEN NULL ELSE GREATEST(e.submission_deadline, t.deadline_override) END`

func queryContext(ctx context.Context, q sqlkit.Querier, query string, args ...any) (*sql.Rows, error) {
	switch t := q.(type) {
	case *sql.DB:
		return t.QueryContext(ctx, query, args...)
	case *sql.Tx:
		return t.QueryContext(ctx, query, args...)
	default:
		return nil, fmt.Errorf("db: querier does not support QueryContext")
	}
}
