package hackentries

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type FormerJudge = model.FormerJudge

// RecordFormerJudge freezes the teams a leaving judge scored and was eligible for, so leaving does not change what their scores count toward.
func RecordFormerJudge(ctx context.Context, q sqlkit.Querier, eventID, userID, role string) error {
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil {
		return err
	}
	eligible, err := EligibleJudgeTeamsIgnoringConflicts(ctx, q, ev, userID)
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

func ListFormerJudges(ctx context.Context, q sqlkit.Querier, eventID string) ([]FormerJudge, error) {
	rows, err := queryContext(ctx, q, `
		SELECT f.user_id, f.role, f.left_at FROM event_former_judges f
		 WHERE event_id = $1 ORDER BY left_at ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []FormerJudge
	var ids []string
	for rows.Next() {
		var f FormerJudge
		if err := rows.Scan(&f.UserID, &f.Role, &f.LeftAt); err != nil {
			return nil, err
		}
		found, ids = append(found, f), append(ids, f.UserID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	out := []FormerJudge{}
	for _, f := range found {
		if u, ok := users[f.UserID]; ok {
			f.DisplayName = u.DisplayName.String
			out = append(out, f)
		}
	}
	return out, nil
}

// FormerJudgeAssignedCount is how many of today's judge-eligible teams this former judge would be assigned under the event's current mode, ignoring that their role is gone.
func FormerJudgeAssignedCount(ctx context.Context, q sqlkit.Querier, ev model.Event, judgeID string) (int, error) {
	teams, err := hackteams.JudgedTeams(ctx, q, ev.ID)
	if err != nil {
		return 0, err
	}
	var n int
	err = q.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM `+teamRows(4)+`
		 WHERE t.event_id = $1
		   AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id = t.event_id AND c.judge_id = $2 AND c.team_id = t.id)
		   AND `+judgeModeSQL("$3::text", "$2"),
		append([]any{ev.ID, judgeID, ev.JudgeAssignmentMode}, teamArgs(teams)...)...).Scan(&n)
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
