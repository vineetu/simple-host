package entries

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// TeamResult is a calculated team result with its track.
type TeamResult = hackentries.TeamResult[model.EventTrack]

// ComputeResults loads the inputs and calculates the current results.
func (s *Service) ComputeResults(ctx context.Context, q sqlkit.Querier, ev model.Event) ([]TeamResult, error) {
	rubric, err := entryrepo.ListRubric(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	raw, err := entryrepo.ListRawScores(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	eligible := map[string]map[string]bool{}
	for _, r := range raw {
		if _, ok := eligible[r.JudgeID]; !ok {
			eligible[r.JudgeID], err = entryrepo.EligibleJudgeTeams(ctx, q, ev.ID, r.JudgeID)
			if err != nil {
				return nil, err
			}
		}
	}
	tracks := make(map[string]*model.EventTrack, len(teams))
	ids := make(map[string]string, len(teams))
	for _, team := range teams {
		track, err := entryrepo.TrackForTeam(ctx, q, ev.ID, team.ID)
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
	return hackentries.ComputeResults(hackentries.ResultsInput[model.EventTrack]{Rubric: rubric, Teams: teams, Raw: raw, Eligible: eligible, Tracks: tracks, TrackIDs: ids, ScoreMode: ev.ScoreMode, TieCriterionID: tie, HasTieCriterion: ev.TieCriterionID.Valid}), nil
}

// PublishResults computes and stores a snapshot in the existing transaction.
// snapshot builds the transport representation while the transaction is open.
func (s *Service) PublishResults(ctx context.Context, eventID, userID string, overrides []hackentries.RankOverride, snapshot func(sqlkit.Querier, []TeamResult) ([]byte, error), committed func()) (entryrepo.EventResultsRow, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return entryrepo.EventResultsRow{}, "begin", err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return entryrepo.EventResultsRow{}, "lock", err
	}
	results, err := s.ComputeResults(ctx, tx, ev)
	if err != nil {
		return entryrepo.EventResultsRow{}, "compute", err
	}
	if len(overrides) > 0 {
		ids := map[string]string{}
		for _, row := range results {
			if row.Track != nil {
				ids[row.TeamID] = row.Track.ID
			}
		}
		if err := hackentries.ApplyRankOverrides(results, overrides, ids); err != nil {
			return entryrepo.EventResultsRow{}, "override", &Error{Code: "invalid_rank_override", Message: err.Error()}
		}
	}
	body, err := snapshot(tx, results)
	if err != nil {
		return entryrepo.EventResultsRow{}, "snapshot", err
	}
	if err := entryrepo.UpsertEventResults(ctx, tx, eventID, userID, body); err != nil {
		return entryrepo.EventResultsRow{}, "write", err
	}
	if err := tx.Commit(); err != nil {
		return entryrepo.EventResultsRow{}, "commit", err
	}
	committed()
	full, err := entryrepo.GetEventResults(ctx, s.db, eventID)
	return full, "read", err
}

// GetResults reads the published snapshot.
func (s *Service) GetResults(ctx context.Context, eventID string) (entryrepo.EventResultsRow, error) {
	return entryrepo.GetEventResults(ctx, s.db, eventID)
}

// SetFullRanking changes the visibility setting and reloads the snapshot.
func (s *Service) SetFullRanking(ctx context.Context, eventID string, full bool) (entryrepo.EventResultsRow, bool, error) {
	ok, err := entryrepo.SetResultsFullRanking(ctx, s.db, eventID, full)
	if err != nil || !ok {
		return entryrepo.EventResultsRow{}, ok, err
	}
	row, err := entryrepo.GetEventResults(ctx, s.db, eventID)
	return row, true, err
}

// ScoreCoverage reads team and judge score counts.
func (s *Service) ScoreCoverage(ctx context.Context, eventID string) (map[string]int, map[string]int, error) {
	return entryrepo.ScoreCoverage(ctx, s.db, eventID)
}
