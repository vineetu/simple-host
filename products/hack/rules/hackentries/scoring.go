package hackentries

import (
	"fmt"
	"github.com/vsriram/simple-host/products/hack/model"
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
	groupOf := map[string]int{} // tied team -> its shared rank
	for _, r := range results {
		if r.Tied && r.Rank != nil {
			groupOf[r.TeamID] = *r.Rank
		}
	}
	chosen := map[string]int{}
	touched := map[int]bool{}
	for _, ov := range overrides {
		group, ok := groupOf[ov.TeamID]
		if !ok {
			return fmt.Errorf("%s is not part of a tie.", ov.TeamID)
		}
		if _, duplicate := chosen[ov.TeamID]; duplicate {
			return fmt.Errorf("name each tied team once")
		}
		chosen[ov.TeamID] = ov.Rank
		touched[group] = true
	}
	if err := checkChosenPlaces(results, groupOf, touched, chosen); err != nil {
		return err
	}
	for _, run := range equalRuns(results, func(a, b TeamResult[T]) bool { return groupOf[a.TeamID] != 0 && groupOf[a.TeamID] == groupOf[b.TeamID] }) {
		if touched[groupOf[run[0].TeamID]] {
			sort.SliceStable(run, func(i, j int) bool { return chosen[run[i].TeamID] < chosen[run[j].TeamID] })
		}
	}
	rerankAfterOverrides(results, groupOf, touched)
	MarkTrackWinners(results, trackIDs)
	return nil
}

// checkChosenPlaces requires every team of a touched tie group to have a
// place, each different from the rest of its group.
func checkChosenPlaces[T any](results []TeamResult[T], groupOf map[string]int, touched map[int]bool, chosen map[string]int) error {
	used := map[[2]int]bool{} // group, place
	for _, r := range results {
		group := groupOf[r.TeamID]
		if !touched[group] {
			continue
		}
		place, ok := chosen[r.TeamID]
		if !ok {
			return fmt.Errorf("give every tied team in a group a rank, or none of them")
		}
		if used[[2]int{group, place}] {
			return fmt.Errorf("tied teams need different ranks from each other")
		}
		used[[2]int{group, place}] = true
	}
	return nil
}

// rerankAfterOverrides rebuilds dense ranks: untouched tie groups share a rank
// and stay tied, and teams placed by the organiser are marked as such.
func rerankAfterOverrides[T any](results []TeamResult[T], groupOf map[string]int, touched map[int]bool) {
	rank := 0
	for i := range results {
		r := &results[i]
		r.Tied = false
		if r.Total == nil {
			r.Rank = nil
			continue
		}
		group := groupOf[r.TeamID]
		if touched[group] {
			r.TieDecider = "organiser"
		}
		if i > 0 && group != 0 && !touched[group] && groupOf[results[i-1].TeamID] == group {
			r.Rank, r.Tied, results[i-1].Tied = results[i-1].Rank, true, true
			continue
		}
		rank++
		n := rank
		r.Rank = &n
	}
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
	entries := judgeEntries(in.Rubric, in.Raw, in.Eligible)
	normalised := normalisedEntries(entries)
	byTeam := map[string][]JudgeEntry{}
	for _, e := range entries {
		byTeam[e.Team] = append(byTeam[e.Team], e)
	}
	out := make([]TeamResult[T], 0, len(in.Teams))
	for _, team := range in.Teams {
		out = append(out, teamResult(in, team, byTeam[team.ID], normalised))
	}
	sortByTotal(out)
	for _, group := range equalRuns(out, func(a, b TeamResult[T]) bool { return a.Total != nil && b.Total != nil && *a.Total == *b.Total }) {
		breakTies(group, in.HasTieCriterion)
	}
	rankResults(out)
	MarkTrackWinners(out, in.TrackIDs)
	return out
}

// judgeEntries sums each eligible judge's weighted score per team, keeping
// every criterion as a percentage of its maximum.
func judgeEntries(rubric []model.RubricCriterion, raw []model.RawScoreRow, eligible map[string]map[string]bool) []JudgeEntry {
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
		if byPair[key] == nil {
			byPair[key] = &JudgeEntry{Team: r.TeamID, Judge: r.JudgeID, Criteria: map[string]float64{}}
		}
		fraction := float64(r.Points) / maxes[r.CriterionID]
		byPair[key].Raw += weights[r.CriterionID] * fraction
		byPair[key].Criteria[r.CriterionID] = fraction * 100
	}
	entries := make([]JudgeEntry, 0, len(byPair))
	for _, e := range byPair {
		entries = append(entries, *e)
	}
	return entries
}

