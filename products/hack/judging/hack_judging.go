package judging

import (
	"log"
	"net/http"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Judging setup: the rubric, how judges are spread, conflicts, and the organiser's dashboard. The lock column is checked here so a locked event cannot have its rubric rewritten out from under stored scores.

func rubricJSON(items []model.RubricCriterion) map[string]any {
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
	return map[string]any{"criteria": out, "version": entryapp.RubricVersion(items)}
}

func (h *handler) getRubric(w http.ResponseWriter, r *http.Request) {
	// Teams can see the rubric: an approved participant reads the same criteria judges and organisers do -- names, descriptions, weights and max points, never a score.
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser", "judge", "participant")
	if !ok {
		return
	}
	items, err := h.Entries.Rubric(r.Context(), a.Event.ID)
	if err != nil {
		log.Printf("hack: rubric %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rubricJSON(items))
}

func (h *handler) putRubric(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	// Locked wins over a body that would also fail validation: the organiser's
	// next step is to move back from Results, not to repair the rubric.
	if a.Event.JudgingLockedAt.Valid {
		httpx.WriteError(w, http.StatusConflict, "scores_locked", "Judging is locked because the event is at Results. Move the event back to Entries closed to change the rubric.")
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
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	items, msg := entryapp.ValidateRubric(req.Criteria)
	if msg != "" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_rubric", msg)
		return
	}
	ids := make([]string, len(req.Criteria))
	for i, c := range req.Criteria {
		ids[i] = c.ID
	}
	saved, err := h.Entries.ReplaceRubric(r.Context(), a.Event.ID, items, ids, req.ConfirmScoreDeletion, a.User.ID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rubricJSON(saved))
}

func judgingSettingsJSON(ev model.Event) map[string]any {
	return map[string]any{
		"assignment_mode":  ev.JudgeAssignmentMode,
		"organiser_judges": ev.OrganiserJudges,
		"judges_per_team":  ev.JudgesPerTeam,
		"score_mode":       ev.ScoreMode,
		"tie_criterion_id": nullableString(ev.TieCriterionID),
		"public_scores":    ev.PublicScores,
		"public_ranks":     ev.PublicRanks,
	}
}

func (h *handler) getJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	h.writeJudgingSettings(w, r, a.Event)
}

func (h *handler) patchJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		OrganiserJudges   *[]string `json:"organiser_judges"`
		IncludeOrganisers *bool     `json:"include_organisers"`
		AssignmentMode    *string   `json:"assignment_mode"`
		JudgesPerTeam     *float64  `json:"judges_per_team"`
		ScoreMode         *string   `json:"score_mode"`
		TieCriterionID    *string   `json:"tie_criterion_id"`
		PublicScores      *bool     `json:"public_scores"`
		PublicRanks       *bool     `json:"public_ranks"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	ev, err := h.Entries.ChangeJudgingSettings(r.Context(), a.Event.ID, entryapp.JudgingSettingsInput{OrganiserJudges: req.OrganiserJudges, IncludeOrganisers: req.IncludeOrganisers, Mode: req.AssignmentMode, PerTeam: req.JudgesPerTeam, ScoreMode: req.ScoreMode, TieCriterionID: req.TieCriterionID, PublicScores: req.PublicScores, PublicRanks: req.PublicRanks})
	if err != nil {
		core.WriteError(w, err)
		return
	}
	h.writeJudgingSettings(w, r, ev)
}

func (h *handler) generateAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		IncludeOrganisers *bool `json:"include_organisers"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
			return
		}
	}
	// Read before the transaction: judgingPoolFor reads team sites and may
	// pin due teams in transactions of its own.
	allTeams, err := h.Entries.TeamsWithMembers(r.Context(), a.Event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	pool, err := h.Entries.JudgingPool(r.Context(), a.Event, allTeams)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	planned, under, err := h.Entries.GenerateAssignments(r.Context(), a.Event.ID, req.IncludeOrganisers, pool.In, false)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, assignmentJSON(planned, under))
}

