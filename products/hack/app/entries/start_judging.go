package entries

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
)

// beforeJudging are the stages an event moves into judging from.
var beforeJudging = map[string]bool{"draft": true, "open": true, "building": true, "closed": true}

// AssignOnStartJudging spreads the judges over the judging pool when an
// event in automatic mode has just moved from an earlier stage into judging
// (from is the stage it left). siteLive says whether a team's site is live.
func (s *Service) AssignOnStartJudging(ctx context.Context, ev model.Event, from string, siteLive func(model.NamedTeam) (bool, error)) error {
	if ev.Stage != "judging" || !beforeJudging[from] || ev.JudgeAssignmentMode != "automatic" {
		return nil
	}
	teams, err := s.TeamsWithMembers(ctx, ev.ID)
	if err != nil {
		return err
	}
	pool, err := s.JudgingPool(ctx, ev, teams, siteLive)
	if err != nil {
		return err
	}
	_, _, err = s.GenerateAssignments(ctx, ev.ID, nil, pool.In, true)
	return err
}
