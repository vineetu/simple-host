package hackentries

import (
	"context"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// JudgeDoneForTeam reports whether the judge scored every current criterion.
func JudgeDoneForTeam(ctx context.Context, q sqlkit.Querier, eventID, judgeID, teamID string, criteriaCount int) (bool, error) {
	if criteriaCount == 0 {
		return false, nil
	}
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT criterion_id) FROM event_scores
		 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`, eventID, judgeID, teamID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n >= criteriaCount, nil
}
