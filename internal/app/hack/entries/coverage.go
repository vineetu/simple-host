package entries

import (
	"context"
	"fmt"
	"strings"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	navigation "github.com/vsriram/simple-host/internal/rules/hackevents"
)

// SetupCoverage describes prospective judging coverage without writing assignments.
type SetupCoverage struct {
	Target    int             `json:"target"`
	Min       int             `json:"min"`
	Max       int             `json:"max"`
	FixTarget string          `json:"fix_target"`
	FixHref   string          `json:"fix_href"`
	Short     []CoverageShort `json:"short"`
}
type CoverageShort struct {
	TeamID       string `json:"team_id"`
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Have         int    `json:"have"`
	MissingTrack bool   `json:"missing_track,omitempty"`
}

func (c SetupCoverage) Detail() string {
	if len(c.Short) == 0 {
		return fmt.Sprintf("Every team has at least %d %s", c.Min, checklistNoun(c.Min, "judge"))
	}
	names := []string{}
	for _, t := range c.Short {
		names = append(names, fmt.Sprintf("%s (%d of %d)", t.Name, t.Have, c.Target))
	}
	return "Short: " + strings.Join(names, ", ")
}

// JudgingSetupCoverage uses the same prospective pool as generation and stage confirmation.
func (s *Service) JudgingSetupCoverage(ctx context.Context, ev model.Event) (SetupCoverage, error) {
	out := SetupCoverage{Target: ev.JudgesPerTeam, Short: []CoverageShort{}}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	pool, err := s.JudgingPool(ctx, ev, teams, func(t model.NamedTeam) (bool, error) {
		st, err := teamrepo.TeamWriteStateFor(ctx, s.db, t.ID, false)
		if err != nil {
			return false, err
		}
		return s.siteLive != nil && s.siteLive(ctx, ev, t.Slug, st.SiteTakenDown), nil
	})
	if err != nil {
		return out, err
	}
	candidates := []model.NamedTeam{}
	for _, t := range teams {
		if pool.In[t.ID] {
			candidates = append(candidates, t)
		}
	}
	judges, err := entryrepo.ListEventJudges(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	pairs, err := entryrepo.ListConflictPairs(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	conflicts := map[string]bool{}
	for _, p := range pairs {
		conflicts[hackentries.JudgeTeamKey(p.JudgeID, p.TeamID)] = true
	}
	assignments, err := entryrepo.ListEventAssignments(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	panels, err := entryrepo.ListTrackPanels(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	tracks, err := entryrepo.EventTeamTracks(ctx, s.db, ev.ID)
	if err != nil {
		return out, err
	}
	if ev.JudgeAssignmentMode == "automatic" {
		assignments, _ = hackentries.PlanAssignments(candidates, judges, conflicts, ev.JudgesPerTeam)
	}
	out.Min = len(judges)
	if ev.JudgeAssignmentMode == "open" {
		out.Target = max(1, len(judges))
	}
	conflictShort := false
	for _, t := range candidates {
		have := 0
		for _, j := range judges {
			if conflicts[hackentries.JudgeTeamKey(j.ID, t.ID)] {
				continue
			}
			eligible := ev.JudgeAssignmentMode == "open"
			if ev.JudgeAssignmentMode == "automatic" || ev.JudgeAssignmentMode == "manual" {
				for _, a := range assignments {
					if a.JudgeID == j.ID && a.TeamID == t.ID {
						eligible = true
						break
					}
				}
			}
			if ev.JudgeAssignmentMode == "panel" && tracks[t.ID] != nil {
				for _, p := range panels {
					if p.JudgeID == j.ID && p.TrackID == tracks[t.ID].ID {
						eligible = true
						break
					}
				}
			}
			if eligible {
				have++
			}
		}
		if have < out.Min {
			out.Min = have
		}
		if have > out.Max {
			out.Max = have
		}
		if have < out.Target {
			for _, j := range judges {
				if conflicts[hackentries.JudgeTeamKey(j.ID, t.ID)] {
					conflictShort = true
				}
			}
			out.Short = append(out.Short, CoverageShort{TeamID: t.ID, Name: t.Name, Slug: t.Slug, Have: have, MissingTrack: ev.JudgeAssignmentMode == "panel" && tracks[t.ID] == nil})
		}
	}
	if len(candidates) == 0 {
		out.Min = 0
	}
	out.FixTarget = "o.organiser-judges"
	params := map[string]string{}
	if len(judges) > 0 {
		if ev.JudgeAssignmentMode == "manual" {
			out.FixTarget = "o.assignments"
		}
		if ev.JudgeAssignmentMode == "panel" {
			out.FixTarget = "o.panel-assignments"
			for _, t := range out.Short {
				if t.MissingTrack {
					out.FixTarget = "o.team-track"
					params["team"] = t.Slug
					break
				}
			}
			eventTracks, err := eventrepo.ListEventTracks(ctx, s.db, ev.ID)
			if err != nil {
				return out, err
			}
			if len(eventTracks) == 0 {
				out.FixTarget = "o.tracks"
			}
		}
		if conflictShort {
			out.FixTarget = "o.conflicts"
		}
	}
	link, err := navigation.Resolve(out.FixTarget, ev.Slug, params)
	if err != nil {
		return out, err
	}
	out.FixHref = link.Href
	return out, nil
}
