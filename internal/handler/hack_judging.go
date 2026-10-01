package handler

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vsriram/simple-host/internal/db"
)

// Judging setup: the rubric, how judges are spread, conflicts, and the
// organiser's dashboard. Scoring, the lock itself, publishing and results
// are the next pass. The lock column is already checked here so a locked
// event cannot have its rubric rewritten out from under stored scores.

func (h *HackHandler) registerJudging(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/rubric", wrap(h.getRubric))
	mux.Handle("PUT /v1/hack/events/{slug}/rubric", wrap(h.putRubric))
	mux.Handle("GET /v1/hack/events/{slug}/judging/settings", wrap(h.getJudgingSettings))
	mux.Handle("PATCH /v1/hack/events/{slug}/judging/settings", wrap(h.patchJudgingSettings))
	mux.Handle("POST /v1/hack/events/{slug}/assignments/generate", wrap(h.generateAssignments))
	mux.Handle("GET /v1/hack/events/{slug}/conflicts", wrap(h.listConflicts))
	mux.Handle("POST /v1/hack/events/{slug}/conflicts", wrap(h.declareConflict))
	mux.Handle("DELETE /v1/hack/events/{slug}/conflicts/{team_id}", wrap(h.deleteConflict))
	mux.Handle("GET /v1/hack/events/{slug}/judging/dashboard", wrap(h.judgingDashboard))
	mux.Handle("POST /v1/hack/events/{slug}/judging/lock", wrap(h.lockJudging))
	mux.Handle("POST /v1/hack/events/{slug}/judging/unlock", wrap(h.unlockJudging))
	mux.Handle("GET /v1/hack/events/{slug}/judge/queue", wrap(h.judgeQueue))
	mux.Handle("GET /v1/hack/events/{slug}/judge/scores/{team}", wrap(h.getJudgeScores))
	mux.Handle("PUT /v1/hack/events/{slug}/judge/scores/{team}", wrap(h.putJudgeScores))
	mux.Handle("POST /v1/hack/events/{slug}/results/publish", wrap(h.publishResults))
	mux.Handle("PATCH /v1/hack/events/{slug}/results", wrap(h.patchResults))
	mux.Handle("GET /v1/hack/events/{slug}/results", http.HandlerFunc(h.getPublicResults))
	mux.Handle("GET /v1/hack/events/{slug}/my-results", wrap(h.getMyResults))
	mux.Handle("GET /v1/hack/events/{slug}/export/scores.csv", wrap(h.exportScoresCSV))
	mux.Handle("GET /v1/hack/events/{slug}/export/results.csv", wrap(h.exportResultsCSV))
}

// writeIfJudgingLocked refuses a write that would change what a published or
// about-to-be-published total is computed from. Locking is meant to freeze
// every input to scoring, not just score rows themselves: a conflict
// declared or removed, an assignment regenerated, or the assignment mode
// changed after locking would otherwise silently change the result the next
// time it is (re)computed, with no further write to "scores" ever
// happening — defeating the point of locking in the first place. Returns
// true (having already written the response) when locked.
func writeIfJudgingLocked(w http.ResponseWriter, ev db.Event) bool {
	if ev.JudgingLockedAt.Valid {
		writeHackErr(w, http.StatusConflict, "scores_locked", "Judging is locked; unlock it first.")
		return true
	}
	return false
}

func rubricJSON(items []db.RubricCriterion) map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, c := range items {
		out = append(out, map[string]any{
			"id":          c.ID,
			"position":    c.Position,
			"name":        c.Name,
			"description": c.Description,
			"weight":      c.Weight,
			"max_points":  c.MaxPoints,
		})
	}
	return map[string]any{"criteria": out}
}

func (h *HackHandler) getRubric(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	items, err := db.ListRubric(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: rubric %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, rubricJSON(items))
}

