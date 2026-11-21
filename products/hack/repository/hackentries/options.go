package hackentries

import (
	"context"
	"sort"

	"github.com/lib/pq"
	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

type TrackPanelMember struct {
	TrackID string `json:"track_id"`
	JudgeID string `json:"judge_id"`
}

func ListTrackPanels(ctx context.Context, q sqlkit.Querier, eventID string) ([]TrackPanelMember, error) {
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

func ReplaceTrackPanels(ctx context.Context, q sqlkit.Querier, eventID string, panels []TrackPanelMember) error {
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

func ListEventAssignments(ctx context.Context, q sqlkit.Querier, eventID string) ([]JudgeAssignment, error) {
	judges, err := judgeIDs(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	teams, err := hackteams.ListEventTeams(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `SELECT a.judge_id,a.team_id,t.name
		FROM event_assignments a
		JOIN `+teamRows(3)+` ON t.event_id=a.event_id AND t.id=a.team_id
		WHERE a.event_id=$1 AND a.judge_id = ANY($2::uuid[]) ORDER BY t.name`, append([]any{eventID, pq.Array(judges)}, teamArgs(teams)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []JudgeAssignment
	var ids []string
	for rows.Next() {
		var a JudgeAssignment
		if err := rows.Scan(&a.JudgeID, &a.TeamID, &a.TeamName); err != nil {
			return nil, err
		}
		found, ids = append(found, a), append(ids, a.JudgeID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	users, err := accounts.UsersByIDs(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	byName, err := accounts.SortByDisplayName(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	rank := make(map[string]int, len(byName))
	for i, id := range byName {
		rank[id] = i
	}
	out := []JudgeAssignment{}
	for _, a := range found {
		if u, ok := users[a.JudgeID]; ok {
			a.JudgeName = u.DisplayName.String
			out = append(out, a)
		}
	}
	// Team name order comes from the query; within one name, display name order.
	for start := 0; start < len(out); {
		end := start + 1
		for end < len(out) && out[end].TeamName == out[start].TeamName {
			end++
		}
		run := out[start:end]
		sort.SliceStable(run, func(i, j int) bool { return rank[run[i].JudgeID] < rank[run[j].JudgeID] })
		start = end
	}
	return out, nil
}

// EligibleJudgeTeams uses current event mode, event-scoped membership and
// conflict rows.
func EligibleJudgeTeams(ctx context.Context, q sqlkit.Querier, ev model.Event, judgeID string) (map[string]bool, error) {
	eventID := ev.ID
	isMember, err := EventJudgeInEvent(ctx, q, eventID, judgeID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	if isMember {
		teams, err := hackteams.JudgedTeams(ctx, q, eventID)
		if err != nil {
			return nil, err
		}
		rows, err := queryContext(ctx, q, `SELECT t.id FROM `+teamRows(4)+`
			WHERE t.event_id=$1 AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=t.event_id AND c.judge_id=$2 AND c.team_id=t.id)
			AND `+EntryJudgeableSQL+`
			AND `+judgeModeSQL("$3::text", "$2"), append([]any{eventID, judgeID, ev.JudgeAssignmentMode}, teamArgs(teams)...)...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			out[id] = true
		}
		return out, rows.Err()
	}
	// A counted team with a judgeable entry still exists: entries go with their team.
	rows, err := queryContext(ctx, q, `SELECT t.id FROM event_former_judges f
		CROSS JOIN unnest(f.counted_team_ids) AS t(id)
		WHERE f.event_id = $1 AND f.user_id = $2
		AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id = f.event_id AND c.judge_id = f.user_id AND c.team_id = t.id)
		AND `+EntryJudgeableSQL, eventID, judgeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// EligibleJudgeTeamsIgnoringConflicts is EligibleJudgeTeams for a current judge
// or judging organiser (mode, assignments and panels), without the live
// event_conflicts exclusion.
func EligibleJudgeTeamsIgnoringConflicts(ctx context.Context, q sqlkit.Querier, ev model.Event, judgeID string) (map[string]bool, error) {
	isMember, err := EventJudgeInEvent(ctx, q, ev.ID, judgeID)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	if !isMember {
		return out, nil
	}
	teams, err := hackteams.ListEventTeams(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `SELECT t.id FROM `+teamRows(4)+`
		WHERE t.event_id=$1
		AND `+judgeModeSQL("$3::text", "$2"), append([]any{ev.ID, judgeID, ev.JudgeAssignmentMode}, teamArgs(teams)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

func JudgeTeamEligible(ctx context.Context, q sqlkit.Querier, ev model.Event, judgeID, teamID string) (bool, error) {
	eligible, err := EligibleJudgeTeams(ctx, q, ev, judgeID)
	return eligible[teamID], err
}

// EventJudgeInEvent is whether judgeID currently judges the event.
func EventJudgeInEvent(ctx context.Context, q sqlkit.Querier, eventID, judgeID string) (bool, error) {
	return isJudge(ctx, q, eventID, judgeID)
}

func EventCriterionInEvent(ctx context.Context, q sqlkit.Querier, eventID, criterionID string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rubric_criteria WHERE event_id=$1 AND id=$2)`, eventID, criterionID).Scan(&ok)
	return ok, err
}

type EventTrack = model.EventTrack

// EventJudgeEligibility loads current and former judging eligibility in one query.
func EventJudgeEligibility(ctx context.Context, q sqlkit.Querier, eventID string) (map[string]map[string]bool, error) {
	judges, err := judgeIDs(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	teams, err := hackteams.JudgedTeams(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	ev, err := hackevents.GetEventByID(ctx, q, eventID)
	if err != nil {
		return nil, err
	}
	rows, err := queryContext(ctx, q, `SELECT m.user_id,t.id FROM `+teamRows(4)+`
			JOIN unnest($2::uuid[]) AS m(user_id) ON true
			WHERE t.event_id=$1 AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=t.event_id AND c.judge_id=m.user_id AND c.team_id=t.id)
			AND `+EntryJudgeableSQL+`
			AND `+judgeModeSQL("$3::text", "m.user_id")+` UNION SELECT f.user_id,t.id FROM event_former_judges f
 CROSS JOIN unnest(f.counted_team_ids) AS t(id)
 WHERE f.event_id=$1
 AND NOT (f.user_id = ANY($2::uuid[]))
 AND NOT EXISTS (SELECT 1 FROM event_conflicts c WHERE c.event_id=f.event_id AND c.judge_id=f.user_id AND c.team_id=t.id)
 AND `+EntryJudgeableSQL+``, append([]any{eventID, pq.Array(judges), ev.JudgeAssignmentMode}, teamArgs(teams)...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]bool{}
	for rows.Next() {
		var judge, team string
		if err := rows.Scan(&judge, &team); err != nil {
			return nil, err
		}
		if out[judge] == nil {
			out[judge] = map[string]bool{}
		}
		out[judge][team] = true
	}
	return out, rows.Err()
}
