package entries

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
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

// checklistJudgingStages are the stages with organiser judging guidance.
var checklistJudgingStages = map[string]bool{"closed": true, "judging": true, "results": true, "archived": true}

// ChecklistOrganiserJudging shares the rubric already loaded by the checklist.
func (s *Service) ChecklistOrganiserJudging(ctx context.Context, q sqlkit.Querier, ev model.Event, rubric []model.RubricCriterion) (ChecklistJudgingFacts, error) {
	if !checklistJudgingStages[ev.Stage] {
		return ChecklistJudgingFacts{}, nil
	}
	scoresNeeded := ev.Stage != "closed"
	in, err := loadChecklistJudging(ctx, q, ev.ID, scoresNeeded)
	if err != nil {
		return ChecklistJudgingFacts{}, err
	}
	setup, err := s.JudgingSetupCoverage(ctx, ev)
	if err != nil {
		return ChecklistJudgingFacts{}, err
	}
	f := in.facts(ev, len(rubric), setup)
	if !scoresNeeded {
		return f, nil
	}
	snapshot, err := entryrepo.GetEventResults(ctx, q, ev.ID)
	if err != nil {
		return f, err
	}
	f.Published = snapshot.Published
	f.HasStandings, f.TiesDone = checklistTies(computeLoadedResults(ev, rubric, in.teams, in.raw, in.eligibility, in.tracks), snapshot)
	return f, nil
}

// checklistJudging is what organiser judging guidance is computed from.
type checklistJudging struct {
	judges      []model.NamedJudge
	teams       []model.NamedTeam
	judgeable   map[string]bool // team IDs with a judgeable entry
	raw         []model.RawScoreRow
	eligibility map[string]map[string]bool
	tracks      map[string]*model.EventTrack
}

// loadChecklistJudging reads judges, entries, teams, scores (only when needed),
// eligibility and tracks for one event.
func loadChecklistJudging(ctx context.Context, q sqlkit.Querier, eventID string, withScores bool) (checklistJudging, error) {
	var in checklistJudging
	var err error
	if in.judges, err = entryrepo.ListEventJudges(ctx, q, eventID); err != nil {
		return in, err
	}
	stored, err := entryrepo.ListEventEntries(ctx, q, eventID)
	if err != nil {
		return in, err
	}
	if in.teams, err = entryrepo.ListTeamsWithMembers(ctx, q, eventID); err != nil {
		return in, err
	}
	in.judgeable = judgeableTeams(in.teams, stored)
	if withScores {
		if in.raw, err = entryrepo.ListRawScores(ctx, q, eventID); err != nil {
			return in, err
		}
	}
	if in.eligibility, err = entryrepo.EventJudgeEligibility(ctx, q, eventID); err != nil {
		return in, err
	}
	in.tracks, err = entryrepo.EventTeamTracks(ctx, q, eventID)
	return in, err
}

// judgeableTeams returns the IDs of listed teams whose entry can be judged.
func judgeableTeams(teams []model.NamedTeam, entries []model.EventEntry) map[string]bool {
	judgeable := map[string]bool{}
	for _, e := range entries {
		if hackentries.Judgeable(e) {
			judgeable[e.TeamID] = true
		}
	}
	out := map[string]bool{}
	for _, t := range teams {
		if judgeable[t.ID] {
			out[t.ID] = true
		}
	}
	return out
}

// facts counts judges, finished judges and short teams, and words the
// assignment detail.
func (in checklistJudging) facts(ev model.Event, criteria int, setup SetupCoverage) ChecklistJudgingFacts {
	f := ChecklistJudgingFacts{Judgeable: len(in.judgeable), Coverage: setup, AssignmentsReady: len(setup.Short) == 0, AssignmentDetail: setup.Detail()}
	scored := scoredCriteria(in.raw)
	judgesByTeam := map[string]int{}
	for _, j := range in.judges {
		if j.Role == "judge" {
			f.Judges++
		} else {
			f.Organisers++
		}
		eligible := in.eligibility[j.ID]
		for id := range eligible {
			judgesByTeam[id]++
		}
		if len(eligible) > 0 {
			f.JudgesRequired++
			if judgeFinished(eligible, scored[j.ID], criteria) {
				f.JudgesDone++
			}
		}
	}
	if ev.Stage == "judging" && assignedJudgingModes[ev.JudgeAssignmentMode] && ev.JudgesPerTeam > 0 {
		for id := range in.judgeable {
			if judgesByTeam[id] < ev.JudgesPerTeam {
				f.TeamsShort++
			}
		}
	}
	if n := in.withoutTrack(); ev.JudgeAssignmentMode == "panel" && n > 0 {
		f.AssignmentDetail += fmt.Sprintf("; %d %s without a track", n, checklistNoun(n, "team"))
	}
	return f
}

