package db

import (
	"context"
	"database/sql"
)

// RubricCriterion is one row of rubric_criteria, in position order.
type RubricCriterion struct {
	ID          string
	Position    int
	Name        string
	Description string
	Weight      int
	MaxPoints   int
}

// NamedTeam is a team id, display name and URL slug. Assignment skips teams with no members.
type NamedTeam struct {
	ID, Name, Slug string
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
	JudgeID   string `json:"judge_id"`
	JudgeName string `json:"judge_name"`
	TeamID    string `json:"team_id"`
	TeamName  string `json:"team_name"`
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

// ListEventJudges is every member eligible to judge the event, by user id.
func ListEventJudges(ctx context.Context, q Querier, eventID string) ([]NamedJudge, error) {
	rows, err := queryContext(ctx, q, `
		SELECT user_id, display_name FROM event_members
		 WHERE event_id = $1 AND role IN ('judge', 'organiser')
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
		SELECT t.id, t.name, t.slug FROM event_teams t
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
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug); err != nil {
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

// EventJudge loads a member eligible to judge this event.
func EventJudge(ctx context.Context, q Querier, eventID, userID string) (NamedJudge, error) {
	var j NamedJudge
	err := q.QueryRowContext(ctx, `
		SELECT user_id, display_name FROM event_members
		 WHERE event_id = $1 AND user_id = $2 AND role IN ('judge', 'organiser')`, eventID, userID).Scan(&j.ID, &j.Name)
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
		SELECT s.team_id, COUNT(DISTINCT s.judge_id) FROM event_scores s JOIN events e ON e.id=s.event_id
		 WHERE s.event_id = $1 AND `+scoreEligiblePredicate+` GROUP BY s.team_id`, eventID)
	if err != nil {
		return nil, nil, err
	}
	byJudge, err = countGroups(ctx, q, `
		SELECT s.judge_id, COUNT(DISTINCT s.team_id) FROM event_scores s JOIN events e ON e.id=s.event_id
		 WHERE s.event_id = $1 AND `+scoreEligiblePredicate+` GROUP BY s.judge_id`, eventID)
	if err != nil {
		return nil, nil, err
	}
	return byTeam, byJudge, nil
}

const scoreEligiblePredicate = `EXISTS (SELECT 1 FROM event_members m WHERE m.event_id=s.event_id AND m.user_id=s.judge_id AND m.role IN ('judge', 'organiser'))
 AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=s.event_id AND c.judge_id=s.judge_id AND c.team_id=s.team_id)
 AND (e.judge_assignment_mode='open'
 OR (e.judge_assignment_mode IN ('automatic','manual') AND EXISTS (SELECT 1 FROM event_assignments a WHERE a.event_id=s.event_id AND a.judge_id=s.judge_id AND a.team_id=s.team_id))
 OR (e.judge_assignment_mode='panel' AND EXISTS (SELECT 1 FROM event_teams t JOIN event_track_panels p ON p.event_id=t.event_id AND p.track_id=t.track_id AND p.judge_id=s.judge_id WHERE t.event_id=s.event_id AND t.id=s.team_id)))`

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

// --- M3 pass 2: scoring, lock/unlock, publish, results -------------------

// SetJudgingLock sets or clears judging_locked_at/judging_lock_reason.
// locked=false clears both regardless of reason; locked=true sets
// judging_locked_at to now() only if it was not already set (idempotent),
// leaving the stored reason from the last unlock in place until the next one.
func SetJudgingLock(ctx context.Context, q Querier, eventID string, locked bool, reason string) (Event, error) {
	if locked {
		return scanEvent(q.QueryRowContext(ctx, `
			UPDATE events SET
				judging_locked_at = COALESCE(judging_locked_at, clock_timestamp()),
				updated_at = now()
			WHERE id = $1
			RETURNING `+eventColumns, eventID))
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET
			judging_locked_at = NULL,
			judging_lock_reason = $2,
			updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns, eventID, reason))
}

