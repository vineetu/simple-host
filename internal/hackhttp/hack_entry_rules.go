package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"

	entries "github.com/vsriram/simple-host/internal/app/hack/entries"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// Entry rules, slice 1 of the entries and participant journey package
// (build-plan.md). This file is the one place that answers "is this entry
// complete" (section 3), and the settle sweep that runs it against the
// deadline.
//
// Coordinator decision, 2026-11-02 (recorded in INTENT.md, the owner may
// overrule it): at the deadline an incomplete draft is still judged, with an
// "Incomplete" flag organisers and judges see, rather than dropped from
// judging. Organisers can exclude a flagged entry by hand on the Entries
// screen, with a confirm. This replaces the build plan's "an incomplete
// draft is not submitted and is not judged" rule for this slice.

// writeHackErrMissing is writeHackErr with a "missing" array attached, for
// the entry refusals a client maps to field errors (section 4.1).
func writeHackErrMissing(w http.ResponseWriter, status int, code, msg string, missing []string) {
	if missing == nil {
		missing = []string{}
	}
	httpx.WriteJSON(w, status, map[string]any{"error": msg, "code": code, "missing": missing})
}

func (h *HackHandler) evaluateEntryTx(ctx context.Context, tx *sql.Tx, ev model.Event, st model.TeamWriteState, teamID string, e model.EventEntry) (entries.Check, error) {
	return h.entries.EvaluateEntryTx(ctx, tx, ev, st, teamID, e)
}

// entrySiteState reads the team's site once for the entry view: whether it
// exists at all, its live version (nil when it has none), and whether it
// counts as the "site" rules field -- a live version, not taken down
// (section 4.1). siteTakenDown already covers both the organiser's
// take-down and a platform suspension (model.TeamWriteState.SiteTakenDown).
func (h *HackHandler) entrySiteState(ctx context.Context, ev model.Event, teamSlug string, siteTakenDown bool) (exists bool, liveVersion any, live bool) {
	if h.sites == nil {
		return false, nil, false
	}
	info, _, err := h.sites.TeamSiteInfo(ctx, ev.AccountID, teamSlug)
	if err != nil || !info.Exists {
		return false, nil, false
	}
	if info.ActiveVersion > 0 {
		liveVersion = info.ActiveVersion
	}
	live = info.ActiveVersion > 0 && !info.Suspended && !siteTakenDown
	return true, liveVersion, live
}

// entrySiteLive is entrySiteState's third return, for callers (the sweep,
// the rules dry run) that only need whether the site counts as filled.
func (h *HackHandler) entrySiteLive(ctx context.Context, ev model.Event, teamSlug string, siteTakenDown bool) bool {
	_, _, live := h.entrySiteState(ctx, ev, teamSlug, siteTakenDown)
	return live
}

// entryRulesView is GET/PUT .../entry-rules's body (section 4.2).
func entryRulesView(ev model.Event, hasTracks, editable bool) map[string]any {
	fields := entries.LoadRules(ev.EntryRules)
	return map[string]any{
		"preset":      ev.EntryPreset,
		"fields":      fields,
		"judge_first": ev.JudgeFirst,
		"summary":     entries.RulesSummary(fields),
		"has_tracks":  hasTracks,
		"editable":    editable,
	}
}

func (h *HackHandler) getEntryRules(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	tracks, err := h.eventsSvc().EventTracks(r.Context(), a.event.ID)
	if err != nil {
		log.Printf("hack: entry rules %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, entryRulesView(a.event, len(tracks) > 0, entries.RulesEditable(a.event.Stage)))
}

func (h *HackHandler) putEntryRules(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Preset     *string           `json:"preset"`
		Fields     map[string]string `json:"fields"`
		JudgeFirst *string           `json:"judge_first"`
		DryRun     bool              `json:"dry_run"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Preset != nil && !entries.PresetKnown(*req.Preset) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_preset", "preset must be build, code, ideas or custom")
		return
	}
	if req.JudgeFirst != nil && *req.JudgeFirst != "video" && *req.JudgeFirst != "site" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_judge_first", "judge_first must be video or site")
		return
	}
	result, err := h.entries.PutRules(r.Context(), a.event.ID, entries.RulesUpdate{Preset: req.Preset, Fields: req.Fields, JudgeFirst: req.JudgeFirst, DryRun: req.DryRun})
	if err != nil {
		var domainErr *entries.Error
		if errors.As(err, &domainErr) {
			status := http.StatusBadRequest
			if domainErr.Code == "entry_rules_closed" {
				status = http.StatusConflict
			}
			httpx.WriteError(w, status, domainErr.Code, domainErr.Message)
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	if req.DryRun {
		teams := make([]map[string]any, 0, len(result.Impact.Teams))
		for _, t := range result.Impact.Teams {
			teams = append(teams, map[string]any{"id": t.ID, "name": t.Name, "missing": t.Missing})
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"impact": map[string]any{"submitted_missing": result.Impact.SubmittedMissing, "drafts_newly_blocked": result.Impact.DraftsNewlyBlocked, "teams": teams}, "preview": map[string]any{"preset": result.Preset, "fields": result.Fields, "judge_first": result.JudgeFirst, "summary": entries.RulesSummary(result.Fields), "has_tracks": result.HasTracks}})
	} else {
		httpx.WriteJSON(w, http.StatusOK, entryRulesView(result.Updated, result.HasTracks, entries.RulesEditable(result.Updated.Stage)))
	}
}

func (h *HackHandler) entryProgressJSON(ctx context.Context, ev model.Event, team model.EventTeam, teammates []string, st model.TeamWriteState) (map[string]any, error) {
	p, err := h.entries.EntryProgress(ctx, ev, team, teammates, st)
	if err != nil {
		return nil, err
	}
	e := p.Entry
	steps := []map[string]any{
		{"id": "team", "done": true, "detail": p.TeamLine},
		{"id": "summary", "done": p.Filled >= p.Of, "filled": p.Filled, "of": p.Of, "missing": p.SummaryMissing},
		{"id": "evidence", "done": p.EvidenceDone, "summary": p.Summary, "missing": p.EvidenceMissing},
		{"id": "submit", "done": e.Status() == "submitted", "status": e.Status(), "submitted_at": rfc3339UTC(e.SubmittedAt), "auto": e.SubmitAuto},
	}
	return map[string]any{"steps": steps, "next": p.Next, "deadline": rfc3339UTC(st.Deadline), "deadline_is_extension": st.HasOverride, "event_deadline": rfc3339UTC(st.EventDeadline), "server_now": rfc3339Time(p.Now)}, nil
}

// SettleDueEntries runs the deadline sweep for every event.
func (h *HackHandler) SettleDueEntries(ctx context.Context) {
	h.entries.SettleDue(ctx, siteSweepReport{ctx: ctx})
}
