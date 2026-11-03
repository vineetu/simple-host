package hackhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
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

// entryRuleFields are every field the rules engine understands, in canonical
// order. missing ids, the CSV "missing" cell and the organiser's rule rows
// all follow this order.
var entryRuleFields = []string{
	"title", "problem", "tagline", "audience", "track", "tools", "maturity",
	"description", "video_url", "site", "code_url", "screenshot",
}

// entryOneOfFields are the only fields a rules JSON may mark "one_of".
var entryOneOfFields = map[string]bool{"video_url": true, "site": true, "code_url": true, "screenshot": true}

var entryRuleModeOK = map[string]bool{"required": true, "optional": true, "off": true, "one_of": true}

// EntryRules is events.entry_rules, version 1.
type EntryRules struct {
	V      int               `json:"v"`
	Fields map[string]string `json:"fields"`
}

// entryPresets is the Go table PUT .../entry-rules {preset} expands, and
// what a newly created event starts on ("build"). The atlas
// (docs/ux-atlas/spec.json, organiser-entries, "What do the presets set?")
// is the spec:
//   - AI build: everything on the card Required, Video and Website One of,
//     Code Off, Screenshot Optional, video first.
//   - Coding: the card Required except Who it's for, Code link Required,
//     Video and Website One of, Screenshot Optional, website first.
//   - Ideas and design: Problem, Solution, Who it's for and Track Required,
//     Tools and Works today Optional, Video, Website and Screenshot One of,
//     Code Off, video first.
//
// Description is Optional in all three. Title is always Required, and
// Track is ignored when the event has no tracks.
var entryPresets = map[string]map[string]string{
	"build": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "required", "track": "required", "tools": "required", "maturity": "required",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "off", "screenshot": "optional",
	},
	"code": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "optional", "track": "required", "tools": "required", "maturity": "required",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "required", "screenshot": "optional",
	},
	"ideas": {
		"title": "required", "problem": "required", "tagline": "required",
		"audience": "required", "track": "required", "tools": "optional", "maturity": "optional",
		"description": "optional", "video_url": "one_of", "site": "one_of", "code_url": "off", "screenshot": "one_of",
	},
}

// entryPresetJudgeFirst is which evidence judges see first for each preset,
// applied when an organiser picks the preset without naming judge_first.
var entryPresetJudgeFirst = map[string]string{"build": "video", "code": "site", "ideas": "video"}

// entryPresetOrder is the presets an organiser can pick, in display order.
var entryPresetOrder = []string{"build", "code", "ideas"}

func entryPresetKnown(preset string) bool {
	_, ok := entryPresets[preset]
	return ok || preset == "custom"
}

// expandEntryPreset is entryPresets[preset] normalised to every field, title
// forced required. An unknown preset (including "custom") is every field
// optional, title required -- the caller then applies its own fields on top.
func expandEntryPreset(preset string) map[string]string {
	return normaliseEntryRules(entryPresets[preset])
}

// normaliseEntryRules fills every field (a missing key means "optional"),
// drops an unknown field or an unknown mode, drops "one_of" on a field that
// may not carry it, and forces title to "required". entry_rules JSON is
// validated server-side and never stored as the client sent it (section
// 9.6): this is the one function that enforces that.
func normaliseEntryRules(fields map[string]string) map[string]string {
	out := make(map[string]string, len(entryRuleFields))
	for _, f := range entryRuleFields {
		mode := "optional"
		if fields != nil {
			if m, ok := fields[f]; ok && entryRuleModeOK[m] && (m != "one_of" || entryOneOfFields[f]) {
				mode = m
			}
		}
		out[f] = mode
	}
	out["title"] = "required"
	return out
}

// marshalEntryRules is the normalised fields as entry_rules JSON (version 1).
func marshalEntryRules(fields map[string]string) []byte {
	raw, err := json.Marshal(EntryRules{V: 1, Fields: normaliseEntryRules(fields)})
	if err != nil {
		// Fields are plain strings; this cannot fail in practice.
		return []byte(`{"v":1,"fields":{}}`)
	}
	return raw
}

