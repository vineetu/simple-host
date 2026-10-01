package db

import "context"

// RubricCriterion is one row of rubric_criteria, in position order.
type RubricCriterion struct {
	ID          string
	Position    int
	Name        string
	Description string
	Weight      int
	MaxPoints   int
}

// NamedTeam is a team id and name. Assignment skips teams with no members.
type NamedTeam struct {
	ID, Name string
}

// NamedJudge is a judge's user id and the display name they joined with.
type NamedJudge struct {
	ID, Name string
}

// EventConflict is one declared judge/team conflict, with names filled in.
type EventConflict struct {
	JudgeID, JudgeName, TeamID, TeamName, DeclaredBy string
}

// ConflictPair is a judge who must not be assigned to a team.
type ConflictPair struct {
	JudgeID, TeamID string
}

// JudgeAssignment is one judge assigned to one team.
type JudgeAssignment struct {
	JudgeID, JudgeName, TeamID, TeamName string
}

// ListRubric returns the event's criteria in position order.
func ListRubric(ctx context.Context, q Querier, eventID string) ([]RubricCriterion, error) {
	rows, err := queryContext(ctx, q, `
		SELECT id, position, name, description, weight, max_points
		  FROM rubric_criteria WHERE event_id = $1
		 ORDER BY position ASC, id ASC`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RubricCriterion{}
	for rows.Next() {
		var c RubricCriterion
		if err := rows.Scan(&c.ID, &c.Position, &c.Name, &c.Description, &c.Weight, &c.MaxPoints); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReplaceRubric deletes the event's criteria and inserts items in order.
// position is the slice index. Scores that pointed at the old rows go with
// them (event_scores.criterion_id is ON DELETE CASCADE).
func ReplaceRubric(ctx context.Context, q Querier, eventID string, items []RubricCriterion) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM rubric_criteria WHERE event_id = $1`, eventID); err != nil {
		return err
	}
	for i, c := range items {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO rubric_criteria (event_id, position, name, description, weight, max_points)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			eventID, i, c.Name, c.Description, c.Weight, c.MaxPoints); err != nil {
			return err
		}
	}
	return nil
}

// UpdateJudgingSettings writes the fields that are non-nil and returns the event.
func UpdateJudgingSettings(ctx context.Context, q Querier, eventID string, mode *string, perTeam *int) (Event, error) {
	var modeArg, perArg any
	if mode != nil {
		modeArg = *mode
	}
	if perTeam != nil {
		perArg = *perTeam
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET
			judge_assignment_mode = CASE WHEN $2::text IS NULL THEN judge_assignment_mode ELSE $2::text END,
			judges_per_team = CASE WHEN $3::integer IS NULL THEN judges_per_team ELSE $3::integer END,
			updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns, eventID, modeArg, perArg))
}

// ListEventJudges is every judge of the event, by user id.
func ListEventJudges(ctx context.Context, q Querier, eventID string) ([]NamedJudge, error) {
	rows, err := queryContext(ctx, q, `
		SELECT user_id, display_name FROM event_members
		 WHERE event_id = $1 AND role = 'judge'
		 ORDER BY user_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NamedJudge{}
	for rows.Next() {
		var j NamedJudge
		if err := rows.Scan(&j.ID, &j.Name); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListTeamsWithMembers is every team that still has someone on it, by team id.
// The same exclusion as DeleteEmptyOpenTeams (NOT EXISTS event_members): an
// empty or abandoned team does not need a judge.
func ListTeamsWithMembers(ctx context.Context, q Querier, eventID string) ([]NamedTeam, error) {
	rows, err := queryContext(ctx, q, `
		SELECT t.id, t.name FROM event_teams t
		 WHERE t.event_id = $1
		   AND EXISTS (SELECT 1 FROM event_members m WHERE m.team_id = t.id)
		 ORDER BY t.id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NamedTeam{}
	for rows.Next() {
		var t NamedTeam
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// EventTeamInEvent loads a team only when it belongs to this event.
func EventTeamInEvent(ctx context.Context, q Querier, eventID, teamID string) (NamedTeam, error) {
	var t NamedTeam
	err := q.QueryRowContext(ctx, `
		SELECT id, name FROM event_teams WHERE event_id = $1 AND id = $2`, eventID, teamID).Scan(&t.ID, &t.Name)
	return t, err
}

// EventJudge loads a judge member of this event.
func EventJudge(ctx context.Context, q Querier, eventID, userID string) (NamedJudge, error) {
	var j NamedJudge
	err := q.QueryRowContext(ctx, `
		SELECT user_id, display_name FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND role = 'judge'`, eventID, userID).Scan(&j.ID, &j.Name)
	return j, err
}

// ListEventConflicts returns conflicts for the event. judgeID empty means all of them.
func ListEventConflicts(ctx context.Context, q Querier, eventID, judgeID string) ([]EventConflict, error) {
	query := `
		SELECT c.judge_id, COALESCE(j.display_name, ''), c.team_id, t.name, c.declared_by
		  FROM event_conflicts c
		  JOIN event_teams t ON t.id = c.team_id
		  LEFT JOIN event_members j ON j.event_id = c.event_id AND j.user_id = c.judge_id
		 WHERE c.event_id = $1`
	args := []any{eventID}
	if judgeID != "" {
		query += ` AND c.judge_id = $2`
		args = append(args, judgeID)
	}
	query += ` ORDER BY c.created_at ASC, c.judge_id, c.team_id`
	rows, err := queryContext(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []EventConflict{}
	for rows.Next() {
		var c EventConflict
		if err := rows.Scan(&c.JudgeID, &c.JudgeName, &c.TeamID, &c.TeamName, &c.DeclaredBy); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetEventConflict loads one conflict with names. sql.ErrNoRows when there is none.
func GetEventConflict(ctx context.Context, q Querier, eventID, judgeID, teamID string) (EventConflict, error) {
	var c EventConflict
	err := q.QueryRowContext(ctx, `
		SELECT c.judge_id, COALESCE(j.display_name, ''), c.team_id, t.name, c.declared_by
		  FROM event_conflicts c
		  JOIN event_teams t ON t.id = c.team_id
		  LEFT JOIN event_members j ON j.event_id = c.event_id AND j.user_id = c.judge_id
		 WHERE c.event_id = $1 AND c.judge_id = $2 AND c.team_id = $3`,
		eventID, judgeID, teamID).Scan(&c.JudgeID, &c.JudgeName, &c.TeamID, &c.TeamName, &c.DeclaredBy)
	return c, err
}

// DeclareConflict inserts a conflict. A second declaration of the same triple
// does nothing and leaves the original declared_by in place.
func DeclareConflict(ctx context.Context, q Querier, eventID, judgeID, teamID, declaredBy string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_conflicts (event_id, judge_id, team_id, declared_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id, judge_id, team_id) DO NOTHING`,
		eventID, judgeID, teamID, declaredBy)
	return err
}

// DeleteConflict removes one conflict. The bool is false when no row matched.
func DeleteConflict(ctx context.Context, q Querier, eventID, judgeID, teamID string) (bool, error) {
	res, err := q.ExecContext(ctx, `
		DELETE FROM event_conflicts
		 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`, eventID, judgeID, teamID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListConflictPairs is every conflict of the event, without names.
func ListConflictPairs(ctx context.Context, q Querier, eventID string) ([]ConflictPair, error) {
	rows, err := queryContext(ctx, q, `
		SELECT judge_id, team_id FROM event_conflicts WHERE event_id = $1`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ConflictPair{}
	for rows.Next() {
		var p ConflictPair
		if err := rows.Scan(&p.JudgeID, &p.TeamID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ReplaceAssignments replaces every assignment of the event with rows.
func ReplaceAssignments(ctx context.Context, q Querier, eventID string, rows []JudgeAssignment) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM event_assignments WHERE event_id = $1`, eventID); err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO event_assignments (event_id, judge_id, team_id)
			VALUES ($1, $2, $3)`, eventID, row.JudgeID, row.TeamID); err != nil {
			return err
		}
	}
	return nil
}

// ScoreCoverage counts distinct judges per team and distinct teams per judge
// from event_scores. A team or judge with no rows is absent from the map.
func ScoreCoverage(ctx context.Context, q Querier, eventID string) (byTeam, byJudge map[string]int, err error) {
	byTeam, err = countGroups(ctx, q, `
		SELECT team_id, COUNT(DISTINCT judge_id) FROM event_scores
		 WHERE event_id = $1 GROUP BY team_id`, eventID)
	if err != nil {
		return nil, nil, err
	}
	byJudge, err = countGroups(ctx, q, `
		SELECT judge_id, COUNT(DISTINCT team_id) FROM event_scores
		 WHERE event_id = $1 GROUP BY judge_id`, eventID)
	if err != nil {
		return nil, nil, err
	}
	return byTeam, byJudge, nil
}

// AssignmentCounts is how many teams each judge is assigned, in automatic mode.
func AssignmentCounts(ctx context.Context, q Querier, eventID string) (map[string]int, error) {
	return countGroups(ctx, q, `
		SELECT judge_id, COUNT(*) FROM event_assignments
		 WHERE event_id = $1 GROUP BY judge_id`, eventID)
}

func countGroups(ctx context.Context, q Querier, query, eventID string) (map[string]int, error) {
	rows, err := queryContext(ctx, q, query, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}
