package entries

import (
	"context"

	"github.com/vsriram/simple-host/products/hack/model"
)

type Dashboard struct {
	Coverage SetupCoverage
	Teams    []DashboardTeam
	Judges   []DashboardJudge
	Former   []DashboardFormerJudge
	// Pool is what Start judging will do, for Overview's confirm.
	Pool Pool
}

type DashboardTeam struct {
	ID, Name         string
	Scored, Eligible int
	// Flagged is set when fewer judges scored the team than its target.
	Flagged bool
}

type DashboardJudge struct {
	ID, Name       string
	Done, Assigned int
}

// DashboardFormerJudge is a judge who left, with the teams their scores
// still count for and the teams they had been assigned.
type DashboardFormerJudge struct {
	model.FormerJudge
	Counted, Assigned int
}

func (s *Service) JudgingDashboard(ctx context.Context, ev model.Event, live func(model.NamedTeam) (bool, error)) (Dashboard, error) {
	var d Dashboard
	teams, err := s.TeamsWithMembers(ctx, ev.ID)
	if err != nil {
		return d, err
	}
	if d.Pool, err = s.JudgingPool(ctx, ev, teams, live); err != nil {
		return d, err
	}
	judges, err := s.EventJudges(ctx, ev.ID)
	if err != nil {
		return d, err
	}
	scoredTeams, scoredJudges, err := s.ScoreCoverage(ctx, ev.ID)
	if err != nil {
		return d, err
	}
	assigned, eligible, err := s.EligibilityCounts(ctx, ev.ID, judges)
	if err != nil {
		return d, err
	}
	d.Teams = []DashboardTeam{}
	for _, t := range teams {
		if d.Pool.In[t.ID] {
			scored := scoredTeams[t.ID]
			d.Teams = append(d.Teams, DashboardTeam{ID: t.ID, Name: t.Name, Scored: scored, Eligible: eligible[t.ID], Flagged: scored < judgeTarget(ev, eligible[t.ID])})
		}
	}
	d.Judges = make([]DashboardJudge, 0, len(judges))
	for _, j := range judges {
		d.Judges = append(d.Judges, DashboardJudge{ID: j.ID, Name: j.Name, Done: scoredJudges[j.ID], Assigned: assigned[j.ID]})
	}
	if d.Former, err = s.formerJudgeProgress(ctx, ev.ID, scoredJudges); err != nil {
		return d, err
	}
	d.Coverage, err = s.JudgingSetupCoverage(ctx, ev)
	return d, err
}

// judgeTarget is how many judges a team should get; 2 is the column default in open mode.
func judgeTarget(ev model.Event, eligible int) int {
	switch ev.JudgeAssignmentMode {
	case "open":
		return 2
	case "manual", "panel":
		if eligible > 0 && eligible < ev.JudgesPerTeam {
			return eligible
		}
	}
	return ev.JudgesPerTeam
}

// formerJudgeProgress lists the judges who left with their counted and
// assigned teams.
func (s *Service) formerJudgeProgress(ctx context.Context, eventID string, scoredJudges map[string]int) ([]DashboardFormerJudge, error) {
	former, err := s.FormerJudges(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]DashboardFormerJudge, 0, len(former))
	for _, f := range former {
		n, err := s.FormerJudgeAssignedCountLookup(ctx, eventID, f.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, DashboardFormerJudge{FormerJudge: f, Counted: scoredJudges[f.UserID], Assigned: n})
	}
	return out, nil
}
