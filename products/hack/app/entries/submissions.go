package entries

import (
	"context"
	"sort"
	"strings"

	model "github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
)

// Submission is one submitted entry everyone in the event may see once
// judging starts.
type Submission struct {
	Team  model.EventTeam
	Entry model.EventEntry
	// Track is nil when the team chose none or only one team was asked for.
	Track *model.EventTrack
}

// SubmissionsOpen reports whether the event shows its submissions: from
// judging on, unless it was taken down.
func SubmissionsOpen(ev model.Event) bool {
	return !ev.TakenDown() && (ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived")
}

// Submissions lists submitted, not excluded entries by team name, optionally for one team slug.
func (s *Service) Submissions(ctx context.Context, ev model.Event, onlyTeam string) ([]Submission, error) {
	out := []Submission{}
	if !SubmissionsOpen(ev) {
		return out, nil
	}
	stored, err := entryrepo.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	teams, err := teamrepo.ListEventTeams(ctx, s.db, ev.ID)
	if err != nil {
		return nil, err
	}
	tracks := map[string]*model.EventTrack{}
	if onlyTeam == "" {
		if tracks, err = teamrepo.EventTeamTracks(ctx, s.db, ev.ID); err != nil {
			return nil, err
		}
	}
	byTeam := entriesByTeam(stored)
	codeOff := LoadRules(ev.EntryRules)["code_url"] == "off"
	for _, t := range teams {
		e, ok := byTeam[t.ID]
		if onlyTeam != "" && t.Slug != onlyTeam || !ok || e.Status() != "submitted" || e.ExcludedAt.Valid {
			continue
		}
		if codeOff {
			e.CodeURL = ""
		}
		out = append(out, Submission{Team: t, Entry: e, Track: tracks[t.ID]})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Team.Name) < strings.ToLower(out[j].Team.Name) })
	return out, nil
}