func (h *HackHandler) putRubric(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	// Locked wins over a body that would also fail validation: the organiser's
	// next step is to unlock, not to repair the rubric.
	if a.event.JudgingLockedAt.Valid {
		writeHackErr(w, http.StatusConflict, "scores_locked", "Judging is locked; unlock it first to change the rubric.")
		return
	}
	var req struct {
		Criteria []struct {
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Weight      *float64 `json:"weight"`
			MaxPoints   *float64 `json:"max_points"`
		} `json:"criteria"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	items, msg := validateRubric(req.Criteria)
	if msg != "" {
		writeHackErr(w, http.StatusBadRequest, "invalid_rubric", msg)
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: rubric %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if ev.JudgingLockedAt.Valid {
		writeHackErr(w, http.StatusConflict, "scores_locked", "Judging is locked; unlock it first to change the rubric.")
		return
	}
	if err := db.ReplaceRubric(r.Context(), tx, a.event.ID, items); err != nil {
		log.Printf("hack: rubric %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	saved, err := db.ListRubric(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: rubric %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: rubric %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, rubricJSON(saved))
}

// validateRubric applies the same line and text rules as checkHackLine and
// checkHackText. Every refusal is one invalid_rubric message.
func validateRubric(in []struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Weight      *float64 `json:"weight"`
	MaxPoints   *float64 `json:"max_points"`
}) ([]db.RubricCriterion, string) {
	if len(in) < 1 || len(in) > 10 {
		return nil, "Give between 1 and 10 criteria."
	}
	out := make([]db.RubricCriterion, 0, len(in))
	sum := 0
	for _, c := range in {
		name, ok := rubricName(c.Name)
		if !ok {
			return nil, "Give each criterion a name of up to 80 characters."
		}
		desc, msg := rubricDescription(c.Description)
		if msg != "" {
			return nil, msg
		}
		weight, ok := wholeInRange(c.Weight, 0, 100)
		if !ok {
			return nil, "Each weight must be a whole number from 0 to 100."
		}
		points, ok := wholeInRange(c.MaxPoints, 1, 10)
		if !ok {
			return nil, "Each criterion's points must be a whole number from 1 to 10."
		}
		sum += weight
		out = append(out, db.RubricCriterion{
			Name: name, Description: desc, Weight: weight, MaxPoints: points,
		})
	}
	if sum != 100 {
		return nil, "Weights must add up to 100."
	}
	return out, ""
}

// rubricName is checkHackLine for a required criterion name (1–80 runes,
// one line, collapsed spaces, something visible) without writing a response.
func rubricName(s string) (string, bool) {
	if strings.ContainsAny(strings.TrimSpace(s), "\n\r\t\u0085\u2028\u2029") {
		return "", false
	}
	s = strings.Join(strings.Fields(s), " ")
	if !hasVisible(s) {
		return "", false
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", false
	}
	n := utf8.RuneCountInString(s)
	if n < 1 || n > 80 {
		return "", false
	}
	return s, true
}

// rubricDescription is checkHackText for an optional description (0–300
// runes; newlines and tabs stay, other controls do not).
func rubricDescription(s string) (string, string) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", "A description contains characters that are not allowed."
	}
	if utf8.RuneCountInString(s) > 300 {
		return "", "Keep each description to 300 characters."
	}
	return s, ""
}

// judgeCommentText cleans and validates a judge's comment on a team: same
// control-character rule as a rubric description (a comment can be empty,
// unlike a description's cousin fields, and also lands in a CSV export, so
// stripping stray formatting here matters for csvSafe's leading-character
// check to actually see the real first character rather than whatever an
// embedded control sequence put there).
func judgeCommentText(s string) (string, string) {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
	if hasBadControls(s) {
		return "", "A comment contains characters that are not allowed."
	}
	if utf8.RuneCountInString(s) > 1000 {
		return "", "Keep a comment to 1000 characters."
	}
	return s, ""
}

// wholeInRange reports n when it is a finite whole number in [min, max].
func wholeInRange(n *float64, min, max int) (int, bool) {
	if n == nil || math.IsNaN(*n) || math.IsInf(*n, 0) || *n != math.Trunc(*n) {
		return 0, false
	}
	if *n < float64(min) || *n > float64(max) {
		return 0, false
	}
	return int(*n), true
}

func judgingSettingsJSON(ev db.Event) map[string]any {
	return map[string]any{
		"assignment_mode": ev.JudgeAssignmentMode,
		"judges_per_team": ev.JudgesPerTeam,
	}
}

func (h *HackHandler) getJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, judgingSettingsJSON(a.event))
}

func (h *HackHandler) patchJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if writeIfJudgingLocked(w, a.event) {
		return
	}
	var req struct {
		AssignmentMode *string  `json:"assignment_mode"`
		JudgesPerTeam  *float64 `json:"judges_per_team"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	var mode *string
	if req.AssignmentMode != nil {
		if *req.AssignmentMode != "open" && *req.AssignmentMode != "automatic" {
			writeHackErr(w, http.StatusBadRequest, "invalid_request", `assignment_mode must be "open" or "automatic".`)
			return
		}
		mode = req.AssignmentMode
	}
	var perTeam *int
	if req.JudgesPerTeam != nil {
		n, ok := wholeInRange(req.JudgesPerTeam, 1, 20)
		if !ok {
			writeHackErr(w, http.StatusBadRequest, "invalid_request", "judges_per_team must be a whole number from 1 to 20.")
			return
		}
		perTeam = &n
	}
	ev := a.event
	if mode != nil || perTeam != nil {
		var err error
		ev, err = db.UpdateJudgingSettings(r.Context(), h.database, a.event.ID, mode, perTeam)
		if err != nil {
			log.Printf("hack: judging settings %s: %v", a.event.Slug, err)
			writeInternal(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, judgingSettingsJSON(ev))
}

func (h *HackHandler) generateAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if writeIfJudgingLocked(w, ev) {
		return
	}
	// 409, not 400: the route exists, the event is just not in that mode.
	// The code stays invalid_request, the same name as the other bad-input refusals.
	if ev.JudgeAssignmentMode != "automatic" {
		writeHackErr(w, http.StatusConflict, "invalid_request", "Switch to automatic assignment first.")
		return
	}
	judges, err := db.ListEventJudges(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	teams, err := db.ListTeamsWithMembers(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if len(judges) == 0 {
		writeHackErr(w, http.StatusBadRequest, "too_few_judges", "Invite at least one judge first.")
		return
	}
	if len(teams) == 0 {
		writeHackErr(w, http.StatusBadRequest, "too_few_teams", "No teams to assign yet.")
		return
	}
	pairs, err := db.ListConflictPairs(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	conflicted := map[string]bool{}
	for _, p := range pairs {
		conflicted[judgeTeamKey(p.JudgeID, p.TeamID)] = true
	}
	planned, under := planJudgeAssignments(teams, judges, conflicted, ev.JudgesPerTeam)
	if err := db.ReplaceAssignments(r.Context(), tx, a.event.ID, planned); err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, assignmentJSON(planned, under))
}

func judgeTeamKey(judgeID, teamID string) string {
	return judgeID + "\x00" + teamID
}

// planJudgeAssignments spreads judges across teams. Each team gets up to
// perTeam judges, fewer only when fewer judges are free of a conflict with
// that team — that shortage is reported, not refused. Eligible judges are
// taken least-loaded first, and id breaks ties, which is the fair share: a
// judge who already has as many teams as another eligible judge is skipped
// until the loads even out. The same input always yields the same plan.
// Teams and judges are sorted by id here, so the caller need not.
func planJudgeAssignments(teams []db.NamedTeam, judges []db.NamedJudge, conflicted map[string]bool, perTeam int) ([]db.JudgeAssignment, []string) {
	teams = append([]db.NamedTeam(nil), teams...)
	judges = append([]db.NamedJudge(nil), judges...)
	sort.Slice(teams, func(i, j int) bool { return teams[i].ID < teams[j].ID })
	sort.Slice(judges, func(i, j int) bool { return judges[i].ID < judges[j].ID })
	if perTeam < 0 {
		perTeam = 0
	}
	load := make([]int, len(judges))
	assignments := make([]db.JudgeAssignment, 0)
	under := make([]string, 0)
	for _, team := range teams {
		eligible := make([]int, 0, len(judges))
		for i, judge := range judges {
			if conflicted[judgeTeamKey(judge.ID, team.ID)] {
				continue
			}
			eligible = append(eligible, i)
		}
		sort.Slice(eligible, func(a, b int) bool {
			ia, ib := eligible[a], eligible[b]
			if load[ia] != load[ib] {
				return load[ia] < load[ib]
			}
			return judges[ia].ID < judges[ib].ID
		})
		n := perTeam
		if n > len(eligible) {
			n = len(eligible)
		}
		if n < perTeam {
			under = append(under, team.ID)
		}
		for k := 0; k < n; k++ {
			i := eligible[k]
			load[i]++
			assignments = append(assignments, db.JudgeAssignment{
				JudgeID:   judges[i].ID,
				JudgeName: judges[i].Name,
				TeamID:    team.ID,
				TeamName:  team.Name,
			})
		}
	}
	sort.Slice(assignments, func(i, j int) bool {
		if assignments[i].TeamID != assignments[j].TeamID {
			return assignments[i].TeamID < assignments[j].TeamID
		}
		return assignments[i].JudgeID < assignments[j].JudgeID
	})
	return assignments, under
}

func assignmentJSON(rows []db.JudgeAssignment, under []string) map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"judge_id":   row.JudgeID,
			"judge_name": row.JudgeName,
			"team_id":    row.TeamID,
			"team_name":  row.TeamName,
		})
	}
	if under == nil {
		under = []string{}
	}
	return map[string]any{"assignments": out, "teams_under_target": under}
}