// loadEntryRules parses an event's stored entry_rules, defaulting to every
// field optional (title required) on a blank value -- a fresh row before a
// migration backfill runs, or on an instance that predates this release.
func loadEntryRules(raw []byte) map[string]string {
	var r EntryRules
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &r)
	}
	return normaliseEntryRules(r.Fields)
}

// entryOneOfGroup is the fields marked "one_of", in canonical order. Exactly
// one such group can exist (every field in entryOneOfFields marked one_of
// together makes the group); this just reads it back.
func entryOneOfGroup(fields map[string]string) []string {
	var out []string
	for _, f := range entryRuleFields {
		if entryOneOfFields[f] && fields[f] == "one_of" {
			out = append(out, f)
		}
	}
	return out
}

// EntryCheck is whether an entry is complete, and canonically-ordered reasons
// why not: field ids, plus "one_of" for an unmet one-of group, at the end.
type EntryCheck struct {
	Complete bool
	Missing  []string
}

// entryFieldPresent is whether one field of the entry/team counts as filled,
// for completeness. site comes from siteLive (the team's actual site state),
// never from the entry row (section 4.1: "the website evidence comes from
// sites, never from the entry row"). track comes from teamTrack, the team's
// chosen track, not anything stored on the entry.
func entryFieldPresent(field string, e db.EventEntry, siteLive bool, teamTrack *db.EventTrack) bool {
	switch field {
	case "title":
		return e.Title != ""
	case "problem":
		return e.Problem != ""
	case "tagline":
		return e.Tagline != ""
	case "audience":
		return e.Audience != ""
	case "track":
		return teamTrack != nil
	case "tools":
		return len(e.Tools) > 0
	case "maturity":
		return e.Maturity != ""
	case "description":
		return e.Description != ""
	case "video_url":
		return e.VideoURL != ""
	case "site":
		return siteLive
	case "code_url":
		return e.CodeURL != ""
	case "screenshot":
		return e.HasScreenshot
	default:
		return false
	}
}

// evaluateEntry is the only place that answers "is this entry complete"
// (section 3): entryCompleteness, queueReceipt, the CSV, listEntries, the
// judge queue, the Home progress and the settle sweep all go through this.
// hasTracks false ignores the "track" field even when the rules ask for it
// ("with no tracks in the event, track is ignored").
func evaluateEntry(fields map[string]string, e db.EventEntry, siteLive, hasTracks bool, teamTrack *db.EventTrack) EntryCheck {
	missing := []string{} // never null in JSON
	for _, f := range entryRuleFields {
		if f == "track" && !hasTracks {
			continue
		}
		if fields[f] == "required" && !entryFieldPresent(f, e, siteLive, teamTrack) {
			missing = append(missing, f)
		}
	}
	if group := entryOneOfGroup(fields); len(group) > 0 {
		met := false
		for _, f := range group {
			if entryFieldPresent(f, e, siteLive, teamTrack) {
				met = true
				break
			}
		}
		if !met {
			missing = append(missing, "one_of")
		}
	}
	return EntryCheck{Complete: len(missing) == 0, Missing: missing}
}

// entryOneOfWord is how a one-of field reads in the rules sentence.
// codeNamed is true once code_url is already named as required, which reads
// "your website" there instead of "a published website" alone.
func entryOneOfWord(field string, codeNamed bool) string {
	switch field {
	case "video_url":
		return "a video"
	case "site":
		if codeNamed {
			return "your website"
		}
		return "a published website"
	case "code_url":
		return "your code"
	case "screenshot":
		return "a screenshot"
	default:
		return field
	}
}

