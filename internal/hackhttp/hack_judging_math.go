package hackhttp

import (
	"context"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

func computeResultsWithOptions(ctx context.Context, q db.Querier, ev db.Event) ([]teamResult, error) {
	rubric, err := db.ListRubric(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	teams, err := db.ListTeamsWithMembers(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	raw, err := db.ListRawScores(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	eligible := map[string]map[string]bool{}
	for _, r := range raw {
		if _, ok := eligible[r.JudgeID]; !ok {
			eligible[r.JudgeID], err = db.EligibleJudgeTeams(ctx, q, ev.ID, r.JudgeID)
			if err != nil {
				return nil, err
			}
		}
	}
	tracks := make(map[string]*db.EventTrack, len(teams))
	ids := make(map[string]string, len(teams))
	for _, team := range teams {
		track, err := db.TrackForTeam(ctx, q, ev.ID, team.ID)
		if err != nil {
			return nil, err
		}
		tracks[team.ID] = track
		if track != nil {
			ids[team.ID] = track.ID
		}
	}
	tie := ""
	if ev.TieCriterionID.Valid {
		tie = ev.TieCriterionID.String
	}
	return hackentries.ComputeResults(hackentries.ResultsInput[db.EventTrack]{Rubric: rubric, Teams: teams, Raw: raw, Eligible: eligible, Tracks: tracks, TrackIDs: ids, ScoreMode: ev.ScoreMode, TieCriterionID: tie, HasTieCriterion: ev.TieCriterionID.Valid}), nil
}
