package hackentries

import (
	"fmt"
	"github.com/vsriram/simple-host/internal/model"
	"math"
	"sort"
)

// TeamResult is one team's computed standing.
type TeamResult[T any] struct {
	TeamID          string
	TeamName        string
	Total           *float64
	RawTotal        *float64
	NormalisedTotal *float64
	ScoreMode       string
	Track           *T
	TrackRank       *int
	TrackWinner     bool
	TieDecider      string
	criterionMean   float64
	majorityWins    int
	judgeScores     map[string]float64
	Rank            *int
	Tied            bool
	JudgesScored    int
}

// RankOverride is one team's chosen final place within its own tied group.
type RankOverride struct {
	TeamID string
	Rank   int
}

// ApplyRankOverrides orders complete unresolved tie groups by the organiser's
// chosen places, then rebuilds dense ranks. Untouched groups remain tied.
func ApplyRankOverrides[T any](results []TeamResult[T], overrides []RankOverride, trackIDs map[string]string) error {
	groups := map[int][]int{}
	byID := map[string]int{}
	for i := range results {
		byID[results[i].TeamID] = i
		if results[i].Tied && results[i].Rank != nil {
			groups[*results[i].Rank] = append(groups[*results[i].Rank], i)
		}
	}
	assigned := map[string]int{}
	touched := map[int]bool{}
	for _, ov := range overrides {
		i, ok := byID[ov.TeamID]
		if !ok || !results[i].Tied || results[i].Rank == nil {
			return fmt.Errorf("%s is not part of a tie.", ov.TeamID)
		}
		if _, duplicate := assigned[ov.TeamID]; duplicate {
			return fmt.Errorf("name each tied team once")
		}
		assigned[ov.TeamID] = ov.Rank
		touched[*results[i].Rank] = true
	}
	oldGroup := map[string]int{}
	for rank, ids := range groups {
		for _, i := range ids {
			oldGroup[results[i].TeamID] = rank
		}
		if !touched[rank] {
			continue
		}
		seen := map[int]bool{}
		for _, i := range ids {
			order, ok := assigned[results[i].TeamID]
			if !ok {
				return fmt.Errorf("give every tied team in a group a rank, or none of them")
			}
			if seen[order] {
				return fmt.Errorf("tied teams need different ranks from each other")
			}
			seen[order] = true
		}
		start, end := ids[0], ids[len(ids)-1]+1
		sort.SliceStable(results[start:end], func(i, j int) bool { return assigned[results[start+i].TeamID] < assigned[results[start+j].TeamID] })
	}
	rank := 0
	for i := range results {
		if results[i].Total == nil {
			results[i].Rank = nil
			results[i].Tied = false
			continue
		}
		rank++
		r := rank
		results[i].Rank = &r
		results[i].Tied = false
		if i > 0 && oldGroup[results[i].TeamID] != 0 && oldGroup[results[i].TeamID] == oldGroup[results[i-1].TeamID] && !touched[oldGroup[results[i].TeamID]] {
			results[i].Rank = results[i-1].Rank
			results[i].Tied = true
			results[i-1].Tied = true
			rank--
		}
		if touched[oldGroup[results[i].TeamID]] {
			results[i].TieDecider = "organiser"
		}
	}
	MarkTrackWinners(results, trackIDs)
	return nil
}

// JudgeEntry holds one judge’s weighted score for a team.
type JudgeEntry struct {
	Team, Judge string
	Raw         float64
	Criteria    map[string]float64
}

func roundedScore(v float64) float64 { return math.Round(v*100) / 100 }

func meanSpread(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, v := range values {
		d := v - mean
		variance += d * d
	}
	return mean, math.Sqrt(variance / float64(len(values)))
}

// Normalisation uses the population of all currently eligible judge-entry
// weighted scores. A judge with zero spread contributes the global mean; a
// zero global spread also yields the global mean. This stays finite for one
// entry, all-equal scores, and conflicts that remove most of a population.
func normalisedEntries(entries []JudgeEntry) map[string]float64 {
	all := make([]float64, 0, len(entries))
	byJudge := map[string][]float64{}
	for _, e := range entries {
		all = append(all, e.Raw)
		byJudge[e.Judge] = append(byJudge[e.Judge], e.Raw)
	}
	globalMean, globalSpread := meanSpread(all)
	stats := map[string][2]float64{}
	for judge, vals := range byJudge {
		m, s := meanSpread(vals)
		stats[judge] = [2]float64{m, s}
	}
	out := map[string]float64{}
	for _, e := range entries {
		v := globalMean
		st := stats[e.Judge]
		if globalSpread > 0 && st[1] > 0 {
			v = globalMean + (e.Raw-st[0])*globalSpread/st[1]
		}
		out[JudgeTeamKey(e.Judge, e.Team)] = v
	}
	return out
}

// ResultsInput holds the data needed to compute standings.
type ResultsInput[T any] struct {
	Rubric          []model.RubricCriterion
	Teams           []model.NamedTeam
	Raw             []model.RawScoreRow
	Eligible        map[string]map[string]bool
	Tracks          map[string]*T
	TrackIDs        map[string]string
	ScoreMode       string
	TieCriterionID  string
	HasTieCriterion bool
}