func conflictJSON(c db.EventConflict) map[string]any {
	return map[string]any{
		"judge_id":    c.JudgeID,
		"judge_name":  c.JudgeName,
		"team_id":     c.TeamID,
		"team_name":   c.TeamName,
		"declared_by": c.DeclaredBy,
	}
}

func (h *HackHandler) listConflicts(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	judgeID := ""
	if a.member.Role == "judge" {
		judgeID = a.user.ID
	}
	rows, err := db.ListEventConflicts(r.Context(), h.database, a.event.ID, judgeID)
	if err != nil {
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, conflictJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": out})
}

func (h *HackHandler) declareConflict(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser", "judge")
	if !ok {
		return
	}
	if writeIfJudgingLocked(w, a.event) {
		return
	}
	var req struct {
		TeamID      string `json:"team_id"`
		JudgeUserID string `json:"judge_user_id"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	judgeID := a.user.ID
	declaredBy := "judge"
	if a.member.Role != "organiser" {
		if req.JudgeUserID != "" && !strings.EqualFold(req.JudgeUserID, a.user.ID) {
			writeHackErr(w, http.StatusForbidden, "forbidden", "Only an organiser can record a conflict for another judge.")
			return
		}
	} else {
		if strings.TrimSpace(req.JudgeUserID) == "" {
			writeHackErr(w, http.StatusBadRequest, "invalid_request", "Say which judge this conflict is for.")
			return
		}
		judgeID = req.JudgeUserID
		declaredBy = "organiser"
	}
	if !uuidShape.MatchString(req.TeamID) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if !uuidShape.MatchString(judgeID) {
		writeHackErr(w, http.StatusNotFound, "judge_not_found", "judge not found")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	if _, err := db.EventTeamInEvent(r.Context(), tx, a.event.ID, req.TeamID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
			return
		}
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	judge, err := db.EventJudge(r.Context(), tx, a.event.ID, judgeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeHackErr(w, http.StatusNotFound, "judge_not_found", "judge not found")
			return
		}
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	// Store the member's canonical id, not whatever case the body used.
	judgeID = judge.ID
	if err := db.DeclareConflict(r.Context(), tx, a.event.ID, judgeID, req.TeamID, declaredBy); err != nil {
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	saved, err := db.GetEventConflict(r.Context(), tx, a.event.ID, judgeID, req.TeamID)
	if err != nil {
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, conflictJSON(saved))
}

func (h *HackHandler) deleteConflict(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser", "judge")
	if !ok {
		return
	}
	if writeIfJudgingLocked(w, a.event) {
		return
	}
	teamID := r.PathValue("team_id")
	judgeID := a.user.ID
	q := strings.TrimSpace(r.URL.Query().Get("judge_user_id"))
	if a.member.Role != "organiser" {
		if q != "" && !strings.EqualFold(q, a.user.ID) {
			writeHackErr(w, http.StatusBadRequest, "invalid_request", "You can only remove your own conflict.")
			return
		}
	} else if q == "" {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "Say which judge's conflict to remove.")
		return
	} else {
		judgeID = q
	}
	if !uuidShape.MatchString(teamID) || !uuidShape.MatchString(judgeID) {
		writeHackErr(w, http.StatusNotFound, "conflict_not_found", "conflict not found")
		return
	}
	gone, err := db.DeleteConflict(r.Context(), h.database, a.event.ID, judgeID, teamID)
	if err != nil {
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if !gone {
		writeHackErr(w, http.StatusNotFound, "conflict_not_found", "conflict not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *HackHandler) judgingDashboard(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	ctx := r.Context()
	teams, err := db.ListTeamsWithMembers(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	judges, err := db.ListEventJudges(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	scoredTeams, scoredJudges, err := db.ScoreCoverage(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	// flagged when judges_scored is short of the target. Automatic mode's
	// target is judges_per_team. Open mode does not assign a cap, so the
	// target is 2 (the column's default) rather than whatever judges_per_team
	// is set to: an event still on open shows teams that nobody has scored.
	target := a.event.JudgesPerTeam
	if a.event.JudgeAssignmentMode == "open" {
		target = 2
	}
	assigned := map[string]int{}
	if a.event.JudgeAssignmentMode == "automatic" {
		assigned, err = db.AssignmentCounts(ctx, h.database, a.event.ID)
		if err != nil {
			log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
			writeInternal(w)
			return
		}
	} else {
		pairs, err := db.ListConflictPairs(ctx, h.database, a.event.ID)
		if err != nil {
			log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
			writeInternal(w)
			return
		}
		blocked := map[string]bool{}
		for _, p := range pairs {
			blocked[judgeTeamKey(p.JudgeID, p.TeamID)] = true
		}
		for _, judge := range judges {
			n := 0
			for _, team := range teams {
				if !blocked[judgeTeamKey(judge.ID, team.ID)] {
					n++
				}
			}
			assigned[judge.ID] = n
		}
	}
	teamRows := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		scored := scoredTeams[team.ID]
		teamRows = append(teamRows, map[string]any{
			"team_id":       team.ID,
			"team_name":     team.Name,
			"judges_scored": scored,
			"flagged":       scored < target,
		})
	}
	judgeRows := make([]map[string]any, 0, len(judges))
	for _, judge := range judges {
		judgeRows = append(judgeRows, map[string]any{
			"judge_id":       judge.ID,
			"judge_name":     judge.Name,
			"done_count":     scoredJudges[judge.ID],
			"assigned_count": assigned[judge.ID],
		})
	}
	// Tied totals need scores. The next pass fills this once totals exist.
	writeJSON(w, http.StatusOK, map[string]any{
		"teams":       teamRows,
		"judges":      judgeRows,
		"tied_groups": []any{},
	})
}

// --- M3 pass 2: scoring, lock/unlock, publish, results, exports ----------

func judgingLockJSON(ev db.Event, locked bool) map[string]any {
	out := map[string]any{"locked": locked}
	if locked && ev.JudgingLockedAt.Valid {
		out["locked_at"] = rfc3339Time(ev.JudgingLockedAt.Time)
	} else {
		out["locked_at"] = nil
	}
	return out
}

func (h *HackHandler) lockJudging(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	ev, err := db.SetJudgingLock(r.Context(), h.database, a.event.ID, true, "")
	if err != nil {
		log.Printf("hack: judging lock %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, judgingLockJSON(ev, true))
}

func (h *HackHandler) unlockJudging(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	reason, ok := checkHackLine(w, req.Reason, "reason", 1, 300)
	if !ok {
		return
	}
	ev, err := db.SetJudgingLock(r.Context(), h.database, a.event.ID, false, reason)
	if err != nil {
		log.Printf("hack: judging unlock %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	log.Printf("hack: judging unlock %s by=%s reason=%q", a.event.Slug, a.user.ID, reason)
	writeJSON(w, http.StatusOK, judgingLockJSON(ev, false))
}

func (h *HackHandler) judgeQueue(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "judge")
	if !ok {
		return
	}
	ctx := r.Context()
	teams, err := db.ListTeamsWithMembers(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	pairs, err := db.ListConflictPairs(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	conflicted := map[string]bool{}
	for _, p := range pairs {
		if p.JudgeID == a.user.ID {
			conflicted[p.TeamID] = true
		}
	}
	eligible := teams
	// Compute global coverage (how many distinct judges scored each team) once,
	// before filtering to this judge's own eligible set, so ordering reflects
	// true coverage, not just what this judge can see.
	byTeam, _, err := db.ScoreCoverage(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if a.event.JudgeAssignmentMode == "automatic" {
		assignedTeamIDs, aerr := assignedTeamsFor(ctx, h.database, a.event.ID, a.user.ID)
		if aerr != nil {
			log.Printf("hack: queue %s: %v", a.event.Slug, aerr)
			writeInternal(w)
			return
		}
		filtered := make([]db.NamedTeam, 0, len(assignedTeamIDs))
		for _, t := range teams {
			if assignedTeamIDs[t.ID] {
				filtered = append(filtered, t)
			}
		}
		eligible = filtered
	}
	rubric, err := db.ListRubric(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	tl, err := h.loadTeamList(ctx, a.event)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	stored, err := db.ListEventEntries(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	byTeamEntry := map[string]db.EventEntry{}
	for _, e := range stored {
		byTeamEntry[e.TeamID] = e
	}
	required := entryRequiredList(a.event.EntryRequired)
	type row struct {
		item  map[string]any
		count int
		name  string
	}
	rows := make([]row, 0, len(eligible))
	for _, team := range eligible {
		if conflicted[team.ID] {
			continue
		}
		item, ierr := h.entryListItem(ctx, a.event, db.EventTeam{ID: team.ID, Name: team.Name, Slug: team.Name}, byTeamEntry[team.ID], required, tl)
		if ierr != nil {
			log.Printf("hack: queue %s/%s: %v", a.event.Slug, team.ID, ierr)
			writeInternal(w)
			return
		}
		item["team_id"] = team.ID
		done, derr := judgeDoneForTeam(ctx, h.database, a.event.ID, a.user.ID, team.ID, len(rubric))
		if derr != nil {
			log.Printf("hack: queue %s/%s: %v", a.event.Slug, team.ID, derr)
			writeInternal(w)
			return
		}
		item["done"] = done
		count := byTeam[team.ID]
		item["scores_count"] = count
		rows = append(rows, row{item: item, count: count, name: team.Name})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].count != rows[j].count {
			return rows[i].count < rows[j].count
		}
		return strings.ToLower(rows[i].name) < strings.ToLower(rows[j].name)
	})
	out := make([]map[string]any, 0, len(rows))
	for _, rw := range rows {
		out = append(out, rw.item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"queue": out})
}

// assignedTeamsFor is the set of team ids a judge is assigned in automatic
// mode.
func assignedTeamsFor(ctx context.Context, database *sql.DB, eventID, judgeID string) (map[string]bool, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT team_id FROM event_assignments WHERE event_id = $1 AND judge_id = $2`, eventID, judgeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// judgeDoneForTeam: true when this judge has a score row for every current
// rubric criterion of this team. Zero criteria means nothing can be "done"
// yet (there is nothing to score), so it is false, not vacuously true.
func judgeDoneForTeam(ctx context.Context, database *sql.DB, eventID, judgeID, teamID string, criteriaCount int) (bool, error) {
	if criteriaCount == 0 {
		return false, nil
	}
	var n int
	err := database.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT criterion_id) FROM event_scores
		 WHERE event_id = $1 AND judge_id = $2 AND team_id = $3`, eventID, judgeID, teamID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n >= criteriaCount, nil
}

func scoresJSON(scores []db.JudgeScore, comment string, complete *bool) map[string]any {
	out := make([]map[string]any, 0, len(scores))
	for _, s := range scores {
		out = append(out, map[string]any{"criterion_id": s.CriterionID, "points": s.Points})
	}
	resp := map[string]any{"scores": out, "comment": comment}
	if complete != nil {
		resp["complete"] = *complete
	}
	return resp
}

func (h *HackHandler) getJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "judge")
	if !ok {
		return
	}
	teamID := r.PathValue("team")
	if ok2, err := h.judgeMayScore(r.Context(), a, teamID); err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	} else if !ok2 {
		writeHackErr(w, http.StatusForbidden, "forbidden", "You have a conflict with this team.")
		return
	}
	scores, comment, err := db.GetJudgeTeamScores(r.Context(), h.database, a.event.ID, a.user.ID, teamID)
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, scoresJSON(scores, comment, nil))
}

// judgeMayScore reports whether teamID is real in this event and this judge
// has no conflict with it. The second return is an internal error only.
func (h *HackHandler) judgeMayScore(ctx context.Context, a hackAccess, teamID string) (bool, error) {
	if _, err := db.EventTeamInEvent(ctx, h.database, a.event.ID, teamID); err != nil {
		return false, err
	}
	pairs, err := db.ListConflictPairs(ctx, h.database, a.event.ID)
	if err != nil {
		return false, err
	}
	for _, p := range pairs {
		if p.JudgeID == a.user.ID && p.TeamID == teamID {
			return false, nil
		}
	}
	return true, nil
}

func (h *HackHandler) putJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "judge")
	if !ok {
		return
	}
	if a.event.JudgingLockedAt.Valid {
		writeHackErr(w, http.StatusConflict, "scores_locked", "Judging is locked.")
		return
	}
	teamID := r.PathValue("team")
	team, err := db.EventTeamInEvent(r.Context(), h.database, a.event.ID, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	_ = team
	pairs, err := db.ListConflictPairs(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	for _, p := range pairs {
		if p.JudgeID == a.user.ID && p.TeamID == teamID {
			writeHackErr(w, http.StatusForbidden, "forbidden", "You have a conflict with this team.")
			return
		}
	}
	var req struct {
		Scores []struct {
			CriterionID string   `json:"criterion_id"`
			Points      *float64 `json:"points"`
		} `json:"scores"`
		Comment *string `json:"comment"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Comment != nil {
		cleaned, msg := judgeCommentText(*req.Comment)
		if msg != "" {
			writeHackErr(w, http.StatusBadRequest, "invalid_comment", msg)
			return
		}
		req.Comment = &cleaned
	}
	crit, err := db.CriterionIDsForEvent(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	scores := make([]db.JudgeScore, 0, len(req.Scores))
	for _, s := range req.Scores {
		maxPts, ok := crit[s.CriterionID]
		if !ok {
			writeHackErr(w, http.StatusNotFound, "criterion_not_found", "That criterion is not in this event's current rubric.")
			return
		}
		pts, ok := wholeInRange(s.Points, 0, maxPts)
		if !ok {
			writeHackErr(w, http.StatusBadRequest, "invalid_points", fmt.Sprintf("Points for %q must be a whole number from 0 to %d.", s.CriterionID, maxPts))
			return
		}
		scores = append(scores, db.JudgeScore{CriterionID: s.CriterionID, Points: pts})
	}
	if req.Comment != nil && len(scores) == 0 {
		existing, _, eerr := db.GetJudgeTeamScores(r.Context(), h.database, a.event.ID, a.user.ID, teamID)
		if eerr != nil {
			log.Printf("hack: scores %s: %v", a.event.Slug, eerr)
			writeInternal(w)
			return
		}
		if len(existing) == 0 {
			writeHackErr(w, http.StatusBadRequest, "invalid_request", "Score at least one criterion before adding a comment.")
			return
		}
	}
	// Re-check the lock inside the same transaction as the write, with the
	// event row locked: a lock request racing this one can no longer let a
	// score land after the lock is taken (the earlier check above is just an
	// optimistic fast path to avoid the extra round trip in the common case).
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if writeIfJudgingLocked(w, ev) {
		return
	}
	updated, comment, err := db.UpsertJudgeScores(r.Context(), tx, a.event.ID, a.user.ID, teamID, scores, req.Comment)
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	rubric, err := db.ListRubric(r.Context(), tx, a.event.ID)
	if err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("hack: scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	complete := len(rubric) > 0 && len(updated) >= len(rubric)
	writeJSON(w, http.StatusOK, scoresJSON(updated, comment, &complete))
}

// teamResult is one team's computed standing.
type teamResult struct {
	TeamID       string
	TeamName     string
	Total        *float64
	Rank         *int
	Tied         bool
	JudgesScored int
}

// computeResults builds the current ranking from raw scores, excluding any
// judge who has a conflict with that team even if score rows exist. A team
// with zero qualifying judges gets Total=nil and sorts last, never "tied"
// with another nil team. A judge's partial (incomplete) scoring still counts
// with whatever sum they have: a partial score is better signal than none,
// and "complete" is a UI nicety, not a scoring gate.
func computeResults(ctx context.Context, database *sql.DB, eventID string) ([]teamResult, error) {
	rubric, err := db.ListRubric(ctx, database, eventID)
	if err != nil {
		return nil, err
	}
	weight := map[string]float64{}
	maxPts := map[string]float64{}
	for _, c := range rubric {
		weight[c.ID] = float64(c.Weight)
		maxPts[c.ID] = float64(c.MaxPoints)
	}
	teams, err := db.ListTeamsWithMembers(ctx, database, eventID)
	if err != nil {
		return nil, err
	}
	raw, err := db.ListRawScores(ctx, database, eventID)
	if err != nil {
		return nil, err
	}
	pairs, err := db.ListConflictPairs(ctx, database, eventID)
	if err != nil {
		return nil, err
	}
	conflicted := map[string]bool{}
	for _, p := range pairs {
		conflicted[p.JudgeID+"\x00"+p.TeamID] = true
	}
	// per (team, judge) weighted sum
	type key struct{ team, judge string }
	sums := map[key]float64{}
	for _, r := range raw {
		if conflicted[r.JudgeID+"\x00"+r.TeamID] {
			continue
		}
		w := weight[r.CriterionID]
		m := maxPts[r.CriterionID]
		if m <= 0 {
			continue
		}
		sums[key{r.TeamID, r.JudgeID}] += (w / 100) * (float64(r.Points) / m) * 100
	}
	byTeamJudges := map[string][]float64{}
	for k, v := range sums {
		byTeamJudges[k.team] = append(byTeamJudges[k.team], v)
	}
	out := make([]teamResult, 0, len(teams))
	for _, t := range teams {
		vals := byTeamJudges[t.ID]
		res := teamResult{TeamID: t.ID, TeamName: t.Name, JudgesScored: len(vals)}
		if len(vals) > 0 {
			sum := 0.0
			for _, v := range vals {
				sum += v
			}
			avg := math.Round((sum/float64(len(vals)))*100) / 100
			res.Total = &avg
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Total, out[j].Total
		if a == nil && b == nil {
			return out[i].TeamName < out[j].TeamName
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		if *a != *b {
			return *a > *b
		}
		return out[i].TeamName < out[j].TeamName
	})
	rank := 0
	for i := range out {
		if out[i].Total == nil {
			continue
		}
		rank++
		r := rank
		out[i].Rank = &r
		if i > 0 && out[i-1].Total != nil && *out[i-1].Total == *out[i].Total {
			out[i].Tied = true
			out[i-1].Tied = true
			out[i].Rank = out[i-1].Rank
			rank--
		}
	}
	return out, nil
}

// rankOverride is one team's chosen final place within its own tied group.
type rankOverride struct {
	TeamID string
	Rank   int
}

// applyRankOverrides resolves one or more tied groups in place. computeResults
// ranks densely (a tied pair shares one rank slot; the next team takes the
// very next integer, not a slot reserved for the tie's width), so an
// override cannot simply be written into the group's old rank numbers —
// that collides with whoever already occupies them. Instead, each
// override's Rank is used only to ORDER its group internally; the whole
// list is then re-sorted (equal totals broken by that order where a group
// was resolved, otherwise unchanged) and every scored team is renumbered
// sequentially from 1. A tied group left untouched keeps sharing one dense
// rank exactly as computeResults produced it; a resolved group's teams each
// get their own distinct, sequential rank like everyone else.
func applyRankOverrides(results []teamResult, overrides []rankOverride) error {
	byID := map[string]*teamResult{}
	for i := range results {
		byID[results[i].TeamID] = &results[i]
	}
	groups := map[int][]string{}
	for i := range results {
		if results[i].Tied && results[i].Rank != nil {
			groups[*results[i].Rank] = append(groups[*results[i].Rank], results[i].TeamID)
		}
	}
	assigned := map[string]int{}
	touchedGroups := map[int]bool{}
	for _, ov := range overrides {
		res, found := byID[ov.TeamID]
		if !found || !res.Tied || res.Rank == nil {
			return fmt.Errorf("%s is not part of a tie.", ov.TeamID)
		}
		touchedGroups[*res.Rank] = true
		assigned[ov.TeamID] = ov.Rank
	}
	resolved := map[string]bool{}
	for group := range touchedGroups {
		ids := groups[group]
		seen := map[int]bool{}
		for _, id := range ids {
			rk, ok := assigned[id]
			if !ok {
				return fmt.Errorf("give every tied team in a group a rank, or none of them")
			}
			if seen[rk] {
				return fmt.Errorf("tied teams need different ranks from each other")
			}
			seen[rk] = true
			resolved[id] = true
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i].Total, results[j].Total
		if a == nil && b == nil {
			return results[i].TeamName < results[j].TeamName
		}
		if a == nil {
			return false
		}
		if b == nil {
			return true
		}
		if *a != *b {
			return *a > *b
		}
		// Equal totals: they were tied together originally (that is what
		// made them tied), so they belong to the same group either way.
		if resolved[results[i].TeamID] && resolved[results[j].TeamID] {
			return assigned[results[i].TeamID] < assigned[results[j].TeamID]
		}
		return results[i].TeamName < results[j].TeamName
	})
	rank := 0
	for i := range results {
		if results[i].Total == nil {
			results[i].Rank = nil
			results[i].Tied = false
			continue
		}
		rank++
		r := rank
		results[i].Rank = &r
		results[i].Tied = false
		if i > 0 && results[i-1].Total != nil && *results[i-1].Total == *results[i].Total &&
			!resolved[results[i].TeamID] && !resolved[results[i-1].TeamID] {
			results[i].Tied = true
			results[i-1].Tied = true
			results[i].Rank = results[i-1].Rank
			rank--
		}
	}
	return nil
}

func (h *HackHandler) publishResults(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		RankOverrides []struct {
			TeamID string `json:"team_id"`
			Rank   int    `json:"rank"`
		} `json:"rank_overrides"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	results, err := computeResults(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: publish %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if len(req.RankOverrides) > 0 {
		overrides := make([]rankOverride, len(req.RankOverrides))
		for i, ov := range req.RankOverrides {
			overrides[i] = rankOverride{TeamID: ov.TeamID, Rank: ov.Rank}
		}
		if err := applyRankOverrides(results, overrides); err != nil {
			writeHackErr(w, http.StatusBadRequest, "invalid_rank_override", err.Error())
			return
		}
	}
	snapshot := resultsSnapshotJSON(results)
	body, err := json.Marshal(snapshot)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := db.UpsertEventResults(r.Context(), h.database, a.event.ID, a.user.ID, body); err != nil {
		log.Printf("hack: publish %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	full, err := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: publish %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": snapshot, "full_ranking": full.FullRanking})
}

func resultsSnapshotJSON(results []teamResult) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, res := range results {
		var total, rank any
		if res.Total != nil {
			total = *res.Total
		}
		if res.Rank != nil {
			rank = *res.Rank
		}
		out = append(out, map[string]any{
			"team_id":       res.TeamID,
			"team_name":     res.TeamName,
			"total":         total,
			"rank":          rank,
			"tied":          res.Tied,
			"judges_scored": res.JudgesScored,
		})
	}
	return out
}

func (h *HackHandler) patchResults(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		FullRanking *bool `json:"full_ranking"`
	}
	if !decodeHackJSON(w, r, &req) || req.FullRanking == nil {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "full_ranking is required")
		return
	}
	ok2, err := db.SetResultsFullRanking(r.Context(), h.database, a.event.ID, *req.FullRanking)
	if err != nil {
		log.Printf("hack: results %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if !ok2 {
		writeHackErr(w, http.StatusNotFound, "no_results_yet", "Publish results first.")
		return
	}
	res, err := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: results %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"full_ranking": res.FullRanking, "results": json.RawMessage(res.Snapshot)})
}

func (h *HackHandler) getPublicResults(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	// A taken-down event's results are not programmatically readable, even
	// though its page still partially renders (410) — a conservative choice
	// for a public, unauthenticated route.
	if ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	res, err := db.GetEventResults(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if !res.Published {
		writeHackErr(w, http.StatusNotFound, "no_results_yet", "Results are not published yet.")
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		writeInternal(w)
		return
	}
	// The organiser (and the platform admin) sees the same full snapshot
	// `publishResults` returns — every team, with total and judges_scored —
	// regardless of the public full_ranking switch, so the Judging tab can
	// redraw its own view correctly after a page reload. Auth here is
	// optional: a bad or missing key just falls through to the public view.
	if k := r.Header.Get("X-API-Key"); k != "" {
		if u, err := db.GetUserByAPIKey(r.Context(), h.database, k); err == nil {
			if u.IsAdmin {
				writeJSON(w, http.StatusOK, map[string]any{"results": rows, "full_ranking": res.FullRanking})
				return
			}
			if m, merr := db.GetEventMember(r.Context(), h.database, ev.ID, u.ID); merr == nil && m.Role == "organiser" {
				writeJSON(w, http.StatusOK, map[string]any{"results": rows, "full_ranking": res.FullRanking})
				return
			}
		}
	}
	if !res.FullRanking {
		winners := make([]map[string]any, 0, 1)
		for _, row := range rows {
			if rank, ok := row["rank"].(float64); ok && rank == 1 {
				winners = append(winners, map[string]any{"team_id": row["team_id"], "team_name": row["team_name"]})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"winners": winners})
		return
	}
	ranking := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row["rank"] == nil {
			continue
		}
		ranking = append(ranking, map[string]any{
			"team_id": row["team_id"], "team_name": row["team_name"],
			"rank": row["rank"], "tied": row["tied"],
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ranking": ranking})
}

func (h *HackHandler) getMyResults(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	res, err := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: my-results %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	if !res.Published {
		writeJSON(w, http.StatusOK, map[string]any{"published": false})
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		writeInternal(w)
		return
	}
	var mine map[string]any
	for _, row := range rows {
		if row["team_id"] == team.ID {
			mine = row
			break
		}
	}
	comments, err := db.TeamComments(r.Context(), h.database, a.event.ID, team.ID)
	if err != nil {
		log.Printf("hack: my-results %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	sort.Strings(comments)
	out := map[string]any{"published": true, "comments": comments}
	if mine != nil {
		out["total"] = mine["total"]
		out["rank"] = mine["rank"]
		out["tied"] = mine["tied"]
	} else {
		out["total"] = nil
		out["rank"] = nil
		out["tied"] = false
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *HackHandler) exportScoresCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := db.ListRawScores(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: export scores %s: %v", a.event.Slug, err)
		writeInternal(w)
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].TeamName != rows[j].TeamName {
			return strings.ToLower(rows[i].TeamName) < strings.ToLower(rows[j].TeamName)
		}
		if rows[i].JudgeName != rows[j].JudgeName {
			return strings.ToLower(rows[i].JudgeName) < strings.ToLower(rows[j].JudgeName)
		}
		return rows[i].CriterionPosition < rows[j].CriterionPosition
	})
	filename := fmt.Sprintf("%s-scores-%s.csv", a.event.Slug, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"team", "judge", "criterion", "points", "max_points", "comment"})
	for _, row := range rows {
		_ = cw.Write([]string{
			csvSafe(row.TeamName), csvSafe(row.JudgeName), csvSafe(row.CriterionName),
			strconv.Itoa(row.Points), strconv.Itoa(row.MaxPoints), csvSafe(row.Comment),
		})
	}
	cw.Flush()
}

func (h *HackHandler) exportResultsCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	// Prefer the published, tie-resolved snapshot so this matches exactly
	// what teams and the public already see; only compute live when nothing
	// has been published yet (so the organiser can still get a working
	// export before the first publish).
	type row struct {
		TeamName     string
		Total        *float64
		Rank         *int
		Tied         bool
		JudgesScored int
	}
	var rows []row
	published, perr := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if perr != nil {
		log.Printf("hack: export results %s: %v", a.event.Slug, perr)
		writeInternal(w)
		return
	}
	if published.Published {
		var snap []map[string]any
		if err := json.Unmarshal(published.Snapshot, &snap); err != nil {
			log.Printf("hack: export results %s: snapshot: %v", a.event.Slug, err)
			writeInternal(w)
			return
		}
		for _, m := range snap {
			rw := row{JudgesScored: int(jsonFloat(m["judges_scored"]))}
			if name, ok := m["team_name"].(string); ok {
				rw.TeamName = name
			}
			if t, ok := m["total"].(float64); ok {
				rw.Total = &t
			}
			if rk, ok := m["rank"].(float64); ok {
				n := int(rk)
				rw.Rank = &n
			}
			if tied, ok := m["tied"].(bool); ok {
				rw.Tied = tied
			}
			rows = append(rows, rw)
		}
	} else {
		results, err := computeResults(r.Context(), h.database, a.event.ID)
		if err != nil {
			log.Printf("hack: export results %s: %v", a.event.Slug, err)
			writeInternal(w)
			return
		}
		for _, res := range results {
			rows = append(rows, row{TeamName: res.TeamName, Total: res.Total, Rank: res.Rank, Tied: res.Tied, JudgesScored: res.JudgesScored})
		}
	}
	filename := fmt.Sprintf("%s-results-%s.csv", a.event.Slug, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"team", "total", "rank", "tied", "judges_scored"})
	for _, res := range rows {
		total, rank := "", ""
		if res.Total != nil {
			total = strconv.FormatFloat(*res.Total, 'f', 2, 64)
		}
		if res.Rank != nil {
			rank = strconv.Itoa(*res.Rank)
		}
		_ = cw.Write([]string{csvSafe(res.TeamName), total, rank, strconv.FormatBool(res.Tied), strconv.Itoa(res.JudgesScored)})
	}
	cw.Flush()
}

// jsonFloat reads a JSON-decoded number (always float64 via encoding/json's
// map[string]any) or 0 for anything else (missing, null, wrong type).
func jsonFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}
