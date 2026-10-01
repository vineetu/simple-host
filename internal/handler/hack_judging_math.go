package handler

import (
	"context"
	"math"
	"sort"

	"github.com/vsriram/simple-host/internal/db"
)

type judgeEntry struct {
	team, judge string
	raw         float64
	criteria    map[string]float64
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
func normalisedEntries(entries []judgeEntry) map[string]float64 {
	all := make([]float64, 0, len(entries))
	byJudge := map[string][]float64{}
	for _, e := range entries {
		all = append(all, e.raw)
		byJudge[e.judge] = append(byJudge[e.judge], e.raw)
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
		st := stats[e.judge]
		if globalSpread > 0 && st[1] > 0 {
			v = globalMean + (e.raw-st[0])*globalSpread/st[1]
		}
		out[judgeTeamKey(e.judge, e.team)] = v
	}
	return out
}

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
	weights, maxes := map[string]float64{}, map[string]float64{}
	for _, c := range rubric {
		weights[c.ID] = float64(c.Weight)
		maxes[c.ID] = float64(c.MaxPoints)
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
	byPair := map[string]*judgeEntry{}
	for _, r := range raw {
		if !eligible[r.JudgeID][r.TeamID] || maxes[r.CriterionID] <= 0 {
			continue
		}
		key := judgeTeamKey(r.JudgeID, r.TeamID)
		e := byPair[key]
		if e == nil {
			e = &judgeEntry{team: r.TeamID, judge: r.JudgeID, criteria: map[string]float64{}}
			byPair[key] = e
		}
		fraction := float64(r.Points) / maxes[r.CriterionID]
		e.raw += weights[r.CriterionID] * fraction
		e.criteria[r.CriterionID] = fraction * 100
	}
	entries := make([]judgeEntry, 0, len(byPair))
	byTeam := map[string][]judgeEntry{}
	for _, e := range byPair {
		entries = append(entries, *e)
		byTeam[e.team] = append(byTeam[e.team], *e)
	}
	normalised := normalisedEntries(entries)
	out := make([]teamResult, 0, len(teams))
	for _, team := range teams {
		items := byTeam[team.ID]
		res := teamResult{TeamID: team.ID, TeamName: team.Name, JudgesScored: len(items), ScoreMode: ev.ScoreMode, judgeScores: map[string]float64{}}
		if len(items) > 0 {
			rawSum, normSum, criterionSum, criterionCount := 0.0, 0.0, 0.0, 0
			for _, e := range items {
				rawSum += e.raw
				normSum += normalised[judgeTeamKey(e.judge, e.team)]
				res.judgeScores[e.judge] = e.raw
				if ev.TieCriterionID.Valid {
					if v, ok := e.criteria[ev.TieCriterionID.String]; ok {
						criterionSum += v
						criterionCount++
					}
				}
			}
			rawMean, normMean := roundedScore(rawSum/float64(len(items))), roundedScore(normSum/float64(len(items)))
			res.RawTotal = &rawMean
			res.NormalisedTotal = &normMean
			res.Total = &rawMean
			if ev.ScoreMode == "normalised" {
				res.Total = &normMean
			}
			if criterionCount > 0 {
				res.criterionMean = roundedScore(criterionSum / float64(criterionCount))
			}
		}
		track, err := db.TrackForTeam(ctx, q, ev.ID, team.ID)
		if err != nil {
			return nil, err
		}
		res.Track = track
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
			if ev.TieCriterionID.Valid {
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
	markTrackWinners(out)
	return out, nil
}

func markTrackWinners(rows []teamResult) {
	byTrack := map[string][]int{}
	for i := range rows {
		rows[i].TrackRank = nil
		rows[i].TrackWinner = false
		if rows[i].Track != nil && rows[i].Total != nil {
			byTrack[rows[i].Track.ID] = append(byTrack[rows[i].Track.ID], i)
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
