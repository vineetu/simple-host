package entries

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

type TeamResult = hackentries.TeamResult[model.EventTrack]

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
	tracks, err := teamrepo.EventTeamTracks(ctx, q, ev.ID)
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

// PublishResults stores the results snapshot in one transaction, first moving
// Closed or Judging to Results with moveToResults; committed runs once the
// snapshot commits, even if the read after it fails.
func (s *Service) PublishResults(ctx context.Context, eventID, userID string, overrides []RankOverride,
	moveToResults func(*sql.Tx, model.Event) (model.Event, error),
	siteURL func(teamID, teamSlug string) (string, error), committed func()) ([]ResultRow, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return nil, false, err
	}
	move, err := publishMovesStage(ev.Stage)
	if err != nil {
		return nil, false, err
	}
	if move {
		if ev, err = moveToResults(tx, ev); err != nil {
			return nil, false, err
		}
	}
	results, err := s.ComputeResults(ctx, tx, ev)
	if err != nil {
		return nil, false, err
	}
	if len(overrides) > 0 {
		ids := map[string]string{}
		for _, row := range results {
			if row.Track != nil {
				ids[row.TeamID] = row.Track.ID
			}
		}
		if err := hackentries.ApplyRankOverrides(results, overrides, ids); err != nil {
			return nil, false, &Error{Code: "invalid_rank_override", Message: err.Error()}
		}
	}
	cards, err := s.entryCards(ctx, tx, ev, results, siteURL)
	if err != nil {
		return nil, false, fmt.Errorf("results cards: %w", err)
	}
	rows := SnapshotRows(results, cards)
	body, err := json.Marshal(rows)
	if err != nil {
		return nil, false, err
	}
	if err := entryrepo.UpsertEventResults(ctx, tx, eventID, userID, body); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	committed()
	full, err := entryrepo.GetEventResults(ctx, s.db, eventID)
	return rows, full.FullRanking, err
}

func (s *Service) SetFullRanking(ctx context.Context, eventID string, full bool) (entryrepo.EventResultsRow, bool, error) {
	ok, err := entryrepo.SetResultsFullRanking(ctx, s.db, eventID, full)
	if err != nil || !ok {
		return entryrepo.EventResultsRow{}, ok, err
	}
	row, err := entryrepo.GetEventResults(ctx, s.db, eventID)
	return row, true, err
}

func (s *Service) ScoreCoverage(ctx context.Context, eventID string) (map[string]int, map[string]int, error) {
	return entryrepo.ScoreCoverage(ctx, s.db, eventID)
}
