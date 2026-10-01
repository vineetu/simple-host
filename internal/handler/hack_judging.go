package handler

import (
	"database/sql"
	"errors"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
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