// JudgeScore is one judge's points for one criterion.
type JudgeScore struct {
	CriterionID string
	Points      int
}

// GetJudgeTeamScores returns a judge's current scores for a team (whatever
// rows exist; an absent criterion is simply not in the slice) and the
// comment stored on those rows (empty string if none exist yet).
func GetJudgeTeamScores(ctx context.Context, q Querier, eventID, judgeID, teamID string) ([]JudgeScore, string, error) {
	// UpsertJudgeScores keeps every row for a (judge, team) pair holding the
	// same comment, so any row's value would do; ORDER BY updated_at DESC
	// is a defensive tie-break (the most recently touched row wins) rather
	// than a load-bearing requirement.
	rows, err := queryContext(ctx, q, `
		SELECT criterion_id, points, comment FROM event_scores
		 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3
		 ORDER BY updated_at DESC`, eventID, judgeID, teamID)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	out := []JudgeScore{}
	comment := ""
	haveComment := false
	for rows.Next() {
		var s JudgeScore
		var c string
		if err := rows.Scan(&s.CriterionID, &s.Points, &c); err != nil {
			return nil, "", err
		}
		out = append(out, s)
		if !haveComment {
			comment = c
			haveComment = true
		}
	}
	return out, comment, rows.Err()
}

// UpsertJudgeScores writes each given score (ON CONFLICT DO UPDATE, so a
// retried write never double-counts) and, when comment is non-nil, sets it
// on every row for this (judge, team) pair — not just the ones named in
// this call — so every row always agrees on the one comment. Returns the
// full updated set.
func UpsertJudgeScores(ctx context.Context, q Querier, eventID, judgeID, teamID string, scores []JudgeScore, comment *string) ([]JudgeScore, string, error) {
	// The comment is stored once per criterion row (there is no separate
	// comment table), so every row for this (judge, team) pair must always
	// hold the SAME value or "the" comment is ambiguous. Upsert points
	// first, carrying over whatever comment a row already has (so a
	// points-only call never blanks an existing comment); if a comment is
	// given at all, a single blanket UPDATE afterward puts it on every row
	// that now exists, including ones just inserted — never just the rows
	// named in this call.
	for _, s := range scores {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO event_scores (event_id, judge_id, team_id, criterion_id, points, comment)
			VALUES ($1, $2, $3, $4, $5, '')
			ON CONFLICT (judge_id, team_id, criterion_id) DO UPDATE
			   SET points = EXCLUDED.points,
			       updated_at = now()`,
			eventID, judgeID, teamID, s.CriterionID, s.Points); err != nil {
			return nil, "", err
		}
	}
	if comment != nil {
		// A comment-only call (scores empty) needs at least one existing row
		// to hold it; the handler already refuses that case when none
		// exists, so this is a no-op rather than an error if ever called
		// directly with nothing to update.
		if _, err := q.ExecContext(ctx, `
			UPDATE event_scores SET comment = $4, updated_at = now()
			 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`,
			eventID, judgeID, teamID, *comment); err != nil {
			return nil, "", err
		}
	}
	return GetJudgeTeamScores(ctx, q, eventID, judgeID, teamID)
}

// CriterionIDsForEvent is the set of criterion ids currently in the event's
// rubric, for validating a score write's criterion_ids belong to it.
func CriterionIDsForEvent(ctx context.Context, q Querier, eventID string) (map[string]int, error) {
	rows, err := queryContext(ctx, q, `
		SELECT id, max_points FROM rubric_criteria WHERE event_id = $1`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var id string
		var max int
		if err := rows.Scan(&id, &max); err != nil {
			return nil, err
		}
		out[id] = max
	}
	return out, rows.Err()
}

// RawScoreRow is one row of event_scores joined with names, for the judge
// queue's "scores_count" and for the CSV export.
type RawScoreRow struct {
	JudgeID, JudgeName         string
	TeamID, TeamName           string
	CriterionID, CriterionName string
	CriterionPosition          int
	Points, MaxPoints          int
	Comment                    string
}

// ListRawScores is every score row of the event with names resolved, for the
// scores CSV and for computing results in Go.
func ListRawScores(ctx context.Context, q Querier, eventID string) ([]RawScoreRow, error) {
	rows, err := queryContext(ctx, q, `
		SELECT s.judge_id, COALESCE(j.display_name, ''), s.team_id, t.name,
		       s.criterion_id, r.name, r.position, s.points, r.max_points, s.comment
		  FROM event_scores s
		  JOIN event_teams t ON t.id = s.team_id
		  JOIN rubric_criteria r ON r.id = s.criterion_id
		  LEFT JOIN event_members j ON j.event_id = s.event_id AND j.user_id = s.judge_id
		 WHERE s.event_id = $1`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RawScoreRow{}
	for rows.Next() {
		var row RawScoreRow
		if err := rows.Scan(&row.JudgeID, &row.JudgeName, &row.TeamID, &row.TeamName,
			&row.CriterionID, &row.CriterionName, &row.CriterionPosition, &row.Points, &row.MaxPoints, &row.Comment); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// EventResultsRow is the stored event_results row.
type EventResultsRow struct {
	Published   bool
	PublishedAt sql.NullTime
	FullRanking bool
	Snapshot    []byte // raw JSON, the caller decodes into whatever shape it wants
}

// GetEventResults loads the stored snapshot. Published is false (zero value)
// when there is no row yet; that is not an error.
func GetEventResults(ctx context.Context, q Querier, eventID string) (EventResultsRow, error) {
	var row EventResultsRow
	err := q.QueryRowContext(ctx, `
		SELECT published_at, full_ranking, snapshot FROM event_results WHERE event_id = $1`,
		eventID).Scan(&row.PublishedAt, &row.FullRanking, &row.Snapshot)
	if err == sql.ErrNoRows {
		return EventResultsRow{}, nil
	}
	if err != nil {
		return EventResultsRow{}, err
	}
	row.Published = true
	return row, nil
}

// UpsertEventResults writes the snapshot, keeping the existing full_ranking
// value on a republish (a fresh row defaults to false).
func UpsertEventResults(ctx context.Context, q Querier, eventID, publishedBy string, snapshot []byte) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_results (event_id, published_at, published_by, full_ranking, snapshot)
		VALUES ($1, now(), $2, FALSE, $3)
		ON CONFLICT (event_id) DO UPDATE
		   SET published_at = now(), published_by = EXCLUDED.published_by, snapshot = EXCLUDED.snapshot`,
		eventID, publishedBy, snapshot)
	return err
}