// entryRulesSummary is the one sentence naming what "show your work" needs
// ("a video or a published website", or "your code link, and a video or
// your website"), built server-side so the website and the AI apps word it
// identically (section 3). It only describes the show-your-work fields
// (code_url and the one-of group); the summary-card fields (problem,
// audience, track, tools, maturity) are not part of it.
func entryRulesSummary(fields map[string]string) string {
	var parts []string
	codeNamed := fields["code_url"] == "required"
	if codeNamed {
		parts = append(parts, "your code link")
	}
	if group := entryOneOfGroup(fields); len(group) > 0 {
		words := make([]string, 0, len(group))
		for _, f := range group {
			words = append(words, entryOneOfWord(f, codeNamed))
		}
		parts = append(parts, strings.Join(words, " or "))
	}
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
	}
}

// entrySummaryFields are the summary-card lines progress.steps["summary"]
// counts: problem, tagline, audience, track, tools, maturity, plus title
// when it is empty (section 4.3).
var entrySummaryFields = []string{"title", "problem", "tagline", "audience", "track", "tools", "maturity"}

// entryFieldWords names a field in plain words, for the "missing" list a
// client renders and for progress.steps["summary"].missing. one_of itself
// is reworded per-event by entryMissingMessage (the actual group members),
// this is only the generic fallback.
var entryFieldWords = map[string]string{
	"title": "a title", "problem": "the problem it solves", "tagline": "the one-line solution",
	"audience": "who it's for", "track": "a track", "tools": "the tools used",
	"maturity": "how far along it is", "description": "a description",
	"video_url": "a video", "site": "a published website", "code_url": "your code link",
	"screenshot": "a screenshot", "one_of": "a video or a published website",
}

// entryMissingMessage renders a missing list (field ids, plus "one_of") as
// one plain-English clause: "who it's for, and a video or a published
// website". "one_of" names the event's own one-of group.
func entryMissingMessage(missing []string, fields map[string]string) string {
	words := make([]string, 0, len(missing))
	for _, m := range missing {
		if m == "one_of" {
			if group := entryOneOfGroup(fields); len(group) > 0 {
				parts := make([]string, 0, len(group))
				for _, f := range group {
					parts = append(parts, entryOneOfWord(f, false))
				}
				words = append(words, strings.Join(parts, " or "))
				continue
			}
		}
		if w, ok := entryFieldWords[m]; ok {
			words = append(words, w)
		} else {
			words = append(words, m)
		}
	}
	switch len(words) {
	case 0:
		return ""
	case 1:
		return words[0]
	case 2:
		return words[0] + " and " + words[1]
	default:
		return strings.Join(words[:len(words)-1], ", ") + ", and " + words[len(words)-1]
	}
}

// writeHackErrMissing is writeHackErr with a "missing" array attached, for
// the entry refusals a client maps to field errors (section 4.1).
func writeHackErrMissing(w http.ResponseWriter, status int, code, msg string, missing []string) {
	if missing == nil {
		missing = []string{}
	}
	httpx.WriteJSON(w, status, map[string]any{"error": msg, "code": code, "missing": missing})
}

// evaluateEntryTx is evaluateEntry with every piece read fresh inside tx:
// the rules, the team's track and the site's live state.
func (h *HackHandler) evaluateEntryTx(ctx context.Context, tx *sql.Tx, ev db.Event, st db.TeamWriteState, teamID string, e db.EventEntry) (EntryCheck, error) {
	fields := loadEntryRules(ev.EntryRules)
	tracks, err := db.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return EntryCheck{}, err
	}
	track, err := db.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return EntryCheck{}, err
	}
	siteLive := h.entrySiteLive(ctx, ev, st.TeamSlug, st.SiteTakenDown)
	return evaluateEntry(fields, e, siteLive, len(tracks) > 0, track), nil
}

// keepSubmittedComplete re-checks a previously-submitted entry after a
// write (section 4.1: "a save on a submitted entry is evaluated after the
// change"). A no-op when the entry was not submitted before this write.
func (h *HackHandler) keepSubmittedComplete(ctx context.Context, tx *sql.Tx, ev db.Event, st db.TeamWriteState, teamID string, wasSubmitted bool) error {
	if !wasSubmitted {
		return nil
	}
	after, err := db.GetEventEntry(ctx, tx, teamID)
	if err != nil {
		return err
	}
	check, err := h.evaluateEntryTx(ctx, tx, ev, st, teamID, after)
	if err != nil {
		return err
	}
	if !check.Complete {
		return &entryWouldBeIncompleteError{Missing: check.Missing}
	}
	return nil
}

