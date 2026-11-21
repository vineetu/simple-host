package hackhttp

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

func paperHackTimes(r *http.Request) { paperRecorder(r.Context()).HackPhasesChanged(r.Context()) }

// Judging setup: the rubric, how judges are spread, conflicts, and the
// organiser's dashboard. Scoring, the lock itself, publishing and results
// are the next pass. The lock column is already checked here so a locked
// event cannot have its rubric rewritten out from under stored scores.

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

func (h *judgingHTTP) getRubric(w http.ResponseWriter, r *http.Request) {
	// Teams can see the rubric (2026-11-02, audit item 1): an approved participant
	// reads the same criteria judges and organisers do -- names, descriptions,
	// weights and max points, never a score.
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser", "judge", "participant")
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

func (h *judgingHTTP) putRubric(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	// Locked wins over a body that would also fail validation: the organiser's
	// next step is to move back from Results, not to repair the rubric.
	if a.event.JudgingLockedAt.Valid {
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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
	saved, err := h.entries.ReplaceRubric(r.Context(), a.event.ID, items, ids, req.ConfirmScoreDeletion, a.user.ID)
	if err != nil {
		writeHackError(w, err)
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

func (h *judgingHTTP) getJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	h.writeJudgingSettings(w, r, a.event)
}

func (h *judgingHTTP) patchJudgingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	ev, err := h.entries.ChangeJudgingSettings(r.Context(), a.event.ID, entryapp.JudgingSettingsInput{OrganiserJudges: req.OrganiserJudges, IncludeOrganisers: req.IncludeOrganisers, Mode: req.AssignmentMode, PerTeam: req.JudgesPerTeam, ScoreMode: req.ScoreMode, TieCriterionID: req.TieCriterionID, PublicScores: req.PublicScores, PublicRanks: req.PublicRanks})
	if err != nil {
		writeHackError(w, err)
		return
	}
	h.writeJudgingSettings(w, r, ev)
}

// poolSiteLive is siteLiveCheck in the shape the judging pool asks it.
func (h *siteLinks) poolSiteLive(ctx context.Context, ev model.Event) func(model.NamedTeam) (bool, error) {
	live := h.siteLiveCheck(ctx, ev)
	return func(t model.NamedTeam) (bool, error) { return live(t.ID, t.Slug) }
}

// siteLiveCheck reports whether a team's site is live right now, loading
// the event's team list once, on first use.
func (h *siteLinks) siteLiveCheck(ctx context.Context, ev model.Event) func(teamID, teamSlug string) (bool, error) {
	var tl *teamList
	return func(teamID, teamSlug string) (bool, error) {
		if tl == nil {
			loaded, err := h.loadTeamList(ctx, ev)
			if err != nil {
				return false, err
			}
			tl = &loaded
		}
		return entryapp.TeamSiteLive(tl.site(teamSlug), tl.states[teamID]), nil
	}
}

func (h *judgingHTTP) generateAssignments(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		IncludeOrganisers *bool `json:"include_organisers"`
	}
	if r.Body != nil && r.ContentLength != 0 {
		if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
			return
		}
	}
	// Read before the transaction: judgingPoolFor reads team sites and may
	// pin due teams in transactions of its own.
	allTeams, err := h.entries.TeamsWithMembers(r.Context(), a.event.ID)
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	pool, err := h.entries.JudgingPool(r.Context(), a.event, allTeams, h.links.poolSiteLive(r.Context(), a.event))
	if err != nil {
		log.Printf("hack: assignments %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	planned, under, err := h.entries.GenerateAssignments(r.Context(), a.event.ID, req.IncludeOrganisers, pool.In, false)
	if err != nil {
		writeHackError(w, err)
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
		// had_score (Codex review, Grok retest 3 Cosmetic #4): whether the
		// judge already has score rows for this team -- the judge-queue
		// undo dialog uses this directly instead of probing
		// .../judge/scores/{team}, which 403s for a conflicted team.
		"had_score": c.HadScore,
	}
}

func (h *judgingHTTP) listConflicts(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser", "judge")
	if !ok {
		return
	}
	judgeID := ""
	if a.member.Role == "judge" || r.URL.Query().Get("mine") == "1" {
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

func (h *judgingHTTP) declareConflict(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser", "judge")
	if !ok {
		return
	}
	if err := entryapp.JudgingLocked(a.event); err != nil {
		writeHackError(w, err)
		return
	}
	var req struct {
		TeamID      string `json:"team_id"`
		JudgeUserID string `json:"judge_user_id"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	saved, err := h.entries.RecordConflict(r.Context(), a.event.ID, conflictCaller(a), req.TeamID, req.JudgeUserID)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, conflictJSON(saved))
}

func (h *judgingHTTP) deleteConflict(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser", "judge")
	if !ok {
		return
	}
	judgeID := strings.TrimSpace(r.URL.Query().Get("judge_user_id"))
	if err := h.entries.RemoveConflict(r.Context(), a.event, conflictCaller(a), r.PathValue("team_id"), judgeID); err != nil {
		writeHackError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// conflictCaller is the caller of a conflict route.
func conflictCaller(a hackAccess) entryapp.ConflictCaller {
	return entryapp.ConflictCaller{UserID: a.user.ID, Organiser: a.member.Role == "organiser"}
}

func (h *judgingHTTP) judgingDashboard(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	// TODO(4.1): This prospective pool includes unsettled entries and live sites; the queue requires judgeable entries.
	d, err := h.entries.JudgingDashboard(r.Context(), a.event, h.links.poolSiteLive(r.Context(), a.event))
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, dashboardJSON(d))
}

// dashboardJSON is the judging dashboard's body.
func dashboardJSON(d entryapp.Dashboard) map[string]any {
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
			"left_at": rfc3339Time(f.LeftAt), "counted_teams": f.Counted, "assigned_count": f.Assigned})
	}
	return map[string]any{
		"coverage": d.Coverage, "teams": teams, "judges": judges, "former_judges": former, "tied_groups": []any{},
		"entries": map[string]any{"judged": d.Pool.Judged, "no_entry": d.Pool.NoEntry, "excluded": d.Pool.Excluded},
	}
}

// deleteFormerJudgeScores handles removal at
// .../judging/former-judges/{user_id} (SIM-340): an organiser dropping a
// departed judge's scores so they stop counting.
func (h *judgingHTTP) deleteFormerJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if req.Confirm != a.event.Slug {
		httpx.WriteError(w, http.StatusConflict, "confirm_required", `to delete this judge's scores, send {"confirm":"`+a.event.Slug+`"} with this event's address name`)
		return
	}
	err := h.entries.DeleteFormerJudgeScores(r.Context(), a.event.ID, a.user.ID, userID, a.event.Slug)
	if err != nil {
		writeHackError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- M3 pass 2: scoring, lock/unlock, publish, results, exports ----------

func (h *judgingHTTP) judgingLockRemoved(w http.ResponseWriter, r *http.Request) {
	if _, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser"); !ok {
		return
	}
	httpx.WriteError(w, http.StatusGone, "judging_lock_removed", "Judging locks when you finish judging (move to Results) and unlocks when you move the event back.")
}

func (h *judgingHTTP) judgeQueue(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "judge", "organiser")
	if !ok {
		return
	}
	if a.admin {
		writeEventNotFound(w)
		return
	}
	ctx := r.Context()
	var tl teamList
	queue, err := h.entries.JudgeCandidates(ctx, a.event, a.user.ID, func() error {
		var err error
		tl, err = h.links.loadTeamList(ctx, a.event)
		return err
	})
	if err != nil {
		writeHackError(w, err)
		return
	}
	fields := entryapp.LoadRules(a.event.EntryRules)
	tracks, err := h.events.EventTracks(ctx, a.event.ID)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	hasTracks := len(tracks) > 0
	mayLink, err := h.views.judgeSiteLinkGate(ctx, a)
	if err != nil {
		log.Printf("hack: queue %s: %v", a.event.Slug, err)
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
		item, ierr := h.views.entryListItem(ctx, a.event, model.EventTeam{ID: team.ID, Name: team.Name, Slug: team.Slug}, row.Entry, fields, hasTracks, tl)
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
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queue": out, "candidates": candidates, "judge_first": a.event.JudgeFirst, "assignment_mode": a.event.JudgeAssignmentMode})
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

func (h *judgingHTTP) getJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "judge", "organiser")
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
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scoresJSON(scores, comment, nil))
}

func (h *judgingHTTP) putJudgeScores(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "judge", "organiser")
	if !ok {
		return
	}
	if a.event.JudgingLockedAt.Valid {
		httpx.WriteError(w, http.StatusConflict, "scores_locked", "Judging is locked.")
		return
	}
	if !h.views.scoringStage(r.Context(), a.event) {
		httpx.WriteError(w, 409, "scoring_not_open", "Scoring opens when judging starts.")
		return
	}
	teamID := r.PathValue("team")
	if err := h.entries.CheckScoreConflict(r.Context(), a.event.ID, a.user.ID, teamID); err != nil {
		writeHackError(w, err)
		return
	}
	var req struct {
		Scores  []entryapp.ScoreInput `json:"scores"`
		Comment *string               `json:"comment"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if req.Comment != nil {
		cleaned, err := h.entries.JudgeComment(*req.Comment)
		if err != nil {
			writeHackError(w, err)
			return
		}
		req.Comment = &cleaned
	}
	updated, comment, complete, err := h.entries.PutJudgeScores(r.Context(), a.event.ID, a.user.ID, teamID, req.Scores, req.Comment, h.views.scoringStage)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, scoresJSON(updated, comment, &complete))
}

// scoringStage reports whether judges may score in the event's stage:
// during Judging, or while building on the standing demo
// (including its weekly replacement slug) so visitors can try judging.
func (h *views) scoringStage(ctx context.Context, ev model.Event) bool {
	if ev.Stage == "judging" {
		return true
	}
	if h.demoSlug == "" || (ev.Slug != h.demoSlug && !strings.HasPrefix(ev.Slug, h.demoSlug+"-")) {
		return false
	}
	demo, err := h.events.DemoEvent(ctx, h.demoSlug)
	return err == nil && demo.ID == ev.ID
}

func (h *judgingHTTP) writeJudgingSettings(w http.ResponseWriter, r *http.Request, ev model.Event) {
	judges, err := h.entries.EventJudges(r.Context(), ev.ID)
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
