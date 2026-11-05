package entries

import (
	"context"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// QueueTeamError identifies the team whose completion query failed.
type QueueTeamError struct {
	TeamID string
	Err    error
}

func (e *QueueTeamError) Error() string { return e.Err.Error() }

// Unwrap returns the completion query error.
func (e *QueueTeamError) Unwrap() error { return e.Err }

// QueueRow holds the values needed to render one judge queue entry.
type QueueRow struct {
	Team        model.NamedTeam
	Entry       model.EventEntry
	Done        bool
	ScoresCount int
}

// JudgeQueue gathers and orders the entries visible to a judge.
func (s *Service) JudgeQueue(ctx context.Context, ev model.Event, judgeID string, loadTeams func() error) ([]QueueRow, error) {
	teams, err := entryrepo.ListTeamsWithMembers(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	pairs, err := entryrepo.ListConflictPairs(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	conflicted := map[string]bool{}
	for _, p := range pairs {
		if p.JudgeID == judgeID {
			conflicted[p.TeamID] = true
		}
	}
	coverage, _, err := entryrepo.ScoreCoverage(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	if ev.JudgeAssignmentMode != "open" {
		assigned, err := entryrepo.EligibleJudgeTeams(ctx, s.db, ev.ID, judgeID)
		if err != nil {
			return nil, err
		}
		filtered := make([]model.NamedTeam, 0, len(assigned))
		for _, t := range teams {
			if assigned[t.ID] {
				filtered = append(filtered, t)
			}
		}
		teams = filtered
	}
	rubric, err := entryrepo.ListRubric(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	if err := loadTeams(); err != nil {
		return nil, err
	}
	stored, err := entryrepo.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	byTeam := map[string]model.EventEntry{}
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	out := make([]QueueRow, 0, len(teams))
	for _, team := range teams {
		entry := byTeam[team.ID]
		if !hackentries.Judgeable(entry) || conflicted[team.ID] {
			continue
		}
		done, err := entryrepo.JudgeDoneForTeam(ctx, s.db, ev.ID, judgeID, team.ID, len(rubric))
		if err != nil {
			return nil, &QueueTeamError{TeamID: team.ID, Err: err}
		}
		out = append(out, QueueRow{Team: team, Entry: entry, Done: done, ScoresCount: coverage[team.ID]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ScoresCount != out[j].ScoresCount {
			return out[i].ScoresCount < out[j].ScoresCount
		}
		return strings.ToLower(out[i].Team.Name) < strings.ToLower(out[j].Team.Name)
	})
	return out, nil
}

// UnscoredTeams is the events service's EntryEvaluator.UnscoredTeams
// (0003-hack-notices-judges-messages.sql): how many of this judge's queue
// rows are not yet Done. Used for judging_open/judging_reminder's N, both
// live (NeedsYou) and at send time (the mail sweep and the reminder sweep).
func (s *Service) UnscoredTeams(ctx context.Context, ev model.Event, judgeID string) (int, error) {
	_, unscored, err := s.QueueCounts(ctx, ev, judgeID)
	return unscored, err
}

// QueueCounts is the events service's EntryEvaluator.QueueCounts
// (0003-hack-notices-judges-messages.sql): the judge's queue size and how
// many of those rows are not yet Done. Lets judging_open distinguish an
// empty queue ("No teams are in your queue yet.") from a populated queue
// the judge has already finished ("You've scored every team in your
// queue."), which a bare unscored count of 0 cannot tell apart.
func (s *Service) QueueCounts(ctx context.Context, ev model.Event, judgeID string) (total, unscored int, err error) {
	rows, err := s.JudgeQueue(ctx, ev, judgeID, func() error { return nil })
	if err != nil {
		return 0, 0, err
	}
	for _, r := range rows {
		if !r.Done {
			unscored++
		}
	}
	return len(rows), unscored, nil
}

// Pool is the prospective judging pool used before entries settle.
type Pool struct {
	In                        map[string]bool
	Judged, NoEntry, Excluded int
}

// JudgingPool gathers the prospective pool, consulting site state only when needed.
func (s *Service) JudgingPool(ctx context.Context, ev model.Event, teams []model.NamedTeam, live func(model.NamedTeam) (bool, error)) (Pool, error) {
	pool := Pool{In: map[string]bool{}}
	entries, err := entryrepo.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return pool, err
	}
	byTeam := map[string]model.EventEntry{}
	for _, e := range entries {
		byTeam[e.TeamID] = e
	}
	for _, t := range teams {
		e, ok := byTeam[t.ID]
		switch {
		case ok && e.ExcludedAt.Valid:
			pool.Excluded++
			continue
		case ok:
			pool.In[t.ID] = true
			pool.Judged++
			continue
		}
		isLive, err := live(t)
		if err != nil {
			return pool, err
		}
		if isLive {
			pool.In[t.ID] = true
			pool.Judged++
		} else {
			pool.NoEntry++
		}
	}
	return pool, nil
}

// EligibilityCounts returns eligible team counts per judge and judge counts per team.
func (s *Service) EligibilityCounts(ctx context.Context, eventID string, judges []model.NamedJudge) (map[string]int, map[string]int, error) {
	assigned := map[string]int{}
	byTeam := map[string]int{}
	for _, judge := range judges {
		eligible, err := entryrepo.EligibleJudgeTeams(ctx, s.db, eventID, judge.ID)
		if err != nil {
			return nil, nil, err
		}
		assigned[judge.ID] = len(eligible)
		for teamID := range eligible {
			byTeam[teamID]++
		}
	}
	return assigned, byTeam, nil
}
