package entries

import (
	"context"
	"sort"
	"strings"

	hostmodel "github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

type QueueRow struct {
	Team        model.NamedTeam
	Entry       model.EventEntry
	Done        bool
	ScoresCount int
	Conflicted  bool
}

// JudgeCandidates includes mode-eligible, judgeable teams before own conflicts are subtracted.
func (s *Service) JudgeCandidates(ctx context.Context, ev model.Event, judgeID string) ([]QueueRow, error) {
	return s.judgeQueue(ctx, ev, judgeID, true)
}

// judgeQueue lists the judge's judgeable teams; candidates keeps their own conflicts.
func (s *Service) judgeQueue(ctx context.Context, ev model.Event, judgeID string, candidates bool) ([]QueueRow, error) {
	judge, err := entryrepo.EventJudgeInEvent(ctx, s.db, ev.ID, judgeID)
	if err != nil {
		return nil, err
	}
	if !judge {
		return nil, &Error{Code: "judge_not_found", Message: "You are not a judge of this event."}
	}
	teams, err := s.queueTeams(ctx, ev, judgeID)
	if err != nil {
		return nil, err
	}
	pairs, err := entryrepo.ListConflictPairs(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	coverage, _, err := entryrepo.ScoreCoverage(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	rubric, err := entryrepo.ListRubric(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	stored, err := entryrepo.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	completions, err := entryrepo.JudgeCompletionCounts(ctx, s.db, ev.ID, judgeID)
	if err != nil {
		return nil, err
	}
	conflicted := judgeConflicts(pairs, judgeID)
	byTeam := entriesByTeam(stored)
	out := make([]QueueRow, 0, len(teams))
	for _, team := range teams {
		entry := byTeam[team.ID]
		if !hackentries.Judgeable(entry) || (!candidates && conflicted[team.ID]) {
			continue
		}
		done := len(rubric) > 0 && completions[judgeID][team.ID] >= len(rubric)
		out = append(out, QueueRow{Team: team, Entry: entry, Done: done, ScoresCount: coverage[team.ID], Conflicted: conflicted[team.ID]})
	}
	sortQueue(out)
	return out, nil
}

// judgeConflicts is the set of teams judgeID declared a conflict with.
func judgeConflicts(pairs []entryrepo.ConflictPair, judgeID string) map[string]bool {
	out := map[string]bool{}
	for _, p := range pairs {
		if p.JudgeID == judgeID {
			out[p.TeamID] = true
		}
	}
	return out
}

// sortQueue orders rows least-scored first, then by team name ignoring case.
func sortQueue(rows []QueueRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].ScoresCount != rows[j].ScoresCount {
			return rows[i].ScoresCount < rows[j].ScoresCount
		}
		return strings.ToLower(rows[i].Team.Name) < strings.ToLower(rows[j].Team.Name)
	})
}

// queueTeams is every team in open assignment, otherwise those assigned to the judge.
func (s *Service) queueTeams(ctx context.Context, ev model.Event, judgeID string) ([]model.NamedTeam, error) {
	teams, err := entryrepo.ListTeamsWithMembers(ctx, s.db, ev.ID)
	if err != nil || ev.JudgeAssignmentMode == "open" {
		return teams, err
	}
	assigned, err := entryrepo.EligibleJudgeTeamsIgnoringConflicts(ctx, s.db, ev, judgeID)
	if err != nil {
		return nil, err
	}
	out := make([]model.NamedTeam, 0, len(assigned))
	for _, t := range teams {
		if assigned[t.ID] {
			out = append(out, t)
		}
	}
	return out, nil
}

func (s *Service) UnscoredTeams(ctx context.Context, ev model.Event, judgeID string) (int, error) {
	_, unscored, err := s.QueueCounts(ctx, ev, judgeID)
	return unscored, err
}

// QueueCounts is the judge's queue size and how many rows are not yet Done.
func (s *Service) QueueCounts(ctx context.Context, ev model.Event, judgeID string) (total, unscored int, err error) {
	rows, err := s.judgeQueue(ctx, ev, judgeID, false)
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

type Pool struct {
	In                        map[string]bool
	Judged, NoEntry, Excluded int
}

// JudgingPool gathers the prospective pool: teams with an entry not
// excluded, and teams without one whose site is live (TeamSiteLive).
func (s *Service) JudgingPool(ctx context.Context, ev model.Event, teams []model.NamedTeam) (Pool, error) {
	return s.judgingPool(ctx, ev, teams, s.poolSiteLive(ctx, ev))
}

// poolSiteLive reports whether a team's site is live, loading the event
// account's sites and the teams' states once, on first use.
func (s *Service) poolSiteLive(ctx context.Context, ev model.Event) func(model.NamedTeam) (bool, error) {
	var sitesByName map[string]hostmodel.Site
	var states map[string]model.TeamWriteState
	return func(t model.NamedTeam) (bool, error) {
		if sitesByName == nil {
			list, err := sites.AccountSites(ctx, s.db, ev.AccountID)
			if err != nil {
				return false, err
			}
			if states, err = teamrepo.TeamWriteStatesForEvent(ctx, s.db, ev.ID); err != nil {
				return false, err
			}
			sitesByName = make(map[string]hostmodel.Site, len(list))
			for _, site := range list {
				sitesByName[site.Name] = site
			}
		}
		site, ok := sitesByName[t.Slug]
		return ok && TeamSiteLive(&site, states[t.ID]), nil
	}
}

// judgingPool is JudgingPool with the site check given.
func (s *Service) judgingPool(ctx context.Context, ev model.Event, teams []model.NamedTeam, live func(model.NamedTeam) (bool, error)) (Pool, error) {
	pool := Pool{In: map[string]bool{}}
	entries, err := entryrepo.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return pool, err
	}
	byTeam := entriesByTeam(entries)
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
func (s *Service) EligibilityCounts(ctx context.Context, ev model.Event, judges []model.NamedJudge) (map[string]int, map[string]int, error) {
	assigned := map[string]int{}
	byTeam := map[string]int{}
	for _, judge := range judges {
		eligible, err := entryrepo.EligibleJudgeTeams(ctx, s.db, ev, judge.ID)
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

func entriesByTeam(entries []model.EventEntry) map[string]model.EventEntry {
	out := make(map[string]model.EventEntry, len(entries))
	for _, e := range entries {
		out[e.TeamID] = e
	}
	return out
}
