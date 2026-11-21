package entries

import (
	"context"

	model "github.com/vsriram/simple-host/products/hack/model"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackevents"
)

type EntryCounts map[string]int

// CountEntries tallies teams by entry state (missing entry: not started) and live site.
func CountEntries(teams []model.EventTeam, entries map[string]model.EventEntry, live map[string]bool) EntryCounts {
	counts := EntryCounts{"submitted": 0, "drafts": 0, "not_started": 0, "not_submitted": 0, "sites_live": 0}
	for _, team := range teams {
		counts[entryState(entries, team.ID)]++
		if live[team.ID] {
			counts["sites_live"]++
		}
	}
	return counts
}

// entryState is the count bucket of a team's entry.
func entryState(entries map[string]model.EventEntry, teamID string) string {
	e, ok := entries[teamID]
	switch {
	case !ok:
		return "not_started"
	case e.Status() == "submitted":
		return "submitted"
	case e.Status() == "not_submitted":
		return "not_submitted"
	}
	return "drafts"
}

// JudgeSiteLinks says per team whether the caller may see site links: organisers always, judges only for teams they score once the site is final.
func (s *Service) JudgeSiteLinks(ctx context.Context, ev model.Event, userID string, organiser, scoring bool) (func(teamID string) bool, error) {
	if organiser {
		return func(string) bool { return true }, nil
	}
	eligible, err := s.EligibleJudgeTeams(ctx, ev, userID)
	if err != nil {
		return nil, err
	}
	if hackevents.SubmissionsClosedStage(ev.Stage) || ev.Stage == "archived" || scoring {
		return func(teamID string) bool { return eligible[teamID] }, nil
	}
	states, err := teamrepo.TeamWriteStatesForEvent(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	return func(teamID string) bool {
		st, ok := states[teamID]
		return eligible[teamID] && ok && st.Frozen()
	}, nil
}
