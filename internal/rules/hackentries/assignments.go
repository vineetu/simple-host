package hackentries

import (
	"github.com/vsriram/simple-host/internal/model"
	"sort"
)

// JudgeTeamKey identifies a judge and team pair.
func JudgeTeamKey(judgeID, teamID string) string {
	return judgeID + "\x00" + teamID
}

// PlanAssignments spreads judges across teams. Each team gets up to
// perTeam judges, fewer only when fewer judges are free of a conflict with
// that team — that shortage is reported, not refused. Eligible judges are
// taken least-loaded first, and id breaks ties, which is the fair share: a
// judge who already has as many teams as another eligible judge is skipped
// until the loads even out. The same input always yields the same plan.
// Teams and judges are sorted by id here, so the caller need not.
func PlanAssignments(teams []model.NamedTeam, judges []model.NamedJudge, conflicted map[string]bool, perTeam int) ([]model.JudgeAssignment, []string) {
	teams = append([]model.NamedTeam(nil), teams...)
	judges = append([]model.NamedJudge(nil), judges...)
	sort.Slice(teams, func(i, j int) bool { return teams[i].ID < teams[j].ID })
	sort.Slice(judges, func(i, j int) bool { return judges[i].ID < judges[j].ID })
	if perTeam < 0 {
		perTeam = 0
	}
	load := make([]int, len(judges))
	assignments := make([]model.JudgeAssignment, 0)
	under := make([]string, 0)
	for _, team := range teams {
		eligible := make([]int, 0, len(judges))
		for i, judge := range judges {
			if conflicted[JudgeTeamKey(judge.ID, team.ID)] {
				continue
			}
			eligible = append(eligible, i)
		}
		sort.Slice(eligible, func(a, b int) bool {
			ia, ib := eligible[a], eligible[b]
			if load[ia] != load[ib] {
				return load[ia] < load[ib]
			}
			return judges[ia].ID < judges[ib].ID
		})
		n := perTeam
		if n > len(eligible) {
			n = len(eligible)
		}
		if n < perTeam {
			under = append(under, team.ID)
		}
		for k := 0; k < n; k++ {
			i := eligible[k]
			load[i]++
			assignments = append(assignments, model.JudgeAssignment{
				JudgeID:   judges[i].ID,
				JudgeName: judges[i].Name,
				TeamID:    team.ID,
				TeamName:  team.Name,
			})
		}
	}
	sort.Slice(assignments, func(i, j int) bool {
		if assignments[i].TeamID != assignments[j].TeamID {
			return assignments[i].TeamID < assignments[j].TeamID
		}
		return assignments[i].JudgeID < assignments[j].JudgeID
	})
	return assignments, under
}
