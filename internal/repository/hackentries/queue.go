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

// JudgeCompletionCounts batches the exact DISTINCT-criterion predicate used by
// JudgeDoneForTeam. An empty judgeID returns all judges for organiser guidance.
func JudgeCompletionCounts(ctx context.Context, q sqlkit.Querier, eventID, judgeID string) (map[string]map[string]int, error) {
	rows, err := queryContext(ctx, q, `SELECT judge_id, team_id, COUNT(DISTINCT criterion_id) FROM event_scores WHERE event_id=$1 AND ($2='' OR judge_id::text=$2) GROUP BY judge_id, team_id`, eventID, judgeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]int{}
	for rows.Next() {
		var judge, team string
		var n int
		if err := rows.Scan(&judge, &team, &n); err != nil {
			return nil, err
		}
		if out[judge] == nil {
			out[judge] = map[string]int{}
		}
		out[judge][team] = n
	}
	return out, rows.Err()
}
