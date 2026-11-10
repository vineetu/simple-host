package entries

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"reflect"
)

// ChecklistJudgingFacts shares batched eligibility, scores and standings for checklist guidance.
type ChecklistJudgingFacts struct {
	TeamsShort                                                int
	Coverage                                                  SetupCoverage
	Judges, Organisers, Judgeable, JudgesDone, JudgesRequired int
	AssignmentsReady                                          bool
	AssignmentDetail                                          string
	OwnTotal, OwnDone, OwnCandidates, OwnConflicts            int
	FirstUnfinished                                           string
	TiesDone, Published                                       bool
	HasStandings                                              bool
}

func (s *Service) ChecklistJudging(ctx context.Context, ev model.Event, actorID string, organiser bool) (ChecklistJudgingFacts, error) {
	var f ChecklistJudgingFacts
	if !organiser {
		return s.checklistOwnJudging(ctx, ev, actorID)
	}
	rubric, err := entryrepo.ListRubric(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	return s.ChecklistOrganiserJudging(ctx, s.db, ev, rubric)
}

// ChecklistOrganiserJudging shares the rubric already loaded by the checklist.
func (s *Service) ChecklistOrganiserJudging(ctx context.Context, q sqlkit.Querier, ev model.Event, rubric []model.RubricCriterion) (ChecklistJudgingFacts, error) {
	var f ChecklistJudgingFacts
	if ev.Stage != "closed" && ev.Stage != "judging" && ev.Stage != "results" && ev.Stage != "archived" {
		return f, nil
	}
	scoresNeeded := ev.Stage != "closed"
	completions := map[string]map[string]int{}
	judges, err := entryrepo.ListEventJudges(ctx, q, ev.ID)
	if err != nil {
		return f, err
	}
	stored, err := entryrepo.ListEventEntries(ctx, q, ev.ID)
	if err != nil {
		return f, err
	}
	teams, err := entryrepo.ListTeamsWithMembers(ctx, q, ev.ID)
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
	var rawScores []model.RawScoreRow
	if scoresNeeded {
		rawScores, err = entryrepo.ListRawScores(ctx, q, ev.ID)
		if err != nil {
			return f, err
		}
	}
	for _, row := range rawScores {
		if completions[row.JudgeID] == nil {
			completions[row.JudgeID] = map[string]int{}
		}
		completions[row.JudgeID][row.TeamID]++
	}

	eligibility, err := entryrepo.EventJudgeEligibility(ctx, q, ev.ID)
	if err != nil {
		return f, err
	}
	tracks, err := entryrepo.EventTeamTracks(ctx, q, ev.ID)
	if err != nil {
		return f, err
	}
	eligibleByTeam := map[string]int{}
	for _, j := range judges {
		if j.Role == "judge" {
			f.Judges++
		} else {
			f.Organisers++
		}
		eligible := eligibility[j.ID]
		done := true
		for id := range eligible {
			eligibleByTeam[id]++
			ok := len(rubric) > 0 && completions[j.ID][id] >= len(rubric)
			if !ok {
				done = false
			}
		}
		required := len(eligible) > 0
		if required {
			f.JudgesRequired++
			if done {
				f.JudgesDone++
			}
		}
	}
	setup, err := s.JudgingSetupCoverage(ctx, ev)
	if err != nil {
		return f, err
	}
	if ev.Stage == "judging" && (ev.JudgeAssignmentMode == "automatic" || ev.JudgeAssignmentMode == "manual" || ev.JudgeAssignmentMode == "panel") && ev.JudgesPerTeam > 0 {
		for id := range present {
			if eligibleByTeam[id] < ev.JudgesPerTeam {
				f.TeamsShort++
			}
		}
	}
	f.Coverage = setup
	f.AssignmentsReady = len(setup.Short) == 0
	f.AssignmentDetail = setup.Detail()

	if ev.JudgeAssignmentMode == "panel" {
		missingTracks := 0
		for id := range present {
			if tracks[id] == nil {
				missingTracks++
			}
		}
		if missingTracks > 0 {
			f.AssignmentDetail += fmt.Sprintf("; %d %s without a track", missingTracks, checklistNoun(missingTracks, "team"))
		}
	}
	if !scoresNeeded {
		return f, nil
	}
	results := computeLoadedResults(ev, rubric, teams, rawScores, eligibility, tracks)
	snapshot, err := entryrepo.GetEventResults(ctx, q, ev.ID)
	if err != nil {
		return f, err
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
	f.TiesDone = f.HasStandings && (!tied || snapshot.Published && checklistSnapshotResolved(snapshot.Snapshot) && checklistSnapshotMatches(results, snapshot.Snapshot))
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

func (s *Service) checklistOwnJudging(ctx context.Context, ev model.Event, actorID string) (ChecklistJudgingFacts, error) {
	var f ChecklistJudgingFacts
	queue, err := s.JudgeCandidates(ctx, ev, actorID, func() error { return nil })
	if err != nil {
		return f, err
	}
	f.OwnCandidates = len(queue)
	for _, row := range queue {
		if row.Conflicted {
			continue
		}
		f.OwnTotal++
		if row.Done {
			f.OwnDone++
		} else if f.FirstUnfinished == "" {
			f.FirstUnfinished = row.Team.ID
		}
	}
	conflicts, err := entryrepo.ListEventConflicts(ctx, s.db, ev.ID, actorID)
	if err != nil {
		return f, err
	}
	f.OwnConflicts = len(conflicts)
	snapshot, err := entryrepo.GetEventResults(ctx, s.db, ev.ID)
	if err != nil {
		return f, err
	}
	f.Published = snapshot.Published
	return f, nil
}

func checklistNoun(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func checklistSnapshotResolved(snapshot []byte) bool {
	var rows []struct {
		Tied bool `json:"tied"`
	}
	if json.Unmarshal(snapshot, &rows) != nil {
		return false
	}
	for _, row := range rows {
		if row.Tied {
			return false
		}
	}
	return true
}