// SetResultsFullRanking flips the display switch on an existing row. The
// bool is false when there is no row to flip.
func SetResultsFullRanking(ctx context.Context, q Querier, eventID string, full bool) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE event_results SET full_ranking = $2 WHERE event_id = $1`, eventID, full)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// TeamComments is every distinct, judge-deduplicated comment on a team's
// scores (one per judge who left a non-empty comment, order not correlated
// with any particular judge). Every row for a given judge now always holds
// the same comment (see UpsertJudgeScores), so which one DISTINCT ON picks
// doesn't matter; ORDER BY updated_at DESC is a defensive tie-break only.
func TeamComments(ctx context.Context, q Querier, eventID, teamID string) ([]string, error) {
	// A conflicted judge's score is excluded from the team's total (see
	// computeResults); their comment is excluded here for the same reason —
	// someone who declared a conflict with this team isn't a fair reviewer
	// of it, and the team should not be shown their feedback as if it were.
	rows, err := queryContext(ctx, q, `
		SELECT DISTINCT ON (s.judge_id) s.comment FROM event_scores s JOIN events e ON e.id=s.event_id
		 WHERE s.event_id = $1 AND s.team_id = $2 AND s.comment <> '' AND `+scoreEligiblePredicate+`
		 ORDER BY s.judge_id, s.updated_at DESC`, eventID, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