// assignedJudgingModes are the modes where judges see only assigned teams.
var assignedJudgingModes = map[string]bool{"automatic": true, "manual": true, "panel": true}

// withoutTrack counts judgeable teams that have no track.
func (in checklistJudging) withoutTrack() int {
	n := 0
	for id := range in.judgeable {
		if in.tracks[id] == nil {
			n++
		}
	}
	return n
}

// scoredCriteria counts scored criteria per judge and team.
func scoredCriteria(raw []model.RawScoreRow) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, row := range raw {
		if out[row.JudgeID] == nil {
			out[row.JudgeID] = map[string]int{}
		}
		out[row.JudgeID][row.TeamID]++
	}
	return out
}

// judgeFinished reports whether a judge scored every criterion for every
// eligible team; an empty rubric is never finished.
func judgeFinished(eligible map[string]bool, scored map[string]int, criteria int) bool {
	for id := range eligible {
		if criteria == 0 || scored[id] < criteria {
			return false
		}
	}
	return true
}

// checklistTies reports whether standings exist and whether any ties are
// settled: none remain, or the published snapshot resolves the same ranking.
func checklistTies(results []TeamResult, snapshot entryrepo.EventResultsRow) (hasStandings, tiesDone bool) {
	tied := false
	for _, r := range results {
		hasStandings = hasStandings || r.Total != nil
		tied = tied || r.Tied
	}
	settled := !tied || snapshot.Published && checklistSnapshotResolved(snapshot.Snapshot) && checklistSnapshotMatches(results, snapshot.Snapshot)
	return hasStandings, hasStandings && settled
}

// checklistSnapshotMatches reports whether the published snapshot ranks the same teams on the same inputs.
func checklistSnapshotMatches(current []TeamResult, snapshot []byte) bool {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(snapshot, &rows) != nil || len(rows) != len(current) {
		return false
	}
	byID := make(map[string]map[string]json.RawMessage, len(rows))
	for _, r := range rows {
		var id string
		_ = json.Unmarshal(r["team_id"], &id)
		if id == "" || byID[id] != nil {
			return false
		}
		byID[id] = r
	}
	for _, r := range current {
		if old := byID[r.TeamID]; old == nil || !snapshotRowMatches(old, r) {
			return false
		}
	}
	return true
}

// snapshotRowMatches compares one published row's ranking inputs with r.
func snapshotRowMatches(old map[string]json.RawMessage, r TeamResult) bool {
	// "organiser" is publication order from ApplyRankOverrides, not a ranking input.
	decider := r.TieDecider
	if jsonEqual(old["tie_decider"], "organiser") {
		decider = "organiser"
	}
	projection := map[string]any{"total": r.Total, "raw_total": r.RawTotal, "normalised_total": r.NormalisedTotal, "deciding_mode": r.ScoreMode, "tie_decider": decider, "judges_scored": r.JudgesScored}
	for key, value := range projection {
		if !jsonEqual(old[key], value) {
			return false
		}
	}
	var track *model.EventTrack
	if raw, ok := old["track"]; !ok || json.Unmarshal(raw, &track) != nil {
		return false
	}
	if track == nil || r.Track == nil {
		return track == nil && r.Track == nil
	}
	return track.ID == r.Track.ID
}

// jsonEqual reports whether raw holds the JSON value encodes to; a missing raw never matches.
func jsonEqual(raw json.RawMessage, value any) bool {
	b, _ := json.Marshal(value)
	var a, c any
	return json.Unmarshal(raw, &a) == nil && json.Unmarshal(b, &c) == nil && reflect.DeepEqual(a, c)
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
