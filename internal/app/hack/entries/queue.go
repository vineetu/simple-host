package entries

import (
	"context"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// QueueRow holds the values needed to render one judge queue entry.
type QueueRow struct {
	Team        db.NamedTeam
	Entry       db.EventEntry
	Done        bool
	ScoresCount int
}

// JudgeQueue gathers and orders the entries visible to a judge.
func (s *Service) JudgeQueue(ctx context.Context, ev db.Event, judgeID string, loadTeams func() error) ([]QueueRow, error) {
	teams, err := db.ListTeamsWithMembers(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	pairs, err := db.ListConflictPairs(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	conflicted := map[string]bool{}
	for _, p := range pairs {
		if p.JudgeID == judgeID {
			conflicted[p.TeamID] = true
		}
	}
	coverage, _, err := db.ScoreCoverage(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	if ev.JudgeAssignmentMode != "open" {
		assigned, err := db.EligibleJudgeTeams(ctx, s.db, ev.ID, judgeID)
		if err != nil {
			return nil, err
		}
		filtered := make([]db.NamedTeam, 0, len(assigned))
		for _, t := range teams {
			if assigned[t.ID] {
				filtered = append(filtered, t)
			}
		}
		teams = filtered
	}
	rubric, err := db.ListRubric(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	if err := loadTeams(); err != nil {
		return nil, err
	}
	stored, err := db.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	byTeam := map[string]db.EventEntry{}
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	out := make([]QueueRow, 0, len(teams))
	for _, team := range teams {
		entry := byTeam[team.ID]
		if !hackentries.Judgeable(entry) || conflicted[team.ID] {
			continue
		}
		done := false
		if len(rubric) > 0 {
			var n int
			err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT criterion_id) FROM event_scores
    WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`, ev.ID, judgeID, team.ID).Scan(&n)
			if err != nil {
				return nil, err
			}
			done = n >= len(rubric)
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

// Pool is the prospective judging pool used before entries settle.
type Pool struct {
	In                        map[string]bool
	Judged, NoEntry, Excluded int
}

// JudgingPool gathers the prospective pool, consulting site state only when needed.
func (s *Service) JudgingPool(ctx context.Context, ev db.Event, teams []db.NamedTeam, live func(db.NamedTeam) (bool, error)) (Pool, error) {
	pool := Pool{In: map[string]bool{}}
	entries, err := db.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return pool, err
	}
	byTeam := map[string]db.EventEntry{}
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
func (s *Service) EligibilityCounts(ctx context.Context, eventID string, judges []db.NamedJudge) (map[string]int, map[string]int, error) {
	assigned := map[string]int{}
	byTeam := map[string]int{}
	for _, judge := range judges {
		eligible, err := db.EligibleJudgeTeams(ctx, s.db, eventID, judge.ID)
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