// ComputeResults calculates the event standings.
func ComputeResults[T any](in ResultsInput[T]) []TeamResult[T] {
	rubric, teams, raw, eligible := in.Rubric, in.Teams, in.Raw, in.Eligible
	weights, maxes := map[string]float64{}, map[string]float64{}
	for _, c := range rubric {
		weights[c.ID] = float64(c.Weight)
		maxes[c.ID] = float64(c.MaxPoints)
	}
	byPair := map[string]*JudgeEntry{}
	for _, r := range raw {
		if !eligible[r.JudgeID][r.TeamID] || maxes[r.CriterionID] <= 0 {
			continue
		}
		key := JudgeTeamKey(r.JudgeID, r.TeamID)
		e := byPair[key]
		if e == nil {
			e = &JudgeEntry{Team: r.TeamID, Judge: r.JudgeID, Criteria: map[string]float64{}}
			byPair[key] = e
		}
		fraction := float64(r.Points) / maxes[r.CriterionID]
		e.Raw += weights[r.CriterionID] * fraction
		e.Criteria[r.CriterionID] = fraction * 100
	}
	entries := make([]JudgeEntry, 0, len(byPair))
	byTeam := map[string][]JudgeEntry{}
	for _, e := range byPair {
		entries = append(entries, *e)
		byTeam[e.Team] = append(byTeam[e.Team], *e)
	}
	normalised := normalisedEntries(entries)
	out := make([]TeamResult[T], 0, len(teams))
	for _, team := range teams {
		items := byTeam[team.ID]
		res := TeamResult[T]{TeamID: team.ID, TeamName: team.Name, JudgesScored: len(items), ScoreMode: in.ScoreMode, judgeScores: map[string]float64{}}
		if len(items) > 0 {
			rawSum, normSum, criterionSum, criterionCount := 0.0, 0.0, 0.0, 0
			for _, e := range items {
				rawSum += e.Raw
				normSum += normalised[JudgeTeamKey(e.Judge, e.Team)]
				res.judgeScores[e.Judge] = e.Raw
				if in.HasTieCriterion {
					if v, ok := e.Criteria[in.TieCriterionID]; ok {
						criterionSum += v
						criterionCount++
					}
				}
			}
			rawMean, normMean := roundedScore(rawSum/float64(len(items))), roundedScore(normSum/float64(len(items)))
			res.RawTotal = &rawMean
			res.NormalisedTotal = &normMean
			res.Total = &rawMean
			if in.ScoreMode == "normalised" {
				res.Total = &normMean
			}
			if criterionCount > 0 {
				res.criterionMean = roundedScore(criterionSum / float64(criterionCount))
			}
		}
		res.Track = in.Tracks[team.ID]
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Total == nil {
			return b.Total == nil && a.TeamName < b.TeamName
		}
		if b.Total == nil {
			return true
		}
		if *a.Total != *b.Total {
			return *a.Total > *b.Total
		}
		return a.TeamName < b.TeamName
	})
	// Among equal selected totals: criterion mean, then each judge's pairwise
	// preference within the remaining tied subgroup. Any residual tie stays
	// unresolved until an organiser records a choice at publish.
	for start := 0; start < len(out); {
		end := start + 1
		for end < len(out) && out[start].Total != nil && out[end].Total != nil && *out[start].Total == *out[end].Total {
			end++
		}
		if end-start > 1 {
			if in.HasTieCriterion {
				sort.SliceStable(out[start:end], func(i, j int) bool { return out[start+i].criterionMean > out[start+j].criterionMean })
			}
			for c := start; c < end; {
				d := c + 1
				for d < end && out[d].criterionMean == out[c].criterionMean {
					d++
				}
				if d-c > 1 {
					for i := c; i < d; i++ {
						wins := 0
						for j := c; j < d; j++ {
							if i == j {
								continue
							}
							for judge, a := range out[i].judgeScores {
								if b, ok := out[j].judgeScores[judge]; ok && a > b {
									wins++
								}
							}
						}
						out[i].majorityWins = wins
					}
					sort.SliceStable(out[c:d], func(i, j int) bool { return out[c+i].majorityWins > out[c+j].majorityWins })
				}
				c = d
			}
		}
		start = end
	}
	rank := 0
	for i := range out {
		if out[i].Total == nil {
			continue
		}
		rank++
		r := rank
		out[i].Rank = &r
		out[i].TieDecider = "score"
		if i > 0 && out[i-1].Total != nil && *out[i].Total == *out[i-1].Total {
			if out[i].criterionMean != out[i-1].criterionMean {
				out[i].TieDecider = "criterion"
			} else if out[i].majorityWins != out[i-1].majorityWins {
				out[i].TieDecider = "judge_majority"
			} else {
				out[i].Rank = out[i-1].Rank
				rank--
				out[i].Tied = true
				out[i-1].Tied = true
				out[i].TieDecider = "unresolved"
				out[i-1].TieDecider = "unresolved"
			}
		}
	}
	MarkTrackWinners(out, in.TrackIDs)
	return out
}

// MarkTrackWinners assigns track ranks.
func MarkTrackWinners[T any](rows []TeamResult[T], trackIDs map[string]string) {
	byTrack := map[string][]int{}
	for i := range rows {
		rows[i].TrackRank = nil
		rows[i].TrackWinner = false
		if rows[i].Track != nil && rows[i].Total != nil {
			byTrack[trackIDs[rows[i].TeamID]] = append(byTrack[trackIDs[rows[i].TeamID]], i)
		}
	}
	for _, ids := range byTrack {
		rank := 0
		for n, i := range ids {
			if n == 0 || rows[i].Rank == nil || rows[ids[n-1]].Rank == nil || *rows[i].Rank != *rows[ids[n-1]].Rank {
				rank++
			}
			trackRank := rank
			rows[i].TrackRank = &trackRank
			rows[i].TrackWinner = rank == 1
		}
	}
}
