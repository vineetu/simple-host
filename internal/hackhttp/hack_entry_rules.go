package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
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

// writeHackErrMissing is writeHackErr with a "missing" array attached, for
// the entry refusals a client maps to field errors (section 4.1).
func writeHackErrMissing(w http.ResponseWriter, status int, code, msg string, missing []string) {
	if missing == nil {
		missing = []string{}
	}
	httpx.WriteJSON(w, status, map[string]any{"error": msg, "code": code, "missing": missing})
}

// evaluateEntryTx is hackentries.Evaluate with every piece read fresh inside tx:
// the rules, the team's track and the site's live state.
func (h *HackHandler) evaluateEntryTx(ctx context.Context, tx *sql.Tx, ev db.Event, st db.TeamWriteState, teamID string, e db.EventEntry) (hackentries.Check, error) {
	fields := hackentries.LoadRules(ev.EntryRules)
	tracks, err := db.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return hackentries.Check{}, err
	}
	track, err := db.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return hackentries.Check{}, err
	}
	siteLive := h.entrySiteLive(ctx, ev, st.TeamSlug, st.SiteTakenDown)
	return hackentries.Evaluate(fields, e, siteLive, len(tracks) > 0, track), nil
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
	fields := hackentries.LoadRules(ev.EntryRules)
	return map[string]any{
		"preset":      ev.EntryPreset,
		"fields":      fields,
		"judge_first": ev.JudgeFirst,
		"summary":     hackentries.RulesSummary(fields),
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
	httpx.WriteJSON(w, http.StatusOK, entryRulesView(a.event, len(tracks) > 0, hackentries.RulesEditable(a.event.Stage)))
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
	if req.Preset != nil && !hackentries.PresetKnown(*req.Preset) {
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
	if !hackentries.RulesEditable(ev.Stage) {
		httpx.WriteError(w, http.StatusConflict, "entry_rules_closed", "entry rules are fixed once entries close")
		return
	}
	preset := ev.EntryPreset
	fields := hackentries.LoadRules(ev.EntryRules)
	if req.Preset != nil {
		preset = *req.Preset
		fields = hackentries.ExpandPreset(preset)
	}
	if req.Fields != nil {
		for k, v := range req.Fields {
			known := false
			for _, f := range hackentries.RuleFields {
				if f == k {
					known = true
					break
				}
			}
			if !known {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_entry_field", "fields keys must be one of: "+strings.Join(hackentries.RuleFields, ", "))
				return
			}
			if !hackentries.RuleModeOK[v] || (v == "one_of" && !hackentries.OneOfFields[k]) {
				httpx.WriteError(w, http.StatusBadRequest, "invalid_entry_field_mode", k+" must be required, optional or off"+hackentries.ModeSuffix(k))
				return
			}
			fields[k] = v
		}
		fields = hackentries.NormaliseRules(fields)
		// A field sent that differs from the preset's own table demotes the
		// stored preset to custom (section 4.2): the saved rules are no
		// longer exactly that preset's table.
		if preset != "custom" && !hackentries.FieldsEqual(fields, hackentries.ExpandPreset(preset)) {
			preset = "custom"
		}
	}
	fields = hackentries.NormaliseRules(fields)
	judgeFirst := ev.JudgeFirst
	if req.Preset != nil {
		if jf, ok := hackentries.PresetJudgeFirst[*req.Preset]; ok {
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
			"preset": preset, "fields": fields, "judge_first": judgeFirst, "summary": hackentries.RulesSummary(fields), "has_tracks": len(tracks) > 0,
		}})
		return
	}
	rulesJSON := hackentries.MarshalRules(fields)
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
	httpx.WriteJSON(w, http.StatusOK, entryRulesView(updated, len(tracks) > 0, hackentries.RulesEditable(updated.Stage)))
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
	current := hackentries.LoadRules(ev.EntryRules)
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
		check := hackentries.Evaluate(fields, e, siteLive, hasTracks, track)
		if check.Complete {
			continue
		}
		before := hackentries.Evaluate(current, e, siteLive, hasTracks, track)
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
	fields := hackentries.LoadRules(ev.EntryRules)
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
	for _, f := range hackentries.SummaryFields {
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
		if hackentries.FieldPresent(f, e, siteLive, track) {
			filled++
		} else {
			summaryMissing = append(summaryMissing, f)
		}
	}
	evidenceCheck := hackentries.Evaluate(fields, e, siteLive, hasTracks, track)
	evidenceMissing := []string{}
	for _, m := range evidenceCheck.Missing {
		if m == "one_of" || m == "code_url" {
			evidenceMissing = append(evidenceMissing, m)
		}
	}
	evidenceDone := len(evidenceMissing) == 0

	teamLine := "On " + team.Name
	if len(teammates) > 0 {
		teamLine += " with " + hackentries.JoinNames(teammates)
	}
	steps := []map[string]any{
		{"id": "team", "done": true, "detail": teamLine},
		{"id": "summary", "done": filled >= of, "filled": filled, "of": of, "missing": summaryMissing},
		{"id": "evidence", "done": evidenceDone, "summary": hackentries.RulesSummary(fields), "missing": evidenceMissing},
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
	fields := hackentries.LoadRules(ev.EntryRules)
	tracks, err := db.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return err
	}
	track, err := db.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return err
	}
	siteLive := h.entrySiteLive(ctx, ev, st.TeamSlug, st.SiteTakenDown)
	check := hackentries.Evaluate(fields, e, siteLive, len(tracks) > 0, track)
	if check.Complete {
		deadline := time.Now()
		if st.Deadline.Valid {
			deadline = st.Deadline.Time
		}
		return db.SettleEntryComplete(ctx, tx, teamID, deadline)
	}
	return db.SettleEntryIncomplete(ctx, tx, teamID)
}
