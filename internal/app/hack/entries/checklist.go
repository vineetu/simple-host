package entries

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"reflect"
)

// ChecklistJudgingFacts reuses eligibility and JudgeDoneForTeam for checklist guidance.
type ChecklistJudgingFacts struct {
	Judges, Organisers, Judgeable, JudgesDone, JudgesRequired int
	AssignmentsReady                                          bool
	AssignmentDetail                                          string
	OwnTotal, OwnDone, OwnCandidates, OwnConflicts            int
	FirstUnfinished                                           string
	TiesDone, Published                                       bool
	HasStandings                                              bool
}

func (s *Service) ChecklistJudging(ctx context.Context, ev model.Event, actorID string) (ChecklistJudgingFacts, error) {
	var f ChecklistJudgingFacts
	judges, err := entryrepo.ListEventJudges(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	rubric, err := entryrepo.ListRubric(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	stored, err := entryrepo.ListEventEntries(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	judged := map[string]bool{}
	for _, e := range stored {
		if hackentries.Judgeable(e) {
			judged[e.TeamID] = true
		}
	}
	present := map[string]bool{}
	for _, t := range teams {
		if judged[t.ID] {
			present[t.ID] = true
		}
	}
	f.Judgeable = len(present)
	rawScores, err := entryrepo.ListRawScores(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	scored := map[string]bool{}
	for _, row := range rawScores {
		scored[row.JudgeID] = true
	}

	assignments, err := entryrepo.ListEventAssignments(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	assigned := map[string]bool{}
	for _, a := range assignments {
		if ev.JudgeAssignmentMode == "automatic" || ev.JudgeAssignmentMode == "manual" {
			assigned[a.JudgeID] = true
		}
	}
	panels, err := entryrepo.ListTrackPanels(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	for _, p := range panels {
		if ev.JudgeAssignmentMode == "panel" {
			assigned[p.JudgeID] = true
		}
	}
	coverage := map[string]int{}
	for _, j := range judges {
		if j.Role == "judge" {
			f.Judges++
		} else {
			f.Organisers++
		}
		eligible, err := entryrepo.EligibleJudgeTeams(ctx, s.db, ev.ID, j.ID)
		if err != nil {
			return f, err
		}
		done := true
		for id := range eligible {
			coverage[id]++
			ok, err := entryrepo.JudgeDoneForTeam(ctx, s.db, ev.ID, j.ID, id, len(rubric))
			if err != nil {
				return f, err
			}
			if !ok {
				done = false
			}
		}
		required := j.Role == "judge" && len(eligible) > 0 || j.Role == "organiser" && (scored[j.ID] || ev.JudgeAssignmentMode != "open" && assigned[j.ID])
		if required {
			f.JudgesRequired++
			if done {
				f.JudgesDone++
			}
		}
	}
	f.AssignmentsReady = f.Judgeable > 0
	under := 0
	for id := range present {
		n := coverage[id]
		if n == 0 {
			f.AssignmentsReady = false
		}
		target := ev.JudgesPerTeam
		if (ev.JudgeAssignmentMode == "manual" || ev.JudgeAssignmentMode == "panel") && n > 0 && n < target {
			target = n
		}
		if n < target {
			under++
		}
	}
	f.AssignmentDetail = fmt.Sprintf("%d judgeable teams; %d need more eligible judges after conflicts", f.Judgeable, under)

	if actorID != "" {
		queue, err := s.JudgeQueue(ctx, ev, actorID, func() error { return nil })
		if err != nil {
			return f, err
		}
		f.OwnTotal = len(queue)
		for _, q := range queue {
			if q.Done {
				f.OwnDone++
			} else if f.FirstUnfinished == "" {
				f.FirstUnfinished = q.Team.ID
			}
		}
		candidates, err := entryrepo.EligibleJudgeTeamsIgnoringConflicts(ctx, s.db, ev.ID, actorID)
		if err != nil {
			return f, err
		}
		for id := range candidates {
			if present[id] {
				f.OwnCandidates++
			}
		}
		conflicts, err := entryrepo.ListEventConflicts(ctx, s.db, ev.ID, actorID)
		if err != nil {
			return f, err
		}
		f.OwnConflicts = len(conflicts)
	}
	results, err := s.ComputeResults(ctx, s.db, ev)
	if err != nil {
		return f, err
	}
	snapshot, err := entryrepo.GetEventResults(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	if ev.JudgeAssignmentMode == "panel" {
		missingTracks := 0
		for _, r := range results {
			if present[r.TeamID] && r.Track == nil {
				missingTracks++
			}
		}
		if missingTracks > 0 {
			f.AssignmentDetail += fmt.Sprintf("; %d teams need a track", missingTracks)
		}
	}
	f.Published = snapshot.Published
	tied := false
	for _, r := range results {
		if r.Total != nil {
			f.HasStandings = true
		}
		if r.Tied {
			tied = true
		}
	}
	f.TiesDone = f.HasStandings && (!tied || snapshot.Published && checklistSnapshotMatches(results, snapshot.Snapshot))
	return f, nil
}

// Compare the ranking inputs, ignoring the organiser's chosen publication order.
func checklistSnapshotMatches(current []TeamResult, snapshot []byte) bool {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(snapshot, &rows) != nil || len(rows) != len(current) {
		return false
	}
	byID := map[string]map[string]json.RawMessage{}
	for _, r := range rows {
		var id string
		_ = json.Unmarshal(r["team_id"], &id)
		if id == "" || byID[id] != nil {
			return false
		}
		byID[id] = r
	}
	for _, r := range current {
		old := byID[r.TeamID]
		if old == nil {
			return false
		}
		// ApplyRankOverrides replaces tie_decider with "organiser"; that value is
		// publication order, so compare the underlying decider in that one case.
		decider := r.TieDecider
		var savedDecider string
		_ = json.Unmarshal(old["tie_decider"], &savedDecider)
		if savedDecider == "organiser" {
			decider = "organiser"
		}
		projection := map[string]any{"total": r.Total, "raw_total": r.RawTotal, "normalised_total": r.NormalisedTotal, "deciding_mode": r.ScoreMode, "tie_decider": decider, "judges_scored": r.JudgesScored}
		for key, value := range projection {
			raw, ok := old[key]
			if !ok {
				return false
			}
			b, _ := json.Marshal(value)
			var a, c any
			if json.Unmarshal(raw, &a) != nil || json.Unmarshal(b, &c) != nil || !reflect.DeepEqual(a, c) {
				return false
			}
		}
		raw, ok := old["track"]
		if !ok {
			return false
		}
		var track *model.EventTrack
		if json.Unmarshal(raw, &track) != nil {
			return false
		}
		if (track == nil) != (r.Track == nil) || track != nil && track.ID != r.Track.ID {
			return false
		}
	}
	return true
}

// ChecklistRubricValid uses the same validator as rubric writes.
func ChecklistRubricValid(criteria []model.RubricCriterion) bool {
	type input struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Weight      *float64 `json:"weight"`
		MaxPoints   *float64 `json:"max_points"`
	}
	in := []struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Weight      *float64 `json:"weight"`
		MaxPoints   *float64 `json:"max_points"`
	}{}
	for _, c := range criteria {
		w, p := float64(c.Weight), float64(c.MaxPoints)
		in = append(in, input{c.ID, c.Name, c.Description, &w, &p})
	}
	_, msg := hackentries.ValidateRubric(in)
	return msg == ""
}

// ChecklistParticipant obtains the existing progress without changing its contract.
func (s *Service) ChecklistParticipant(ctx context.Context, ev model.Event, member model.EventMember) (Progress, model.TeamWriteState, error) {
	team, err := teamrepo.GetEventTeamByID(ctx, s.db, member.TeamID.String)
	if err != nil {
		return Progress{}, model.TeamWriteState{}, err
	}
	st, err := teamrepo.TeamWriteStateFor(ctx, s.db, team.ID, false)
	if err != nil {
		return Progress{}, st, err
	}
	p, err := s.EntryProgress(ctx, ev, team, nil, st)
	return p, st, err
}

// Keep membership approval in the existing repository's vocabulary.
func (s *Service) ChecklistParticipantApproved(ctx context.Context, ev model.Event, userID string) (bool, error) {
	m, err := eventrepo.MemberApprovalStatus(ctx, s.db, ev.ID, userID)
	return m == "approved", err
}
