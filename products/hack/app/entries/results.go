package entries

import (
	"context"
	"database/sql"

	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
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
	eligible, err := entryrepo.EventJudgeEligibility(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	tracks, err := entryrepo.EventTeamTracks(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	return computeLoadedResults(ev, rubric, teams, raw, eligible, tracks), nil
}

func computeLoadedResults(ev model.Event, rubric []model.RubricCriterion, teams []entryrepo.NamedTeam, raw []model.RawScoreRow, eligible map[string]map[string]bool, tracks map[string]*model.EventTrack) []TeamResult {
	ids := map[string]string{}
	for id, track := range tracks {
		ids[id] = track.ID
	}
	tie := ""
	if ev.TieCriterionID.Valid {
		tie = ev.TieCriterionID.String
	}
	return hackentries.ComputeResults(hackentries.ResultsInput[model.EventTrack]{Rubric: rubric, Teams: teams, Raw: raw, Eligible: eligible, Tracks: tracks, TrackIDs: ids, ScoreMode: ev.ScoreMode, TieCriterionID: tie, HasTieCriterion: ev.TieCriterionID.Valid})
}

// PublishResults computes and stores a snapshot in the existing transaction.
// snapshot builds the transport representation while the transaction is open.
// prepare, when set, runs right after the event row is locked and may
// refuse the publish or change the event (SIM-271: move it to Results); its
// error is returned with phase "prepare".
func (s *Service) PublishResults(ctx context.Context, eventID, userID string, overrides []hackentries.RankOverride, prepare func(*sql.Tx, model.Event) (model.Event, error), snapshot func(sqlkit.Querier, []TeamResult) ([]byte, error), committed func()) (entryrepo.EventResultsRow, string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return entryrepo.EventResultsRow{}, "begin", err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return entryrepo.EventResultsRow{}, "lock", err
	}
	if prepare != nil {
		if ev, err = prepare(tx, ev); err != nil {
			return entryrepo.EventResultsRow{}, "prepare", err
		}
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
