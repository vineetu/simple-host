package db

import (
	"context"
	"database/sql"
)

type TrackPanelMember struct {
	TrackID string `json:"track_id"`
	JudgeID string `json:"judge_id"`
}

func ListTrackPanels(ctx context.Context, q Querier, eventID string) ([]TrackPanelMember, error) {
	rows, err := queryContext(ctx, q, `SELECT track_id,judge_id FROM event_track_panels WHERE event_id=$1 ORDER BY track_id,judge_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrackPanelMember{}
	for rows.Next() {
		var p TrackPanelMember
		if err := rows.Scan(&p.TrackID, &p.JudgeID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func ReplaceTrackPanels(ctx context.Context, q Querier, eventID string, panels []TrackPanelMember) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM event_track_panels WHERE event_id=$1`, eventID); err != nil {
		return err
	}
	for _, p := range panels {
		if _, err := q.ExecContext(ctx, `INSERT INTO event_track_panels(event_id,track_id,judge_id) VALUES($1,$2,$3)`, eventID, p.TrackID, p.JudgeID); err != nil {
			return err
		}
	}
	return nil
}

func ListEventAssignments(ctx context.Context, q Querier, eventID string) ([]JudgeAssignment, error) {
	rows, err := queryContext(ctx, q, `SELECT a.judge_id,j.display_name,a.team_id,t.name
		FROM event_assignments a JOIN event_members j ON j.event_id=a.event_id AND j.user_id=a.judge_id AND j.role IN ('judge', 'organiser')
		JOIN event_teams t ON t.event_id=a.event_id AND t.id=a.team_id
		WHERE a.event_id=$1 ORDER BY t.name,j.display_name`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []JudgeAssignment{}
	for rows.Next() {
		var a JudgeAssignment
		if err := rows.Scan(&a.JudgeID, &a.JudgeName, &a.TeamID, &a.TeamName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// EligibleJudgeTeams uses current event mode, event-scoped membership and
// conflict rows. The same predicate gates queues, score writes and totals.
func EligibleJudgeTeams(ctx context.Context, q Querier, eventID, judgeID string) (map[string]bool, error) {
	rows, err := queryContext(ctx, q, `SELECT t.id FROM event_teams t JOIN events e ON e.id=t.event_id
		JOIN event_members m ON m.event_id=e.id AND m.user_id=$2 AND m.role IN ('judge', 'organiser')
		WHERE e.id=$1 AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=e.id AND c.judge_id=$2 AND c.team_id=t.id)
		AND (e.judge_assignment_mode='open'
		OR (e.judge_assignment_mode IN ('automatic','manual') AND EXISTS (SELECT 1 FROM event_assignments a WHERE a.event_id=e.id AND a.judge_id=$2 AND a.team_id=t.id))
		OR (e.judge_assignment_mode='panel' AND EXISTS (SELECT 1 FROM event_track_panels p WHERE p.event_id=e.id AND p.track_id=t.track_id AND p.judge_id=$2)))`, eventID, judgeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func JudgeTeamEligible(ctx context.Context, q Querier, eventID, judgeID, teamID string) (bool, error) {
	eligible, err := EligibleJudgeTeams(ctx, q, eventID, judgeID)
	return eligible[teamID], err
}

func EventTrackInEvent(ctx context.Context, q Querier, eventID, trackID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_tracks WHERE event_id=$1 AND id=$2)`, eventID, trackID).Scan(&ok)
	return ok, err
}

func EventJudgeInEvent(ctx context.Context, q Querier, eventID, judgeID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM event_members WHERE event_id=$1 AND user_id=$2 AND role IN ('judge', 'organiser'))`, eventID, judgeID).Scan(&ok)
	return ok, err
}

func EventCriterionInEvent(ctx context.Context, q Querier, eventID, criterionID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rubric_criteria WHERE event_id=$1 AND id=$2)`, eventID, criterionID).Scan(&ok)
	return ok, err
}

func TrackForTeam(ctx context.Context, q Querier, eventID, teamID string) (*EventTrack, error) {
	var t EventTrack
	err := q.QueryRowContext(ctx, `SELECT x.id,x.slug,x.name,x.challenge,x.prize FROM event_teams m JOIN event_tracks x ON x.id=m.track_id AND x.event_id=m.event_id WHERE m.event_id=$1 AND m.id=$2`, eventID, teamID).Scan(&t.ID, &t.Slug, &t.Name, &t.Challenge, &t.Prize)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}
