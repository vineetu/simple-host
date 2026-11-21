package entries

import (
	"context"
	"fmt"
	"strings"

	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
	navigation "github.com/vsriram/simple-host/products/hack/rules/hackevents"
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
	in, err := s.loadCoverage(ctx, ev)
	if err != nil {
		return SetupCoverage{Target: ev.JudgesPerTeam, Short: []CoverageShort{}}, err
	}
	out := in.coverage(ev.JudgesPerTeam)
	target, teamSlug := in.fixTarget(out.Short)
	out.FixTarget = target
	link, err := navigation.Resolve(target, ev.Slug, map[string]string{"team": teamSlug})
	if err != nil {
		return out, err
	}
	out.FixHref = link.Href
	return out, nil
}

type coverageInput struct {
	mode        string
	candidates  []model.NamedTeam
	judges      []model.NamedJudge
	conflicts   map[string]bool // judge/team keys
	assigned    map[string]bool // judge/team keys
	panels      map[string]bool // judge/track keys
	tracks      map[string]*model.EventTrack
	eventTracks int
}

// loadCoverage reads the pool, judges, conflicts, assignments and panels;
// automatic mode replaces stored assignments with a fresh plan.
func (s *Service) loadCoverage(ctx context.Context, ev model.Event) (coverageInput, error) {
	in := coverageInput{mode: ev.JudgeAssignmentMode, conflicts: map[string]bool{}, assigned: map[string]bool{}, panels: map[string]bool{}}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, s.db, ev.ID)
	if err != nil {
		return in, err
	}
	if in.candidates, err = s.poolTeams(ctx, ev, teams); err != nil {
		return in, err
	}
	if in.judges, err = entryrepo.ListEventJudges(ctx, s.db, ev.ID); err != nil {
		return in, err
	}
	pairs, err := entryrepo.ListConflictPairs(ctx, s.db, ev.ID)
	if err != nil {
		return in, err
	}
	for _, p := range pairs {
		in.conflicts[hackentries.JudgeTeamKey(p.JudgeID, p.TeamID)] = true
	}
	assignments, err := entryrepo.ListEventAssignments(ctx, s.db, ev.ID)
	if err != nil {
		return in, err
	}
	panels, err := entryrepo.ListTrackPanels(ctx, s.db, ev.ID)
	if err != nil {
		return in, err
	}
	for _, p := range panels {
		in.panels[hackentries.JudgeTeamKey(p.JudgeID, p.TrackID)] = true
	}
	if in.tracks, err = teamrepo.EventTeamTracks(ctx, s.db, ev.ID); err != nil {
		return in, err
	}
	if in.mode == "automatic" {
		assignments, _ = hackentries.PlanAssignments(in.candidates, in.judges, in.conflicts, ev.JudgesPerTeam)
	}
	for _, a := range assignments {
		in.assigned[hackentries.JudgeTeamKey(a.JudgeID, a.TeamID)] = true
	}
	if in.mode == "panel" && len(in.judges) > 0 {
		eventTracks, err := eventrepo.ListEventTracks(ctx, s.db, ev.ID)
		in.eventTracks = len(eventTracks)
		return in, err
	}
	return in, nil
}

// poolTeams keeps the teams in the judging pool, counting a team without an
// entry when its site is live.
func (s *Service) poolTeams(ctx context.Context, ev model.Event, teams []model.NamedTeam) ([]model.NamedTeam, error) {
	states, err := teamrepo.TeamWriteStatesForEvent(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	pool, err := s.judgingPool(ctx, ev, teams, func(t model.NamedTeam) (bool, error) {
		st := states[t.ID]
		return s.siteLive != nil && s.siteLive(ctx, ev, t.Slug, st.SiteTakenDown), nil
	})
	candidates := []model.NamedTeam{}
	for _, t := range teams {
		if pool.In[t.ID] {
			candidates = append(candidates, t)
		}
	}
	return candidates, err
}

// eligible reports whether the assignment mode lets a judge score a team,
// before conflicts are applied.
func (in coverageInput) eligible(judgeID, teamID string) bool {
	switch in.mode {
	case "open":
		return true
	case "automatic", "manual":
		return in.assigned[hackentries.JudgeTeamKey(judgeID, teamID)]
	case "panel":
		track := in.tracks[teamID]
		return track != nil && in.panels[hackentries.JudgeTeamKey(judgeID, track.ID)]
	}
	return false
}

// judgesFor counts the unconflicted eligible judges of a team and whether any
// judge is conflicted with it.
func (in coverageInput) judgesFor(teamID string) (have int, conflicted bool) {
	for _, j := range in.judges {
		if in.conflicts[hackentries.JudgeTeamKey(j.ID, teamID)] {
			conflicted = true
		} else if in.eligible(j.ID, teamID) {
			have++
		}
	}
	return have, conflicted
}

func (in coverageInput) coverage(perTeam int) SetupCoverage {
	out := SetupCoverage{Target: perTeam, Min: len(in.judges), Short: []CoverageShort{}}
	if in.mode == "open" {
		out.Target = max(1, len(in.judges))
	}
	if len(in.candidates) == 0 {
		out.Min = 0
	}
	for _, t := range in.candidates {
		have, _ := in.judgesFor(t.ID)
		out.Min, out.Max = min(out.Min, have), max(out.Max, have)
		if have < out.Target {
			out.Short = append(out.Short, CoverageShort{TeamID: t.ID, Name: t.Name, Slug: t.Slug, Have: have, MissingTrack: in.mode == "panel" && in.tracks[t.ID] == nil})
		}
	}
	return out
}

// fixTarget picks the navigation target that best fixes short coverage, and
// the team slug the team-track target needs.
func (in coverageInput) fixTarget(short []CoverageShort) (target, teamSlug string) {
	if len(in.judges) == 0 {
		return "o.organiser-judges", ""
	}
	for _, t := range short {
		if _, conflicted := in.judgesFor(t.TeamID); conflicted {
			return "o.conflicts", ""
		}
	}
	switch in.mode {
	case "manual":
		return "o.assignments", ""
	case "panel":
		if in.eventTracks == 0 {
			return "o.tracks", ""
		}
		for _, t := range short {
			if t.MissingTrack {
				return "o.team-track", t.Slug
			}
		}
		return "o.panel-assignments", ""
	}
	return "o.organiser-judges", ""
}
