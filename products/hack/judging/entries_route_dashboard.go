package judging

import (
	"context"

	"github.com/vsriram/simple-host/products/hack/app/entries"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"

	"github.com/vsriram/simple-host/products/hack/model"
)

type Dashboard struct {
	Coverage entries.SetupCoverage
	Teams    []DashboardTeam
	Judges   []DashboardJudge
	Former   []DashboardFormerJudge
	// Pool is what Start judging will do, for Overview's confirm.
	Pool entries.Pool
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

func (h *handler) JudgingDashboard(ctx context.Context, ev model.Event) (Dashboard, error) {
	var d Dashboard
	teams, err := h.Entries.TeamsWithMembers(ctx, ev.ID)
	if err != nil {
		return d, err
	}
	if d.Pool, err = h.Entries.JudgingPool(ctx, ev, teams); err != nil {
		return d, err
	}
	judges, err := h.Entries.EventJudges(ctx, ev.ID)
	if err != nil {
		return d, err
	}
	scoredTeams, scoredJudges, err := h.Entries.ScoreCoverage(ctx, ev.ID)
	if err != nil {
		return d, err
	}
	assigned, eligible, err := h.Entries.EligibilityCounts(ctx, ev, judges)
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
	if d.Former, err = h.formerJudgeProgress(ctx, ev, scoredJudges); err != nil {
		return d, err
	}
	d.Coverage, err = h.Entries.JudgingSetupCoverage(ctx, ev)
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
func (h *handler) formerJudgeProgress(ctx context.Context, ev model.Event, scoredJudges map[string]int) ([]DashboardFormerJudge, error) {
	former, err := h.Entries.FormerJudges(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	out := make([]DashboardFormerJudge, 0, len(former))
	for _, f := range former {
		n, err := entryrepo.FormerJudgeAssignedCount(ctx, h.DB, ev, f.UserID)
		if err != nil {
			return nil, err
		}
		out = append(out, DashboardFormerJudge{FormerJudge: f, Counted: scoredJudges[f.UserID], Assigned: n})
	}
	return out, nil
}