// teamResult averages a team's judge entries into raw, normalised and
// selected totals plus the tie criterion's mean; no entries means no total.
func teamResult[T any](in ResultsInput[T], team model.NamedTeam, items []JudgeEntry, normalised map[string]float64) TeamResult[T] {
	res := TeamResult[T]{TeamID: team.ID, TeamName: team.Name, JudgesScored: len(items), ScoreMode: in.ScoreMode, judgeScores: map[string]float64{}, Track: in.Tracks[team.ID]}
	if len(items) == 0 {
		return res
	}
	rawSum, normSum, criterionSum, criterionCount := 0.0, 0.0, 0.0, 0
	for _, e := range items {
		rawSum += e.Raw
		normSum += normalised[JudgeTeamKey(e.Judge, e.Team)]
		res.judgeScores[e.Judge] = e.Raw
		if v, ok := e.Criteria[in.TieCriterionID]; ok && in.HasTieCriterion {
			criterionSum += v
			criterionCount++
		}
	}
	rawMean, normMean := roundedScore(rawSum/float64(len(items))), roundedScore(normSum/float64(len(items)))
	res.RawTotal, res.NormalisedTotal, res.Total = &rawMean, &normMean, &rawMean
	if in.ScoreMode == "normalised" {
		res.Total = &normMean
	}
	if criterionCount > 0 {
		res.criterionMean = roundedScore(criterionSum / float64(criterionCount))
	}
	return res
}

// sortByTotal orders by total descending then name; teams without a total go last by name.
func sortByTotal[T any](out []TeamResult[T]) {
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Total == nil:
			return b.Total == nil && a.TeamName < b.TeamName
		case b.Total == nil:
			return true
		case *a.Total != *b.Total:
			return *a.Total > *b.Total
		}
		return a.TeamName < b.TeamName
	})
}

// equalRuns splits rows into consecutive runs whose rows are the same as the run's first.
func equalRuns[T any](rows []TeamResult[T], same func(a, b TeamResult[T]) bool) [][]TeamResult[T] {
	var runs [][]TeamResult[T]
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && same(rows[start], rows[end]) {
			end++
		}
		runs = append(runs, rows[start:end])
		start = end
	}
	return runs
}

// breakTies orders teams with equal totals by the tie criterion's mean, then,
// among equal means, by how many pairwise judge preferences each team wins.
// Any residual tie stays unresolved until an organiser records a choice at publish.
func breakTies[T any](group []TeamResult[T], byCriterion bool) {
	if byCriterion {
		sort.SliceStable(group, func(i, j int) bool { return group[i].criterionMean > group[j].criterionMean })
	}
	for _, sub := range equalRuns(group, func(a, b TeamResult[T]) bool { return a.criterionMean == b.criterionMean }) {
		for i := range sub {
			sub[i].majorityWins = majorityWins(sub, i)
		}
		sort.SliceStable(sub, func(i, j int) bool { return sub[i].majorityWins > sub[j].majorityWins })
	}
}

// majorityWins counts, over every other team in the group, the judges who
// scored team i higher than that team.
func majorityWins[T any](group []TeamResult[T], i int) int {
	wins := 0
	for j := range group {
		if i == j {
			continue
		}
		for judge, a := range group[i].judgeScores {
			if b, ok := group[j].judgeScores[judge]; ok && a > b {
				wins++
			}
		}
	}
	return wins
}

// rankResults assigns dense ranks in sorted order, naming what separated each
// team from the one above; teams nothing separates share a rank as unresolved.
func rankResults[T any](out []TeamResult[T]) {
	rank := 0
	for i := range out {
		if out[i].Total == nil {
			continue
		}
		rank++
		r := rank
		out[i].Rank, out[i].TieDecider = &r, "score"
		if i == 0 || out[i-1].Total == nil || *out[i].Total != *out[i-1].Total {
			continue
		}
		switch {
		case out[i].criterionMean != out[i-1].criterionMean:
			out[i].TieDecider = "criterion"
		case out[i].majorityWins != out[i-1].majorityWins:
			out[i].TieDecider = "judge_majority"
		default:
			out[i].Rank = out[i-1].Rank
			rank--
			out[i].Tied, out[i-1].Tied = true, true
			out[i].TieDecider, out[i-1].TieDecider = "unresolved", "unresolved"
		}
	}
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