// entrySiteState reads the team's site once for the entry view: whether it
// exists at all, its live version (nil when it has none), and whether it
// counts as the "site" rules field -- a live version, not taken down
// (section 4.1). siteTakenDown already covers both the organiser's
// take-down and a platform suspension (db.TeamWriteState.SiteTakenDown).
func (h *HackHandler) entrySiteState(ctx context.Context, ev db.Event, teamSlug string, siteTakenDown bool) (exists bool, liveVersion any, live bool) {
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
func (h *HackHandler) entrySiteLive(ctx context.Context, ev db.Event, teamSlug string, siteTakenDown bool) bool {
	_, _, live := h.entrySiteState(ctx, ev, teamSlug, siteTakenDown)
	return live
}

// entryRulesView is GET/PUT .../entry-rules's body (section 4.2).
func entryRulesView(ev db.Event, hasTracks, editable bool) map[string]any {
	fields := loadEntryRules(ev.EntryRules)
	return map[string]any{
		"preset":      ev.EntryPreset,
		"fields":      fields,
		"judge_first": ev.JudgeFirst,
		"summary":     entryRulesSummary(fields),
		"has_tracks":  hasTracks,
		"editable":    editable,
	}
}

func (h *HackHandler) getEntryRules(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	tracks, err := db.ListEventTracks(r.Context(), h.database, a.event.ID)
	if err != nil {
		log.Printf("hack: entry rules %s: %v", a.event.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, entryRulesView(a.event, len(tracks) > 0, entryRulesEditable(a.event.Stage)))
}

// entryRulesEditable: rules may change from draft through building. From
// closed onwards they are read-only (409 entry_rules_closed), since judging
// has to see a fixed definition of complete.
func entryRulesEditable(stage string) bool {
	return stage == "draft" || stage == "open" || stage == "building"
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
	if req.Preset != nil && !entryPresetKnown(*req.Preset) {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_preset", "preset must be build, code, ideas or custom")
		return
	}
	if req.JudgeFirst != nil && *req.JudgeFirst != "video" && *req.JudgeFirst != "site" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_judge_first", "judge_first must be video or site")
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
	if !entryRulesEditable(ev.Stage) {
		httpx.WriteError(w, http.StatusConflict, "entry_rules_closed", "entry rules are fixed once entries close")
		return
	}
	preset := ev.EntryPreset
	fields := loadEntryRules(ev.EntryRules)
	if req.Preset != nil {
		preset = *req.Preset
		fields = expandEntryPreset(preset)
	}
	if req.Fields != nil {
		for k, v := range req.Fields {
			known := false
			for _, f := range entryRuleFields {
				if f == k {
					known = true
					break
				}
			}
			if !known {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_entry_field", "fields keys must be one of: "+strings.Join(entryRuleFields, ", "))
				return
			}
			if !entryRuleModeOK[v] || (v == "one_of" && !entryOneOfFields[k]) {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_entry_field_mode", k+" must be required, optional or off"+modeSuffix(k))
				return
			}
			fields[k] = v
		}
		fields = normaliseEntryRules(fields)
		// A field sent that differs from the preset's own table demotes the
		// stored preset to custom (section 4.2): the saved rules are no
		// longer exactly that preset's table.
		if preset != "custom" && !fieldsEqual(fields, expandEntryPreset(preset)) {
			preset = "custom"
		}
	}
	fields = normaliseEntryRules(fields)
	judgeFirst := ev.JudgeFirst
	if req.Preset != nil {
		if jf, ok := entryPresetJudgeFirst[*req.Preset]; ok {
			judgeFirst = jf
		}
	}
	if req.JudgeFirst != nil {
		judgeFirst = *req.JudgeFirst
	}
	if req.DryRun {
		impact, err := h.entryRulesImpact(r.Context(), tx, ev, fields)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		tracks, err := db.ListEventTracks(r.Context(), tx, a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"impact": impact, "preview": map[string]any{
			"preset": preset, "fields": fields, "judge_first": judgeFirst, "summary": entryRulesSummary(fields), "has_tracks": len(tracks) > 0,
		}})
		return
	}
	rulesJSON := marshalEntryRules(fields)
	legacyRequired := []string{}
	for _, f := range hackEntryFields {
		if fields[f] == "required" {
			legacyRequired = append(legacyRequired, f)
		}
	}
	updated, err := db.UpdateEventEntryRules(r.Context(), tx, a.event.ID, preset, rulesJSON, judgeFirst, legacyRequired)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// Tightening never un-submits (section 12): nothing here touches
	// submitted_at. A team newly short of the tightened rules just shows
	// "Incomplete" if it later unsubmits and the deadline passes again.
	if err := tx.Commit(); err != nil {
		httpx.WriteInternal(w)
		return
	}
	tracks, _ := db.ListEventTracks(r.Context(), h.database, updated.ID)
	httpx.WriteJSON(w, http.StatusOK, entryRulesView(updated, len(tracks) > 0, entryRulesEditable(updated.Stage)))
}

func modeSuffix(field string) string {
	if entryOneOfFields[field] {
		return " or one_of"
	}
	return ""
}

func fieldsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// entryRulesImpact is the dry run's counts (section 4.2): how many already-
// submitted entries would newly read as missing something under the
// proposed fields (they stay submitted regardless -- this is
// informational), and how many drafts would newly need more before they can
// submit. "Newly" means the proposed rules ask for a line the current rules
// don't: an entry already short of the current rules in exactly the same
// way is not counted again, so loosening or re-saving unchanged rules
// reports no impact.
func (h *HackHandler) entryRulesImpact(ctx context.Context, tx *sql.Tx, ev db.Event, fields map[string]string) (map[string]any, error) {
	current := loadEntryRules(ev.EntryRules)
	teams, err := db.ListEventTeams(ctx, tx, ev.ID)
	if err != nil {
		return nil, err
	}
	entries, err := db.ListEventEntries(ctx, tx, ev.ID)
	if err != nil {
		return nil, err
	}
	tracks, err := db.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return nil, err
	}
	hasTracks := len(tracks) > 0
	byTeam := map[string]db.EventEntry{}
	for _, e := range entries {
		byTeam[e.TeamID] = e
	}
	submittedMissing, draftsNewlyBlocked := 0, 0
	type impactTeam struct {
		ID, Name string
		Missing  []string
	}
	var affected []impactTeam
	for _, team := range teams {
		e, ok := byTeam[team.ID]
		if !ok {
			continue
		}
		track, err := db.TeamTrack(ctx, tx, team.ID)
		if err != nil {
			return nil, err
		}
		siteLive := h.entrySiteLive(ctx, ev, team.Slug, team.SiteTakenDownAt.Valid)
		check := evaluateEntry(fields, e, siteLive, hasTracks, track)
		if check.Complete {
			continue
		}
		before := evaluateEntry(current, e, siteLive, hasTracks, track)
		was := map[string]bool{}
		for _, m := range before.Missing {
			was[m] = true
		}
		newly := false
		for _, m := range check.Missing {
			if !was[m] {
				newly = true
				break
			}
		}
		// A draft counts only when these rules newly block it: it could be
		// submitted under the current rules and no longer can. A draft that
		// was already incomplete is not newly blocked, even if it now misses
		// one more line.
		if !newly || (!e.SubmittedAt.Valid && !before.Complete) {
			continue
		}
		if e.SubmittedAt.Valid {
			submittedMissing++
		} else {
			draftsNewlyBlocked++
		}
		affected = append(affected, impactTeam{ID: team.ID, Name: team.Name, Missing: check.Missing})
	}
	out := make([]map[string]any, 0, len(affected))
	for _, t := range affected {
		out = append(out, map[string]any{"id": t.ID, "name": t.Name, "missing": t.Missing})
	}
	return map[string]any{"submitted_missing": submittedMissing, "drafts_newly_blocked": draftsNewlyBlocked, "teams": out}, nil
}

