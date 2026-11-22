package entries

import (
	"log"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	entries "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Entry rules: this file is the one place that answers "is this entry complete", plus the settle sweep that runs it against the deadline.
// At the deadline an incomplete draft is still judged, with an "Incomplete" flag organisers and judges see, rather than dropped from judging. Organisers can exclude a flagged entry by hand on the Entries screen, with a confirm.

// writeHackErrMissing is writeHackErr with a "missing" array attached, for entry refusals a client maps to field errors.
func writeHackErrMissing(w http.ResponseWriter, status int, code, msg string, missing []string) {
	if missing == nil {
		missing = []string{}
	}
	httpx.WriteJSON(w, status, map[string]any{"error": msg, "code": code, "missing": missing})
}

// entryRulesView is GET/PUT .../entry-rules's body.
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

func (h *handler) getEntryRules(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	tracks, err := h.Events.EventTracks(r.Context(), a.Event.ID)
	if err != nil {
		log.Printf("hack: entry rules %s: %v", a.Event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, entryRulesView(a.Event, len(tracks) > 0, entries.RulesEditable(a.Event.Stage)))
}

func (h *handler) putEntryRules(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Preset     *string           `json:"preset"`
		Fields     map[string]string `json:"fields"`
		JudgeFirst *string           `json:"judge_first"`
		DryRun     bool              `json:"dry_run"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.Preset != nil && !entries.PresetSettable(*req.Preset) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_preset", "preset must be video_or_website or video_website_code")
		return
	}
	if req.JudgeFirst != nil && *req.JudgeFirst != "video" && *req.JudgeFirst != "site" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_judge_first", "judge_first must be video or site")
		return
	}
	result, err := h.Entries.PutRules(r.Context(), a.Event.ID, entries.RulesUpdate{Preset: req.Preset, Fields: req.Fields, JudgeFirst: req.JudgeFirst, DryRun: req.DryRun})
	if err != nil {
		core.WriteError(w, err)
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
