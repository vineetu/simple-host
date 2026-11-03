package hackhttp

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/papermetrics"
)

// paperHackTimes moved from hack_paper_metrics.go (cleanup 3.2 slice 1c):
// used only here, no *SiteHandler dependency.
func paperHackTimes(r *http.Request) { papermetrics.HackPhasesChanged(r.Context()) }

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
	mux.Handle("DELETE /v1/hack/events/{slug}/judging/former-judges/{user_id}", wrap(h.deleteFormerJudgeScores))
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
	h.registerJudgingOptions(mux, wrap)
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
		httpx.WriteError(w, http.StatusConflict, "scores_locked", "Judging is locked; unlock it first.")
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
	// Teams can see the rubric (2026-11-02, audit item 1): an approved
	// participant reads the same criteria judges and organisers do --
	// names, descriptions, weights and max points, never a score.
	// loadMember's own approval-status gate (len(roles) > 0) still refuses a
	// pending or rejected participant with 403 approval_<status>, and a
	// non-member still gets 404 event_not_found. rubricJSON never includes
	// scores, so no response-shape change is needed for this role.
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser", "judge", "participant")
	if !ok {
		return
	}
	items, err := h.entries.Rubric(r.Context(), a.event.ID)
	if err != nil {
		log.Printf("hack: rubric %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rubricJSON(items))
}

func (h *HackHandler) putRubric(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	// Locked wins over a body that would also fail validation: the organiser's
	// next step is to unlock, not to repair the rubric.
	if a.event.JudgingLockedAt.Valid {
		httpx.WriteError(w, http.StatusConflict, "scores_locked", "Judging is locked; unlock it first to change the rubric.")
		return
	}
	var req struct {
		ConfirmScoreDeletion bool `json:"confirm_score_deletion"`
		Criteria             []struct {
			ID          string   `json:"id"`
			Name        string   `json:"name"`
			Description string   `json:"description"`
			Weight      *float64 `json:"weight"`
			MaxPoints   *float64 `json:"max_points"`
		} `json:"criteria"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	items, msg := hackentries.ValidateRubric(req.Criteria)
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_rubric", msg)
		return
	}
	ids := make([]string, len(req.Criteria))
	for i, c := range req.Criteria {
		ids[i] = c.ID
	}
	saved, err := h.entries.ReplaceRubric(r.Context(), a.event.ID, items, ids, req.ConfirmScoreDeletion)
	if err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := http.StatusConflict
			if e.Code == "invalid_rubric" {
				status = http.StatusBadRequest
			}
			httpx.WriteError(w, status, e.Code, e.Message)
		} else {
			log.Printf("hack: rubric %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rubricJSON(saved))
}

func judgingSettingsJSON(ev db.Event) map[string]any {
	return map[string]any{
		"assignment_mode":  ev.JudgeAssignmentMode,
		"judges_per_team":  ev.JudgesPerTeam,
		"score_mode":       ev.ScoreMode,
		"tie_criterion_id": nullableString(ev.TieCriterionID),
		"public_scores":    ev.PublicScores,
		"public_ranks":     ev.PublicRanks,
	}
}

func (h *HackHandler) getJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, judgingSettingsJSON(a.event))
}

func (h *HackHandler) patchJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		AssignmentMode *string  `json:"assignment_mode"`
		JudgesPerTeam  *float64 `json:"judges_per_team"`
		ScoreMode      *string  `json:"score_mode"`
		TieCriterionID *string  `json:"tie_criterion_id"`
		PublicScores   *bool    `json:"public_scores"`
		PublicRanks    *bool    `json:"public_ranks"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	var mode *string
	if req.AssignmentMode != nil {
		if *req.AssignmentMode != "open" && *req.AssignmentMode != "automatic" && *req.AssignmentMode != "manual" && *req.AssignmentMode != "panel" {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", `assignment_mode must be open, automatic, manual or panel.`)
			return
		}
		mode = req.AssignmentMode
	}
	var perTeam *int
	if req.JudgesPerTeam != nil {
		n, ok := hackentries.WholeInRange(req.JudgesPerTeam, 1, 20)
		if !ok {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "judges_per_team must be a whole number from 1 to 20.")
			return
		}
		perTeam = &n
	}
	if req.ScoreMode != nil && *req.ScoreMode != "raw" && *req.ScoreMode != "normalised" {
		httpx.WriteError(w, 400, "invalid_score_mode", "score_mode must be raw or normalised")
		return
	}
	if req.TieCriterionID != nil && *req.TieCriterionID != "" && !sites.UUIDShape.MatchString(*req.TieCriterionID) {
		httpx.WriteError(w, 400, "invalid_tie_criterion", "use a criterion in this event")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.Stage == "archived" {
		httpx.WriteError(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	if (mode != nil || perTeam != nil) && writeIfJudgingLocked(w, ev) {
		return
	}
	if req.TieCriterionID != nil {
		if ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived" {
			httpx.WriteError(w, 409, "tie_rule_closed", "choose the tie criterion before judging begins")
			return
		}
		if *req.TieCriterionID != "" {
			valid, err := db.EventCriterionInEvent(r.Context(), tx, ev.ID, *req.TieCriterionID)
			if err != nil {
				httpx.WriteInternal(w)
				return
			}
			if !valid {
				httpx.WriteError(w, 404, "criterion_not_found", "criterion not in this event")
				return
			}
		}
	}
	if mode != nil || perTeam != nil {
		ev, err = db.UpdateJudgingSettings(r.Context(), tx, ev.ID, mode, perTeam)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	var scoreMode, publicScores, publicRanks any
	if req.ScoreMode != nil {
		scoreMode = *req.ScoreMode
	}
	if req.PublicScores != nil {
		publicScores = *req.PublicScores
	}
	if req.PublicRanks != nil {
		publicRanks = *req.PublicRanks
	}
	if req.ScoreMode != nil || req.TieCriterionID != nil || req.PublicScores != nil || req.PublicRanks != nil {
		var tie any
		if req.TieCriterionID != nil {
			tie = *req.TieCriterionID
		}
		_, err = tx.ExecContext(r.Context(), `UPDATE events SET score_mode=COALESCE($2::text,score_mode),
			tie_criterion_id=CASE WHEN $3::boolean THEN NULLIF($4::text,'')::uuid ELSE tie_criterion_id END,
			public_scores=COALESCE($5::boolean,public_scores), public_ranks=COALESCE($6::boolean,public_ranks), updated_at=now() WHERE id=$1`, ev.ID, scoreMode, req.TieCriterionID != nil, tie, publicScores, publicRanks)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	ev, err = db.GetEventForUpdate(r.Context(), tx, ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, judgingSettingsJSON(ev))
}

// judgingPool sorts the event's teams (ListTeamsWithMembers) by whether they
// will be judged, the same rule the judge queue applies once judging starts
// (full-test fix, 2026-11-02): a team is in when it has an entry an
// organiser has not excluded -- submitted, settled or still a draft, since
// moving to judging settles every draft (settleEntriesTx) -- or, with no
// entry row, a live team site (settleEntriesTx starts an entry for it). A
// team with neither has no entry and stays out; an excluded entry stays out.
// Automatic assignment, the Judging progress and Overview's Start judging
// confirm all use this, so none of them counts a team the queue never shows.
func (h *HackHandler) judgingPoolFor(ctx context.Context, ev db.Event, teams []db.NamedTeam) (entries.Pool, error) {
	var tl teamList
	loaded := false
	return h.entries.JudgingPool(ctx, ev, teams, func(t db.NamedTeam) (bool, error) {
		if !loaded {
			var err error
			tl, err = h.loadTeamList(ctx, ev)
			if err != nil {
				return false, err
			}
			loaded = true
		}
		st := tl.states[t.ID]
		site := tl.site(t.Slug)
		return site != nil && site.ActiveVersion > 0 && !site.Suspended() && !st.SiteTakenDown && !st.EventTakenDown, nil
	})
}

func (h *HackHandler) generateAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		IncludeOrganisers bool `json:"include_organisers"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !decodeHackJSON(w, r, &req) {
			return
		}
	}
	// Read before the transaction: judgingPoolFor reads team sites and may
	// pin due teams in transactions of its own.
	allTeams, err := db.ListTeamsWithMembers(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	pool, err := h.judgingPoolFor(r.Context(), a.event, allTeams)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	planned, under, err := h.entries.GenerateAssignments(r.Context(), a.event.ID, req.IncludeOrganisers, pool.In)
	if err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := http.StatusConflict
			if e.Code == "too_few_judges" || e.Code == "too_few_teams" {
				status = http.StatusBadRequest
			}
			httpx.WriteError(w, status, e.Code, e.Message)
		} else {
			log.Printf("hack: assignments %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, assignmentJSON(planned, under))
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
		// had_score (Codex review, Grok retest 3 Cosmetic #4): whether the
		// judge already has score rows for this team -- the judge-queue
		// undo dialog uses this directly instead of probing
		// .../judge/scores/{team}, which 403s for a conflicted team.
		"had_score": c.HadScore,
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
	rows, err := h.entries.Conflicts(r.Context(), a.event.ID, judgeID)
	if err != nil {
		log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, conflictJSON(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"conflicts": out})
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
			httpx.WriteError(w, http.StatusForbidden, "forbidden", "Only an organiser can record a conflict for another judge.")
			return
		}
	} else if strings.TrimSpace(req.JudgeUserID) != "" {
		judgeID = req.JudgeUserID
		declaredBy = "organiser"
	}
	if !sites.UUIDShape.MatchString(req.TeamID) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if !sites.UUIDShape.MatchString(judgeID) {
		httpx.WriteError(w, http.StatusNotFound, "judge_not_found", "judge not found")
		return
	}
	saved, err := h.entries.DeclareConflict(r.Context(), a.event.ID, judgeID, req.TeamID, declaredBy)
	if err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := http.StatusConflict
			if e.Code == "team_not_found" || e.Code == "judge_not_found" {
				status = http.StatusNotFound
			}
			httpx.WriteError(w, status, e.Code, e.Message)
		} else {
			log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, conflictJSON(saved))
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
			httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "You can only remove your own conflict.")
			return
		}
	} else if q != "" {
		judgeID = q
	}
	if !sites.UUIDShape.MatchString(teamID) || !sites.UUIDShape.MatchString(judgeID) {
		httpx.WriteError(w, http.StatusNotFound, "conflict_not_found", "conflict not found")
		return
	}
	if err := h.entries.DeleteConflict(r.Context(), a.event.ID, judgeID, teamID); err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := http.StatusConflict
			if e.Code == "conflict_not_found" {
				status = http.StatusNotFound
			}
			httpx.WriteError(w, status, e.Code, e.Message)
		} else {
			log.Printf("hack: conflicts %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
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
	allTeams, err := db.ListTeamsWithMembers(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	// TODO(4.1): This prospective pool includes unsettled entries and live sites; the queue requires judgeable entries.
	// Progress lists only the teams that will be judged (judgingPoolFor),
	// the same set the queue and automatic assignment use.
	pool, err := h.judgingPoolFor(ctx, a.event, allTeams)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	teams := make([]db.NamedTeam, 0, len(allTeams))
	for _, t := range allTeams {
		if pool.In[t.ID] {
			teams = append(teams, t)
		}
	}
	judges, err := db.ListEventJudges(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	scoredTeams, scoredJudges, err := db.ScoreCoverage(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
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
	assigned, eligibleByTeam, err := h.entries.EligibilityCounts(ctx, a.event.ID, judges)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	teamRows := make([]map[string]any, 0, len(teams))
	for _, team := range teams {
		scored := scoredTeams[team.ID]
		teamTarget := target
		if a.event.JudgeAssignmentMode == "manual" || a.event.JudgeAssignmentMode == "panel" {
			if eligibleByTeam[team.ID] > 0 && eligibleByTeam[team.ID] < teamTarget {
				teamTarget = eligibleByTeam[team.ID]
			}
		}
		teamRows = append(teamRows, map[string]any{
			"team_id":       team.ID,
			"team_name":     team.Name,
			"judges_scored": scored,
			"flagged":       scored < teamTarget,
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
	former, err := db.ListFormerJudges(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	formerRows := make([]map[string]any, 0, len(former))
	for _, f := range former {
		assignedCount, err := db.FormerJudgeAssignedCount(ctx, h.database, a.event.ID, f.UserID)
		if err != nil {
			log.Printf("hack: dashboard %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
			return
		}
		formerRows = append(formerRows, map[string]any{
			"user_id":        f.UserID,
			"display_name":   f.DisplayName,
			"role":           f.Role,
			"left_at":        rfc3339Time(f.LeftAt),
			"counted_teams":  scoredJudges[f.UserID],
			"assigned_count": assignedCount,
		})
	}
	// Tied totals need scores. The next pass fills this once totals exist.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"teams":         teamRows,
		"judges":        judgeRows,
		"former_judges": formerRows,
		"tied_groups":   []any{},
		// What Start judging will do, for Overview's confirm: teams that
		// will be judged, teams with no entry (left out), and entries an
		// organiser excluded by hand.
		"entries": map[string]any{"judged": pool.Judged, "no_entry": pool.NoEntry, "excluded": pool.Excluded},
	})
}

// deleteFormerJudgeScores is DELETE .../judging/former-judges/{user_id}
// (SIM-340): an organiser dropping a departed judge's scores so they stop
// counting. Published results change only on the next publish (the snapshot
// already taken is untouched).
func (h *HackHandler) deleteFormerJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(userID) {
		httpx.WriteError(w, http.StatusNotFound, "former_judge_not_found", "former judge not found")
		return
	}
	// Destructive, like delete-event and leave's close:"delete" (Grok
	// security review, SIM-340): confirm must repeat the event's address
	// name, so an agent or a stray retry can't drop a judge's scores without
	// the person having agreed to exactly this event.
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Confirm != a.event.Slug {
		httpx.WriteError(w, http.StatusConflict, "confirm_required", `to delete this judge's scores, send {"confirm":"`+a.event.Slug+`"} with this event's address name`)
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return
	}
	if ev.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	if writeIfJudgingLocked(w, ev) {
		return
	}
	// Re-check the caller is still an organiser now that the event is
	// locked (Grok security review, SIM-340): loadMember's check above runs
	// before this transaction, so a concurrent remove-organiser that
	// commits while this request waits on the event lock must not still
	// let the delete through.
	var role string
	if err := tx.QueryRowContext(r.Context(), `SELECT role FROM event_members WHERE event_id = $1 AND user_id = $2 FOR SHARE`, a.event.ID, a.user.ID).Scan(&role); errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	} else if err != nil {
		httpx.WriteInternal(w)
		return
	} else if role != "organiser" {
		writeEventNotFound(w)
		return
	}
	found, err := db.DeleteFormerJudgeScores(r.Context(), tx, a.event.ID, userID)
	if err != nil {
		log.Printf("hack: delete former judge scores %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if !found {
		httpx.WriteError(w, http.StatusNotFound, "former_judge_not_found", "former judge not found")
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, judgingLockJSON(ev, true))
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
		httpx.WriteInternal(w)
		return
	}
	log.Printf("hack: judging unlock %s by=%s reason=%q", a.event.Slug, a.user.ID, reason)
	httpx.WriteJSON(w, http.StatusOK, judgingLockJSON(ev, false))
}

func (h *HackHandler) judgeQueue(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "judge", "organiser")
	if !ok {
		return
	}
	if a.admin {
		writeEventNotFound(w)
		return
	}
	ctx := r.Context()
	var tl teamList
	queue, err := h.entries.JudgeQueue(ctx, a.event, a.user.ID, func() error {
		var err error
		tl, err = h.loadTeamList(ctx, a.event)
		return err
	})
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	fields := hackentries.LoadRules(a.event.EntryRules)
	tracks, err := db.ListEventTracks(ctx, h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	hasTracks := len(tracks) > 0
	mayLink, err := h.judgeSiteLinkGate(ctx, a)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(queue))
	for _, row := range queue {
		team := row.Team
		item, ierr := h.entryListItem(ctx, a.event, db.EventTeam{ID: team.ID, Name: team.Name, Slug: team.Slug}, row.Entry, fields, hasTracks, tl)
		if ierr != nil {
			log.Printf("hack: queue %s/%s: %v", a.event.Slug, team.ID, ierr)
			httpx.WriteInternal(w)
			return
		}
		item["team_id"] = team.ID
		if !mayLink(team.ID) {
			redactEntrySiteLinks(item)
		}
		item["done"] = row.Done
		item["scores_count"] = row.ScoresCount
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queue": out, "judge_first": a.event.JudgeFirst, "assignment_mode": a.event.JudgeAssignmentMode})
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
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "judge", "organiser")
	if !ok {
		return
	}
	if a.admin {
		writeEventNotFound(w)
		return
	}
	teamID := r.PathValue("team")
	scores, comment, err := h.entries.JudgeScores(r.Context(), a.event.ID, a.user.ID, teamID)
	if err != nil {
		if e, ok := err.(*entries.Error); ok {
			httpx.WriteError(w, http.StatusForbidden, e.Code, e.Message)
		} else {
			log.Printf("hack: scores %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scoresJSON(scores, comment, nil))
}

func (h *HackHandler) putJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "judge", "organiser")
	if !ok {
		return
	}
	if a.event.JudgingLockedAt.Valid {
		httpx.WriteError(w, http.StatusConflict, "scores_locked", "Judging is locked.")
		return
	}
	if !h.scoringStage(r.Context(), a.event) {
		httpx.WriteError(w, 409, "scoring_not_open", "Scoring opens when entries close.")
		return
	}
	teamID := r.PathValue("team")
	if err := h.entries.CheckScoreConflict(r.Context(), a.event.ID, a.user.ID, teamID); err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := http.StatusForbidden
			if e.Code == "team_not_found" {
				status = http.StatusNotFound
			}
			httpx.WriteError(w, status, e.Code, e.Message)
		} else {
			log.Printf("hack: scores %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
		return
	}
	var req struct {
		Scores  []entries.ScoreInput `json:"scores"`
		Comment *string              `json:"comment"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Comment != nil {
		cleaned, err := h.entries.JudgeComment(*req.Comment)
		if err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_comment", err.Error())
			return
		}
		req.Comment = &cleaned
	}
	updated, comment, complete, err := h.entries.PutJudgeScores(r.Context(), a.event.ID, a.user.ID, teamID, req.Scores, req.Comment, h.scoringStage)
	if err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := http.StatusConflict
			switch e.Code {
			case "forbidden":
				status = http.StatusForbidden
			case "criterion_not_found":
				status = http.StatusNotFound
			case "invalid_points", "invalid_request":
				status = http.StatusBadRequest
			}
			httpx.WriteError(w, status, e.Code, e.Message)
		} else {
			log.Printf("hack: scores %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scoresJSON(updated, comment, &complete))
}

type teamResult = hackentries.TeamResult[db.EventTrack]
type rankOverride = hackentries.RankOverride

func trackIDs(rows []teamResult) map[string]string {
	ids := make(map[string]string)
	for _, row := range rows {
		if row.Track != nil {
			ids[row.TeamID] = row.Track.ID
		}
	}
	return ids
}
func markTrackWinners(rows []teamResult) { hackentries.MarkTrackWinners(rows, trackIDs(rows)) }
func applyRankOverrides(rows []teamResult, overrides []rankOverride) error {
	return hackentries.ApplyRankOverrides(rows, overrides, trackIDs(rows))
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	results, err := computeResultsWithOptions(r.Context(), tx, ev)
	if err != nil {
		log.Printf("hack: publish %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if len(req.RankOverrides) > 0 {
		overrides := make([]rankOverride, len(req.RankOverrides))
		for i, ov := range req.RankOverrides {
			overrides[i] = rankOverride{TeamID: ov.TeamID, Rank: ov.Rank}
		}
		if err := applyRankOverrides(results, overrides); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_rank_override", err.Error())
			return
		}
	}
	cards, err := h.resultsEntryCards(r.Context(), tx, a.event, results)
	if err != nil {
		log.Printf("hack: publish %s: cards: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	snapshot := resultsSnapshotJSON(results, cards)
	body, err := json.Marshal(snapshot)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if err := db.UpsertEventResults(r.Context(), tx, a.event.ID, a.user.ID, body); err != nil {
		log.Printf("hack: publish %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	paperHackTimes(r)
	full, err := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: publish %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": snapshot, "full_ranking": full.FullRanking})
}

// resultsSnapshotJSON is the array stored as the published results snapshot
// and returned by POST .../results/publish (build-plan §4.6). Each ranked
// team's row also carries its public summary card under "entry" — the same
// card every ranked team gets, keyed implicitly by the row's own "team_id"
// rather than a separate top-level map, so the snapshot stays the plain
// array every existing reader (getPublicResults, getMyResults,
// hackResultsFromSnapshot) already unmarshals with `[]map[string]any`. A
// snapshot published before this slice simply has no "entry" key on any
// row, which every reader already treats as "no card" (judgement call;
// documented in the slice 3 report).
func resultsSnapshotJSON(results []teamResult, cards map[string]map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(results))
	for _, res := range results {
		var total, rank any
		if res.Total != nil {
			total = *res.Total
		}
		if res.Rank != nil {
			rank = *res.Rank
		}
		row := map[string]any{
			"team_id":          res.TeamID,
			"team_name":        res.TeamName,
			"total":            total,
			"rank":             rank,
			"tied":             res.Tied,
			"judges_scored":    res.JudgesScored,
			"raw_total":        res.RawTotal,
			"normalised_total": res.NormalisedTotal,
			"deciding_mode":    res.ScoreMode,
			"tie_decider":      res.TieDecider,
			"track":            res.Track,
			"track_rank":       res.TrackRank,
			"track_winner":     res.TrackWinner,
		}
		if card, ok := cards[res.TeamID]; ok {
			row["entry"] = card
		}
		out = append(out, row)
	}
	return out
}

// resultsEntryCards builds every ranked team's public summary card for a
// results snapshot (build-plan §4.6): title, tagline, problem, audience,
// tools, maturity, track, the video link and its host, the code link
// (omitted -- left "" -- when the event's code rule is Off), the site link
// (only when the team's site is currently live and not taken down; a
// passcode or named-viewer site still gets its public URL, since the
// visitor just meets the gate there, never a preview link) and whether a
// screenshot is stored. Only ranked teams need a card: an excluded or never-
// scored entry has no Rank and so is never looked up here, which is how
// "excluded entries get no card" falls out for free -- EntryJudgeableSQL
// already kept them out of every judge's eligible set, so they carry no
// score and computeResultsWithOptions never ranks them.
func (h *HackHandler) resultsEntryCards(ctx context.Context, q db.Querier, ev db.Event, results []teamResult) (map[string]map[string]any, error) {
	ranked := false
	for _, res := range results {
		if res.Rank != nil {
			ranked = true
			break
		}
	}
	if !ranked {
		return nil, nil
	}
	teams, err := db.ListTeamsWithMembers(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	slugByID := make(map[string]string, len(teams))
	for _, t := range teams {
		slugByID[t.ID] = t.Slug
	}
	entries, err := db.ListEventEntries(ctx, q, ev.ID)
	if err != nil {
		return nil, err
	}
	entryByTeam := make(map[string]db.EventEntry, len(entries))
	for _, e := range entries {
		entryByTeam[e.TeamID] = e
	}
	tl, err := h.loadTeamList(ctx, ev)
	if err != nil {
		return nil, err
	}
	fields := hackentries.LoadRules(ev.EntryRules)
	codeOff := fields["code_url"] == "off"
	out := make(map[string]map[string]any, len(results))
	for _, res := range results {
		if res.Rank == nil {
			continue
		}
		e := entryByTeam[res.TeamID]
		slug := slugByID[res.TeamID]
		site := tl.site(slug)
		st := tl.states[res.TeamID]
		live := site != nil && site.ActiveVersion > 0 && !site.Suspended()
		takenDown := st.SiteTakenDown || st.EventTakenDown
		siteURL := ""
		if live && !takenDown {
			siteURL = h.teamSiteURL(ev.Slug, slug)
		}
		videoHost, codeHost := "", ""
		if e.VideoURL != "" {
			videoHost = hackURLHost(e.VideoURL)
		}
		codeURL := e.CodeURL
		if codeOff {
			codeURL = ""
		}
		if codeURL != "" {
			codeHost = hackURLHost(codeURL)
		}
		out[res.TeamID] = map[string]any{
			"title": e.Title, "tagline": e.Tagline, "problem": e.Problem, "audience": e.Audience,
			"tools": nonNilStrings(e.Tools), "maturity": e.Maturity, "track": res.Track,
			"video_url": e.VideoURL, "video_host": videoHost,
			"code_url": codeURL, "code_host": codeHost,
			"site_url": siteURL, "has_screenshot": e.HasScreenshot,
		}
	}
	return out, nil
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
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "full_ranking is required")
		return
	}
	ok2, err := db.SetResultsFullRanking(r.Context(), h.database, a.event.ID, *req.FullRanking)
	if err != nil {
		log.Printf("hack: results %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	if !ok2 {
		httpx.WriteError(w, http.StatusNotFound, "no_results_yet", "Publish results first.")
		return
	}
	res, err := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: results %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"full_ranking": res.FullRanking, "results": json.RawMessage(res.Snapshot)})
}

func (h *HackHandler) getPublicResults(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(r.PathValue("slug")))
	ev, err := db.GetEventBySlug(r.Context(), h.database, slug)
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
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
		httpx.WriteInternal(w)
		return
	}
	if !res.Published {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"published": false})
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		httpx.WriteInternal(w)
		return
	}
	// The organiser (and the platform admin) sees the same full snapshot
	// `publishResults` returns — every team, with total and judges_scored —
	// regardless of the public full_ranking switch, so the Judging tab can
	// redraw its own view correctly after a page reload. Auth here is
	// optional: a bad or missing key just falls through to the public view.
	if k := r.Header.Get("X-API-Key"); k != "" {
		if u, err := db.GetUserByAPIKey(r.Context(), h.database, k, h.limits.KeyIdleExpiry); err == nil {
			if u.IsAdmin {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": rows, "full_ranking": res.FullRanking})
				return
			}
			if m, merr := db.GetEventMember(r.Context(), h.database, ev.ID, u.ID); merr == nil && m.Role == "organiser" {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{"results": rows, "full_ranking": res.FullRanking})
				return
			}
		}
	}
	live := h.liveResultSites(r.Context(), ev)
	if !res.FullRanking {
		winners := make([]map[string]any, 0, 1)
		for _, row := range rows {
			_, global := row["rank"].(float64)
			if (global && row["rank"].(float64) == 1) || row["track_winner"] == true {
				item := map[string]any{"team_id": row["team_id"], "team_name": row["team_name"], "track": row["track"], "track_winner": row["track_winner"], "deciding_mode": row["deciding_mode"]}
				if ev.PublicScores {
					item["total"] = row["total"]
					item["raw_total"] = row["raw_total"]
					item["normalised_total"] = row["normalised_total"]
				}
				if row["entry"] != nil {
					item["entry"] = publicResultEntry(row["entry"], row["team_id"], live)
				}
				winners = append(winners, item)
			}
		}
		if !ev.PublicRanks {
			sort.Slice(winners, func(i, j int) bool {
				return winners[i]["team_name"].(string) < winners[j]["team_name"].(string)
			})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"winners": winners})
		return
	}
	ranking := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if row["rank"] == nil {
			continue
		}
		item := map[string]any{
			"team_id": row["team_id"], "team_name": row["team_name"],
			"track": row["track"], "track_winner": row["track_winner"], "deciding_mode": row["deciding_mode"],
		}
		if ev.PublicRanks {
			item["rank"] = row["rank"]
			item["track_rank"] = row["track_rank"]
			item["tied"] = row["tied"]
		}
		if ev.PublicScores {
			item["total"] = row["total"]
			item["raw_total"] = row["raw_total"]
			item["normalised_total"] = row["normalised_total"]
		}
		if row["entry"] != nil {
			item["entry"] = publicResultEntry(row["entry"], row["team_id"], live)
		}
		ranking = append(ranking, item)
	}
	if !ev.PublicRanks {
		sort.Slice(ranking, func(i, j int) bool {
			return ranking[i]["team_name"].(string) < ranking[j]["team_name"].(string)
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ranking": ranking})
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
		httpx.WriteInternal(w)
		return
	}
	if !res.Published {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"published": false})
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal(res.Snapshot, &rows); err != nil {
		httpx.WriteInternal(w)
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
		httpx.WriteInternal(w)
		return
	}
	sort.Strings(comments)
	// ranked_teams is how many teams got a place, the "of N" in "1st of 2
	// judged teams": teams with no entry or no scores have no place, so the
	// event's team count would overstate it (full-test fix, 2026-11-02).
	ranked := 0
	for _, row := range rows {
		if row["rank"] != nil {
			ranked++
		}
	}
	out := map[string]any{"published": true, "comments": comments, "ranked_teams": ranked}
	// shownPublicly and entry (build-plan §9 point 5, participant-results.js
	// "Your entry as the public sees it"): the same visibility the public
	// page and /results already apply -- full ranking shows every ranked
	// row, winners-only shows only rank 1 or a track winner, and an
	// unranked (unscored or excluded) team is never shown either way. The
	// team always sees its own rank/score above regardless; this flag is
	// only about what a STRANGER sees, so the screen can say so plainly.
	shownPublicly := false
	if mine != nil && mine["rank"] != nil {
		rank, _ := mine["rank"].(float64)
		trackWinner, _ := mine["track_winner"].(bool)
		shownPublicly = res.FullRanking || rank == 1 || trackWinner
	}
	out["shown_publicly"] = shownPublicly
	if shownPublicly && mine != nil {
		out["entry"] = mine["entry"]
	} else {
		out["entry"] = nil
	}
	if mine != nil {
		out["total"] = mine["total"]
		out["raw_total"] = mine["raw_total"]
		out["normalised_total"] = mine["normalised_total"]
		out["deciding_mode"] = mine["deciding_mode"]
		out["track"] = mine["track"]
		out["track_rank"] = mine["track_rank"]
		out["track_winner"] = mine["track_winner"]
		out["rank"] = mine["rank"]
		out["tied"] = mine["tied"]
	} else {
		out["total"] = nil
		out["raw_total"] = nil
		out["normalised_total"] = nil
		out["deciding_mode"] = a.event.ScoreMode
		out["track"] = nil
		out["track_rank"] = nil
		out["track_winner"] = false
		out["rank"] = nil
		out["tied"] = false
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *HackHandler) exportScoresCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	rows, err := db.ListRawScores(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: export scores %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
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
			httpx.CSVSafe(row.TeamName), httpx.CSVSafe(row.JudgeName), httpx.CSVSafe(row.CriterionName),
			strconv.Itoa(row.Points), strconv.Itoa(row.MaxPoints), httpx.CSVSafe(row.Comment),
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
		TeamName        string
		Total           *float64
		RawTotal        *float64
		NormalisedTotal *float64
		DecidingMode    string
		TrackName       string
		TrackPrize      string
		TrackRank       *int
		TrackWinner     bool
		Rank            *int
		Tied            bool
		JudgesScored    int
	}
	var rows []row
	published, perr := db.GetEventResults(r.Context(), h.database, a.event.ID)
	if perr != nil {
		log.Printf("hack: export results %s: %v", a.event.Slug, perr)
		httpx.WriteInternal(w)
		return
	}
	if published.Published {
		var snap []map[string]any
		if err := json.Unmarshal(published.Snapshot, &snap); err != nil {
			log.Printf("hack: export results %s: snapshot: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
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
			if v, ok := m["raw_total"].(float64); ok {
				rw.RawTotal = &v
			}
			if v, ok := m["normalised_total"].(float64); ok {
				rw.NormalisedTotal = &v
			}
			if v, ok := m["deciding_mode"].(string); ok {
				rw.DecidingMode = v
			}
			if v, ok := m["track"].(map[string]any); ok {
				rw.TrackName, _ = v["name"].(string)
				rw.TrackPrize, _ = v["prize"].(string)
			}
			if v, ok := m["track_rank"].(float64); ok {
				n := int(v)
				rw.TrackRank = &n
			}
			rw.TrackWinner, _ = m["track_winner"].(bool)
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
		results, err := computeResultsWithOptions(r.Context(), h.database, a.event)
		if err != nil {
			log.Printf("hack: export results %s: %v", a.event.Slug, err)
			httpx.WriteInternal(w)
			return
		}
		for _, res := range results {
			rw := row{TeamName: res.TeamName, Total: res.Total, RawTotal: res.RawTotal, NormalisedTotal: res.NormalisedTotal, DecidingMode: res.ScoreMode, TrackRank: res.TrackRank, TrackWinner: res.TrackWinner, Rank: res.Rank, Tied: res.Tied, JudgesScored: res.JudgesScored}
			if res.Track != nil {
				rw.TrackName = res.Track.Name
				rw.TrackPrize = res.Track.Prize
			}
			rows = append(rows, rw)
		}
	}
	filename := fmt.Sprintf("%s-results-%s.csv", a.event.Slug, time.Now().UTC().Format("2006-01-02"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.WriteHeader(http.StatusOK)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"team", "total", "rank", "tied", "judges_scored", "raw_total", "normalised_total", "deciding_mode", "track", "track_prize", "track_rank", "track_winner"})
	for _, res := range rows {
		total, rank := "", ""
		if res.Total != nil {
			total = strconv.FormatFloat(*res.Total, 'f', 2, 64)
		}
		if res.Rank != nil {
			rank = strconv.Itoa(*res.Rank)
		}
		rawTotal, normTotal, trackRank := "", "", ""
		if res.RawTotal != nil {
			rawTotal = strconv.FormatFloat(*res.RawTotal, 'f', 2, 64)
		}
		if res.NormalisedTotal != nil {
			normTotal = strconv.FormatFloat(*res.NormalisedTotal, 'f', 2, 64)
		}
		if res.TrackRank != nil {
			trackRank = strconv.Itoa(*res.TrackRank)
		}
		_ = cw.Write([]string{httpx.CSVSafe(res.TeamName), total, rank, strconv.FormatBool(res.Tied), strconv.Itoa(res.JudgesScored), rawTotal, normTotal, res.DecidingMode, httpx.CSVSafe(res.TrackName), httpx.CSVSafe(res.TrackPrize), trackRank, strconv.FormatBool(res.TrackWinner)})
	}
	cw.Flush()
}

// jsonFloat reads a JSON-decoded number (always float64 via encoding/json's
// map[string]any) or 0 for anything else (missing, null, wrong type).
func jsonFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

// scoringStage reports whether judges may score in the event's stage:
// from entries closed onward, or while building on the standing demo
// (including its weekly replacement slug) so visitors can try judging.
func (h *HackHandler) scoringStage(ctx context.Context, ev db.Event) bool {
	if ev.Stage == "closed" || ev.Stage == "judging" {
		return true
	}
	if h.demoSlug == "" || (ev.Slug != h.demoSlug && !strings.HasPrefix(ev.Slug, h.demoSlug+"-")) {
		return false
	}
	demo, err := db.GetDemoEvent(ctx, h.database, h.demoSlug)
	return err == nil && demo.ID == ev.ID
}

// liveResultSites maps team id to the public address of every team site that
// is live right now (the gallery's own query), so the public result routes
// (/results, /public) drop a card's site link once a site is taken down or
// goes offline after publish, without a republish -- as the event page does.
func (h *HackHandler) liveResultSites(ctx context.Context, ev db.Event) map[string]string {
	out := map[string]string{}
	if h.sites == nil {
		return out
	}
	cards, err := db.ListGalleryCards(ctx, h.database, ev.ID, ev.AccountID)
	if err != nil {
		log.Printf("hack: result sites %s: %v", ev.Slug, err)
		return out
	}
	for _, c := range cards {
		if link := hackGalleryLink(h.sites.TeamSiteURL, ev.Slug, c.Slug); link != "" {
			out[c.ID] = link
		}
	}
	return out
}

// publicResultEntry is a snapshot row's card for a public route: a copy with
// site_url set from the live sites, never the snapshot's own.
func publicResultEntry(entry any, teamID any, live map[string]string) any {
	m, ok := entry.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	id, _ := teamID.(string)
	out["site_url"] = live[id]
	return out
}