// entryProgressJSON is the participant Home checklist (section 4.3): four
// steps (team, summary, evidence, submit), the next one to do, and the
// deadline, so the website and the AI apps compute it identically.
func (h *HackHandler) entryProgressJSON(ctx context.Context, ev db.Event, team db.EventTeam, teammates []string, st db.TeamWriteState) (map[string]any, error) {
	e, err := db.GetEventEntry(ctx, h.database, team.ID)
	if errors.Is(err, sql.ErrNoRows) {
		e = db.EventEntry{}
	} else if err != nil {
		return nil, err
	}
	fields := loadEntryRules(ev.EntryRules)
	tracks, err := db.ListEventTracks(ctx, h.database, ev.ID)
	if err != nil {
		return nil, err
	}
	hasTracks := len(tracks) > 0
	track, err := db.TeamTrack(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	siteLive := h.entrySiteLive(ctx, ev, team.Slug, st.SiteTakenDown)

	filled, of, summaryMissing := 0, 0, []string{}
	for _, f := range entrySummaryFields {
		if f == "track" && !hasTracks {
			continue
		}
		if fields[f] == "off" {
			continue
		}
		// The project name always counts (full-test fix, 2026-11-02): it
		// used to count only while empty, so "1 of 7 filled" became "6 of
		// 6" once it was typed, and Home and Entry disagreed mid-edit.
		of++
		if entryFieldPresent(f, e, siteLive, track) {
			filled++
		} else {
			summaryMissing = append(summaryMissing, f)
		}
	}
	evidenceCheck := evaluateEntry(fields, e, siteLive, hasTracks, track)
	evidenceMissing := []string{}
	for _, m := range evidenceCheck.Missing {
		if m == "one_of" || m == "code_url" {
			evidenceMissing = append(evidenceMissing, m)
		}
	}
	evidenceDone := len(evidenceMissing) == 0

	teamLine := "On " + team.Name
	if len(teammates) > 0 {
		teamLine += " with " + joinNames(teammates)
	}
	steps := []map[string]any{
		{"id": "team", "done": true, "detail": teamLine},
		{"id": "summary", "done": filled >= of, "filled": filled, "of": of, "missing": summaryMissing},
		{"id": "evidence", "done": evidenceDone, "summary": entryRulesSummary(fields), "missing": evidenceMissing},
		{"id": "submit", "done": e.Status() == "submitted", "status": e.Status(), "submitted_at": rfc3339UTC(e.SubmittedAt), "auto": e.SubmitAuto},
	}
	next := "submit"
	switch {
	case filled < of:
		next = "summary"
	case !evidenceDone:
		next = "evidence"
	case e.Status() != "submitted":
		next = "submit"
	default:
		next = ""
	}
	var now time.Time
	_ = h.database.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return map[string]any{
		"steps": steps, "next": next,
		"deadline": rfc3339UTC(st.Deadline), "deadline_is_extension": st.HasOverride, "event_deadline": rfc3339UTC(st.EventDeadline),
		"server_now": rfc3339Time(now),
	}, nil
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// --- The deadline sweep (section 7) --------------------------------------

// SettleDueEntries runs the deadline sweep for every event: every entry
// whose team is now frozen and not yet settled gets decided -- auto-
// submitted if complete, left as "not submitted" (still judged, see the
// coordinator decision above) otherwise. Wired into the same per-minute
// timer as PinDueTeams (cmd/server/main.go, alongside RequestOpenEventCerts)
// and run again, in the same transaction, by setStage's move to closed or
// judging (settleEntriesTx) so judging never waits a minute for the sweep.
func (h *HackHandler) SettleDueEntries(ctx context.Context) {
	h.settleDueEntries(ctx, "")
}

func (h *HackHandler) settleDueEntries(ctx context.Context, eventID string) {
	ids, err := db.UnsettledFrozenTeamIDs(ctx, h.database, eventID)
	if err != nil {
		log.Printf("hack: settle sweep: %v", err)
		return
	}
	for _, teamID := range ids {
		if err := h.settleOneEntry(ctx, teamID); err != nil {
			log.Printf("hack: settle sweep %s: %v", teamID, err)
		}
	}
}

// settleEntriesTx settles every unsettled, now-frozen entry of the event in
// the caller's own transaction (the setStage hook: one commit covers the
// stage move and every team's settle). The caller has already locked
// event_teams FOR UPDATE.
//
// Before that, a frozen team that never opened the entry form but has a live
// team site gets an empty entry started, so it is settled (and judged,
// flagged Incomplete) too: the site is part of the entry (full-test fix,
// 2026-11-02). A team with neither an entry nor a live site stays out.
func (h *HackHandler) settleEntriesTx(ctx context.Context, tx *sql.Tx, eventID string) error {
	if err := h.startSiteOnlyEntries(ctx, tx, eventID); err != nil {
		return err
	}
	ids, err := db.UnsettledFrozenTeamIDs(ctx, tx, eventID)
	if err != nil {
		return err
	}
	for _, teamID := range ids {
		if err := h.settleOneEntryIn(ctx, tx, teamID); err != nil {
			return err
		}
	}
	return nil
}

// startSiteOnlyEntries starts an empty entry row for each frozen team of the
// event that has no entry but a live team site (settleEntriesTx's first step).
func (h *HackHandler) startSiteOnlyEntries(ctx context.Context, tx *sql.Tx, eventID string) error {
	teams, err := db.FrozenTeamsWithoutEntry(ctx, tx, eventID)
	if err != nil || len(teams) == 0 {
		return err
	}
	ev, err := db.GetEventByID(ctx, tx, eventID)
	if err != nil {
		return err
	}
	for _, t := range teams {
		if !h.entrySiteLive(ctx, ev, t.Slug, t.SiteTakenDown) {
			continue
		}
		if err := db.StartEmptyEventEntry(ctx, tx, eventID, t.TeamID); err != nil {
			return err
		}
	}
	return nil
}

// settleOneEntry locks and settles one team's entry in its own transaction
// (lock order: team, then entry -- there is no specific member or event row
// to lock for this write).
func (h *HackHandler) settleOneEntry(ctx context.Context, teamID string) error {
	tx, err := h.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, teamID); err != nil {
		return err
	}
	if err := h.settleOneEntryIn(ctx, tx, teamID); err != nil {
		return err
	}
	return tx.Commit()
}

// settleOneEntryIn is settleOneEntry's body, run in a transaction that
// already holds the team row FOR UPDATE.
func (h *HackHandler) settleOneEntryIn(ctx context.Context, tx *sql.Tx, teamID string) error {
	st, err := db.TeamWriteStateFor(ctx, tx, teamID, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if !st.Frozen() {
		return nil // raced with an extension since the candidate list was read
	}
	e, err := db.GetEventEntryForUpdate(ctx, tx, teamID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // no entry row: "Not started", nothing to settle
	}
	if err != nil {
		return err
	}
	if e.SubmittedAt.Valid || e.SubmitSettledAt.Valid {
		return nil // already decided
	}
	ev, err := db.GetEventByID(ctx, tx, st.EventID)
	if err != nil {
		return err
	}
	fields := loadEntryRules(ev.EntryRules)
	tracks, err := db.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return err
	}
	track, err := db.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return err
	}
	siteLive := h.entrySiteLive(ctx, ev, st.TeamSlug, st.SiteTakenDown)
	check := evaluateEntry(fields, e, siteLive, len(tracks) > 0, track)
	if check.Complete {
		deadline := time.Now()
		if st.Deadline.Valid {
			deadline = st.Deadline.Time
		}
		return db.SettleEntryComplete(ctx, tx, teamID, deadline)
	}
	return db.SettleEntryIncomplete(ctx, tx, teamID)
}