func assignmentJSON(rows []model.JudgeAssignment, under []string) map[string]any {
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

func conflictJSON(c entryapp.EventConflict) map[string]any {
	return map[string]any{
		"judge_id":    c.JudgeID,
		"judge_name":  c.JudgeName,
		"team_id":     c.TeamID,
		"team_name":   c.TeamName,
		"declared_by": c.DeclaredBy,
		// had_score: whether the judge already has score rows for this team; the judge-queue undo dialog uses it instead of probing .../judge/scores/{team}, which 403s for a conflicted team.
		"had_score": c.HadScore,
	}
}

func (h *handler) listConflicts(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	judgeID := ""
	if a.Member.Role == "judge" || r.URL.Query().Get("mine") == "1" {
		judgeID = a.User.ID
	}
	rows, err := h.Entries.Conflicts(r.Context(), a.Event.ID, judgeID)
	if err != nil {
		log.Printf("hack: conflicts %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		out = append(out, conflictJSON(c))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"conflicts": out})
}

func (h *handler) declareConflict(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser", "judge")
	if !ok {
		return
	}
	if err := entryapp.JudgingLocked(a.Event); err != nil {
		core.WriteError(w, err)
		return
	}
	var req struct {
		TeamID      string `json:"team_id"`
		JudgeUserID string `json:"judge_user_id"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	saved, err := h.Entries.RecordConflict(r.Context(), a.Event.ID, conflictCaller(a), req.TeamID, req.JudgeUserID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, conflictJSON(saved))
}

func (h *handler) deleteConflict(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser", "judge")
	if !ok {
		return
	}
	judgeID := strings.TrimSpace(r.URL.Query().Get("judge_user_id"))
	if err := h.Entries.RemoveConflict(r.Context(), a.Event, conflictCaller(a), r.PathValue("team_id"), judgeID); err != nil {
		core.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func conflictCaller(a core.Access) entryapp.ConflictCaller {
	return entryapp.ConflictCaller{UserID: a.User.ID, Organiser: a.Member.Role == "organiser"}
}

func (h *handler) judgingDashboard(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	// TODO: this prospective pool includes unsettled entries and live sites; the queue requires judgeable entries.
	d, err := h.JudgingDashboard(r.Context(), a.Event)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, dashboardJSON(d))
}

func dashboardJSON(d Dashboard) map[string]any {
	teams := make([]map[string]any, 0, len(d.Teams))
	for _, t := range d.Teams {
		teams = append(teams, map[string]any{"team_id": t.ID, "team_name": t.Name, "judges_scored": t.Scored, "judges_eligible": t.Eligible, "flagged": t.Flagged})
	}
	judges := make([]map[string]any, 0, len(d.Judges))
	for _, j := range d.Judges {
		judges = append(judges, map[string]any{"judge_id": j.ID, "judge_name": j.Name, "done_count": j.Done, "assigned_count": j.Assigned})
	}
	former := make([]map[string]any, 0, len(d.Former))
	for _, f := range d.Former {
		former = append(former, map[string]any{"user_id": f.UserID, "display_name": f.DisplayName, "role": f.Role,
			"left_at": core.RFC3339Time(f.LeftAt), "counted_teams": f.Counted, "assigned_count": f.Assigned})
	}
	return map[string]any{
		"coverage": d.Coverage, "teams": teams, "judges": judges, "former_judges": former, "tied_groups": []any{},
		"entries": map[string]any{"judged": d.Pool.Judged, "no_entry": d.Pool.NoEntry, "excluded": d.Pool.Excluded},
	}
}

// deleteFormerJudgeScores drops a departed judge's scores so they stop counting (.../judging/former-judges/{user_id}).
func (h *handler) deleteFormerJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(userID) {
		httpx.WriteError(w, http.StatusNotFound, "former_judge_not_found", "former judge not found")
		return
	}
	// Destructive, like delete-event and leave's close:"delete": confirm must repeat the event's address name, so an agent or a stray retry can't drop a judge's scores without the person having agreed to exactly this event.
	var req struct {
		Confirm string `json:"confirm"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.Confirm != a.Event.Slug {
		httpx.WriteError(w, http.StatusConflict, "confirm_required", `to delete this judge's scores, send {"confirm":"`+a.Event.Slug+`"} with this event's address name`)
		return
	}
	err := h.Entries.DeleteFormerJudgeScores(r.Context(), a.Event.ID, a.User.ID, userID, a.Event.Slug)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) judgingLockRemoved(w http.ResponseWriter, r *http.Request) {
	if _, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser"); !ok {
		return
	}
	httpx.WriteError(w, http.StatusGone, "judging_lock_removed", "Judging locks when you finish judging (move to Results) and unlocks when you move the event back.")
}

func (h *handler) judgeQueue(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "judge", "organiser")
	if !ok {
		return
	}
	if a.Admin {
		core.WriteEventNotFound(w)
		return
	}
	ctx := r.Context()
	queue, err := h.Entries.JudgeCandidates(ctx, a.Event, a.User.ID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	tl, err := h.Links.LoadTeamList(ctx, a.Event)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	fields := entryapp.LoadRules(a.Event.EntryRules)
	tracks, err := h.Events.EventTracks(ctx, a.Event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	hasTracks := len(tracks) > 0
	mayLink, err := h.JudgeSiteLinkGate(ctx, a)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(queue))
	candidates := make([]map[string]any, 0, len(queue))
	for _, row := range queue {
		team := row.Team
		candidates = append(candidates, map[string]any{"team_id": team.ID, "team_name": team.Name, "conflicted": row.Conflicted})
		if row.Conflicted {
			continue
		}
		item, ierr := h.EntryListItem(ctx, a.Event, model.EventTeam{ID: team.ID, Name: team.Name, Slug: team.Slug}, row.Entry, fields, hasTracks, tl)
		if ierr != nil {
			log.Printf("hack: queue %s/%s: %v", a.Event.Slug, team.ID, ierr)
			httpx.WriteInternal(w)
			return
		}
		item["team_id"] = team.ID
		if !mayLink(team.ID) {
			core.RedactEntrySiteLinks(item)
		}
		item["done"] = row.Done
		item["scores_count"] = row.ScoresCount
		out = append(out, item)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queue": out, "candidates": candidates, "judge_first": a.Event.JudgeFirst, "assignment_mode": a.Event.JudgeAssignmentMode})
}

func scoresJSON(scores []entryapp.JudgeScore, comment string, complete *bool) map[string]any {
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

func (h *handler) getJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "judge", "organiser")
	if !ok {
		return
	}
	if a.Admin {
		core.WriteEventNotFound(w)
		return
	}
	teamID := r.PathValue("team")
	scores, comment, err := h.Entries.JudgeScores(r.Context(), a.Event.ID, a.User.ID, teamID)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scoresJSON(scores, comment, nil))
}

func (h *handler) putJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "judge", "organiser")
	if !ok {
		return
	}
	if a.Event.JudgingLockedAt.Valid {
		httpx.WriteError(w, http.StatusConflict, "scores_locked", "Judging is locked.")
		return
	}
	if !h.ScoringStage(r.Context(), a.Event) {
		httpx.WriteError(w, 409, "scoring_not_open", "Scoring opens when judging starts.")
		return
	}
	teamID := r.PathValue("team")
	if err := h.Entries.CheckScoreConflict(r.Context(), a.Event.ID, a.User.ID, teamID); err != nil {
		core.WriteError(w, err)
		return
	}
	var req struct {
		Scores  []entryapp.ScoreInput `json:"scores"`
		Comment *string               `json:"comment"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.Comment != nil {
		cleaned, err := h.Entries.JudgeComment(*req.Comment)
		if err != nil {
			core.WriteError(w, err)
			return
		}
		req.Comment = &cleaned
	}
	updated, comment, complete, err := h.Entries.PutJudgeScores(r.Context(), a.Event.ID, a.User.ID, teamID, req.Scores, req.Comment, h.ScoringStage)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scoresJSON(updated, comment, &complete))
}

func (h *handler) writeJudgingSettings(w http.ResponseWriter, r *http.Request, ev model.Event) {
	judges, err := h.Entries.EventJudges(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	rows := make([]map[string]any, 0, len(judges))
	for _, j := range judges {
		rows = append(rows, map[string]any{"user_id": j.ID, "name": j.Name, "organiser": j.Role == "organiser"})
	}
	out := judgingSettingsJSON(ev)
	out["judges"] = rows
	httpx.WriteJSON(w, http.StatusOK, out)
}
