package hackentries

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/host/sqlkit"

	"github.com/vsriram/simple-host/products/hack/model"
)

type RubricCriterion = model.RubricCriterion

type NamedTeam = model.NamedTeam

type NamedJudge = model.NamedJudge

type EventConflict struct {
	JudgeID, JudgeName, TeamID, TeamName, DeclaredBy string
	HadScore                                         bool
}

type ConflictPair struct {
	JudgeID, TeamID string
}

type JudgeAssignment = model.JudgeAssignment

func ListRubric(ctx context.Context, q sqlkit.Querier, eventID string) ([]RubricCriterion, error) {
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

// ReplaceRubric keeps criterion identities and removes only omitted criteria.
func ReplaceRubric(ctx context.Context, q sqlkit.Querier, eventID string, items []RubricCriterion) error {
	existing, err := ListRubric(ctx, q, eventID)
	if err != nil {
		return err
	}
	kept := map[string]bool{}
	for i, c := range items {
		if c.ID == "" {
			_, err = q.ExecContext(ctx, `INSERT INTO rubric_criteria (event_id, position, name, description, weight, max_points) VALUES ($1,$2,$3,$4,$5,$6)`, eventID, i, c.Name, c.Description, c.Weight, c.MaxPoints)
		} else {
			kept[c.ID] = true
			_, err = q.ExecContext(ctx, `DELETE FROM event_scores WHERE event_id=$1 AND criterion_id=$2 AND points > $3`, eventID, c.ID, c.MaxPoints)
			if err != nil {
				return err
			}
			_, err = q.ExecContext(ctx, `UPDATE rubric_criteria SET position=$3,name=$4,description=$5,weight=$6,max_points=$7,updated_at=now() WHERE event_id=$1 AND id=$2`, eventID, c.ID, i, c.Name, c.Description, c.Weight, c.MaxPoints)
		}
		if err != nil {
			return err
		}
	}
	for _, c := range existing {
		if !kept[c.ID] {
			if _, err := q.ExecContext(ctx, `DELETE FROM rubric_criteria WHERE event_id=$1 AND id=$2`, eventID, c.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func ListEventJudges(ctx context.Context, q sqlkit.Querier, eventID string) ([]NamedJudge, error) {
	rows, err := queryContext(ctx, q, `
		SELECT user_id, COALESCE(u.display_name,''), role FROM event_members m JOIN users u ON u.id=m.user_id JOIN events e ON e.id=m.event_id
		 WHERE m.event_id = $1 AND `+judgeMemberSQL("m", "e")+`
		 ORDER BY user_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NamedJudge{}
	for rows.Next() {
		var j NamedJudge
		if err := rows.Scan(&j.ID, &j.Name, &j.Role); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListTeamsWithMembers is every team that still needs judging, by team id: one
// with someone on it (the exclusion DeleteEmptyOpenTeams uses for an abandoned
// team), or one marked kept_after_leave. Leaving an event never deletes a team
// it empties, so its entry keeps being judged and ranked as if the leaver had
// not left.
func ListTeamsWithMembers(ctx context.Context, q sqlkit.Querier, eventID string) ([]NamedTeam, error) {
	rows, err := queryContext(ctx, q, `
		SELECT t.id, t.name, t.slug FROM event_teams t
		 WHERE t.event_id = $1
		   AND (EXISTS (SELECT 1 FROM event_members m WHERE m.team_id = t.id) OR t.kept_after_leave)
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

func EventTeamInEvent(ctx context.Context, q sqlkit.Querier, eventID, teamID string) (NamedTeam, error) {
	var t NamedTeam
	err := q.QueryRowContext(ctx, `
		SELECT id, name FROM event_teams WHERE event_id = $1 AND id = $2`, eventID, teamID).Scan(&t.ID, &t.Name)
	return t, err
}

func EventJudge(ctx context.Context, q sqlkit.Querier, eventID, userID string) (NamedJudge, error) {
	var j NamedJudge
	err := q.QueryRowContext(ctx, `
		SELECT user_id, COALESCE(u.display_name,'') FROM event_members m JOIN users u ON u.id=m.user_id JOIN events e ON e.id=m.event_id
		 WHERE m.event_id = $1 AND m.user_id = $2 AND `+judgeMemberSQL("m", "e")+``, eventID, userID).Scan(&j.ID, &j.Name)
	return j, err
}

// ListEventConflicts returns conflicts for the event. judgeID empty means all of them.
func ListEventConflicts(ctx context.Context, q sqlkit.Querier, eventID, judgeID string) ([]EventConflict, error) {
	query := `
		SELECT c.judge_id, COALESCE(u.display_name, ''), c.team_id, t.name, c.declared_by,
		       EXISTS(SELECT 1 FROM event_scores es WHERE es.event_id = c.event_id
		                AND es.judge_id = c.judge_id AND es.team_id = c.team_id)
		  FROM event_conflicts c
		  JOIN event_teams t ON t.id = c.team_id
		  LEFT JOIN users u ON u.id = c.judge_id
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
		if err := rows.Scan(&c.JudgeID, &c.JudgeName, &c.TeamID, &c.TeamName, &c.DeclaredBy, &c.HadScore); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func GetEventConflict(ctx context.Context, q sqlkit.Querier, eventID, judgeID, teamID string) (EventConflict, error) {
	var c EventConflict
	err := q.QueryRowContext(ctx, `
		SELECT c.judge_id, COALESCE(u.display_name, ''), c.team_id, t.name, c.declared_by,
		       EXISTS(SELECT 1 FROM event_scores es WHERE es.event_id = c.event_id
		                AND es.judge_id = c.judge_id AND es.team_id = c.team_id)
		  FROM event_conflicts c
		  JOIN event_teams t ON t.id = c.team_id
		  LEFT JOIN users u ON u.id = c.judge_id
		 WHERE c.event_id = $1 AND c.judge_id = $2 AND c.team_id = $3`,
		eventID, judgeID, teamID).Scan(&c.JudgeID, &c.JudgeName, &c.TeamID, &c.TeamName, &c.DeclaredBy, &c.HadScore)
	return c, err
}

func DeclareConflict(ctx context.Context, q sqlkit.Querier, eventID, judgeID, teamID, declaredBy string) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_conflicts (event_id, judge_id, team_id, declared_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (event_id, judge_id, team_id) DO NOTHING`,
		eventID, judgeID, teamID, declaredBy)
	return err
}

func DeleteConflict(ctx context.Context, q sqlkit.Querier, eventID, judgeID, teamID string) (bool, error) {
	res, err := q.ExecContext(ctx, `
		DELETE FROM event_conflicts
		 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`, eventID, judgeID, teamID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func ListConflictPairs(ctx context.Context, q sqlkit.Querier, eventID string) ([]ConflictPair, error) {
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

func ReplaceAssignments(ctx context.Context, q sqlkit.Querier, eventID string, rows []JudgeAssignment) error {
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
func ScoreCoverage(ctx context.Context, q sqlkit.Querier, eventID string) (byTeam, byJudge map[string]int, err error) {
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

// scoreEligiblePredicate counts a score either the ordinary way (the judge is
// still a member, in good standing with today's mode, conflicts and
// assignments) or, for someone who left, when a former-judge record froze this
// exact team as one that counted when they left.
var scoreEligiblePredicate = `(((
  EXISTS (SELECT 1 FROM event_members m WHERE m.event_id=s.event_id AND m.user_id=s.judge_id AND ` + judgeMemberSQL("m", "e") + `)
  AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=s.event_id AND c.judge_id=s.judge_id AND c.team_id=s.team_id)
  AND (e.judge_assignment_mode='open'
  OR (e.judge_assignment_mode IN ('automatic','manual') AND EXISTS (SELECT 1 FROM event_assignments a WHERE a.event_id=s.event_id AND a.judge_id=s.judge_id AND a.team_id=s.team_id))
  OR (e.judge_assignment_mode='panel' AND EXISTS (SELECT 1 FROM event_teams t JOIN event_track_panels p ON p.event_id=t.event_id AND p.track_id=t.track_id AND p.judge_id=s.judge_id WHERE t.event_id=s.event_id AND t.id=s.team_id)))
) OR (
  NOT EXISTS (SELECT 1 FROM event_members m WHERE m.event_id=s.event_id AND m.user_id=s.judge_id)
  AND EXISTS (SELECT 1 FROM event_former_judges f WHERE f.event_id=s.event_id AND f.user_id=s.judge_id AND s.team_id = ANY(f.counted_team_ids))
  AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=s.event_id AND c.judge_id=s.judge_id AND c.team_id=s.team_id)
)) AND EXISTS (
  SELECT 1 FROM event_entries en WHERE en.team_id=s.team_id
    AND (en.submitted_at IS NOT NULL OR en.submit_settled_at IS NOT NULL)
    AND en.excluded_at IS NULL
))`

// AssignmentCounts is how many teams each judge is assigned, in automatic mode.
func AssignmentCounts(ctx context.Context, q sqlkit.Querier, eventID string) (map[string]int, error) {
	return countGroups(ctx, q, `
		SELECT judge_id, COUNT(*) FROM event_assignments
		 WHERE event_id = $1 GROUP BY judge_id`, eventID)
}

func countGroups(ctx context.Context, q sqlkit.Querier, query, eventID string) (map[string]int, error) {
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

type JudgeScore struct {
	CriterionID string
	Points      int
}

// GetJudgeTeamScores returns a judge's current scores for a team and the
// comment on them (empty if none exist yet), read through scoreRows like
// every other reader of per-judge scores.
func GetJudgeTeamScores(ctx context.Context, q sqlkit.Querier, eventID, judgeID, teamID string) ([]JudgeScore, string, error) {
	rows, err := scoreRows(ctx, q, "s.event_id = $1 AND s.judge_id = $2 AND s.team_id = $3 ORDER BY s.updated_at DESC, r.position", eventID, judgeID, teamID)
	if err != nil {
		return nil, "", err
	}
	out := make([]JudgeScore, 0, len(rows))
	comment := ""
	for _, row := range rows {
		out = append(out, JudgeScore{CriterionID: row.CriterionID, Points: row.Points})
		comment = row.Comment
	}
	return out, comment, nil
}

// UpsertJudgeScores writes each given score (ON CONFLICT DO UPDATE, so a retried
// write never double-counts) and, when comment is non-nil, sets it on every row
// for this (judge, team) pair. There is one comment per judge and team, stored
// on each of its criterion rows: a new criterion row copies it.
func UpsertJudgeScores(ctx context.Context, q sqlkit.Querier, eventID, judgeID, teamID string, scores []JudgeScore, comment *string) ([]JudgeScore, string, error) {
	for _, s := range scores {
		if _, err := q.ExecContext(ctx, `
			INSERT INTO event_scores (event_id, judge_id, team_id, criterion_id, points, comment)
			VALUES ($1, $2, $3, $4, $5, COALESCE((
				SELECT c.comment FROM event_scores c
				 WHERE c.event_id = $1 AND c.judge_id = $2 AND c.team_id = $3
				 ORDER BY c.comment <> '' DESC, c.updated_at DESC LIMIT 1), ''))
			ON CONFLICT (judge_id, team_id, criterion_id) DO UPDATE
			   SET points = EXCLUDED.points,
			       updated_at = now()`,
			eventID, judgeID, teamID, s.CriterionID, s.Points); err != nil {
			return nil, "", err
		}
	}
	if comment != nil {
		// A comment-only call (scores empty) needs an existing row to hold it; the
		// handler refuses that case, so this is a no-op rather than an error.
		if _, err := q.ExecContext(ctx, `
			UPDATE event_scores SET comment = $4, updated_at = now()
			 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`,
			eventID, judgeID, teamID, *comment); err != nil {
			return nil, "", err
		}
	}
	return GetJudgeTeamScores(ctx, q, eventID, judgeID, teamID)
}

func CriterionIDsForEvent(ctx context.Context, q sqlkit.Querier, eventID string) (map[string]int, error) {
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
type RawScoreRow = model.RawScoreRow

// ListRawScores is every score row of the event with names resolved, for the
// scores CSV and for computing results in Go.
func ListRawScores(ctx context.Context, q sqlkit.Querier, eventID string) ([]RawScoreRow, error) {
	return scoreRows(ctx, q, "s.event_id = $1", eventID)
}

// scoreRows is the one read of per-judge scores: a judge's own view and the
// organiser's scores export both come through it, so a field added to
// RawScoreRow here reaches both. Each row carries its (judge, team) pair's
// one comment: the newest non-empty one, so a row saved without a comment
// never blanks it.
func scoreRows(ctx context.Context, q sqlkit.Querier, where string, args ...any) ([]RawScoreRow, error) {
	rows, err := queryContext(ctx, q, `
		SELECT s.judge_id, COALESCE(u.display_name, ''), s.team_id, t.name,
		       s.criterion_id, r.name, r.position, s.points, r.max_points,
		       FIRST_VALUE(s.comment) OVER (PARTITION BY s.judge_id, s.team_id
		                                    ORDER BY s.comment <> '' DESC, s.updated_at DESC)
		  FROM event_scores s
		  JOIN event_teams t ON t.id = s.team_id
		  JOIN rubric_criteria r ON r.id = s.criterion_id
		  LEFT JOIN users u ON u.id = s.judge_id
		 WHERE `+where, args...)
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

type EventResultsRow struct {
	Published   bool
	PublishedAt sql.NullTime
	FullRanking bool
	Snapshot    []byte // raw JSON, the caller decodes into whatever shape it wants
}

func GetEventResults(ctx context.Context, q sqlkit.Querier, eventID string) (EventResultsRow, error) {
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
func UpsertEventResults(ctx context.Context, q sqlkit.Querier, eventID, publishedBy string, snapshot []byte) error {
	_, err := q.ExecContext(ctx, `
		INSERT INTO event_results (event_id, published_at, published_by, full_ranking, snapshot)
		VALUES ($1, now(), $2, FALSE, $3)
		ON CONFLICT (event_id) DO UPDATE
		   SET published_at = now(), published_by = EXCLUDED.published_by, snapshot = EXCLUDED.snapshot`,
		eventID, publishedBy, snapshot)
	return err
}

func SetResultsFullRanking(ctx context.Context, q sqlkit.Querier, eventID string, full bool) (bool, error) {
	res, err := q.ExecContext(ctx, `
		UPDATE event_results SET full_ranking = $2 WHERE event_id = $1`, eventID, full)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// TeamComments is every distinct, judge-deduplicated comment on a team's scores
// (one per judge who left a non-empty comment, order not correlated with any
// particular judge).
func TeamComments(ctx context.Context, q sqlkit.Querier, eventID, teamID string) ([]string, error) {
	// A conflicted judge's score is excluded from the team's total (see
	// computeResults); their comment is excluded too, since the team should not see
	// feedback from someone who declared a conflict with it.
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
