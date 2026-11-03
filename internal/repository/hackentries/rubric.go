package hackentries

import (
	"context"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// CountRubricScoresForDeletion counts scores affected by a rubric change.
func CountRubricScoresForDeletion(ctx context.Context, q sqlkit.Querier, eventID, criterionID string, remove bool, max int) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM event_scores WHERE event_id=$1 AND criterion_id=$2 AND ($3 OR points > $4)`, eventID, criterionID, remove, max).Scan(&count)
	return count, err
}
