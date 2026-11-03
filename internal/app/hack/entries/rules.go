package entries

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/model"
	entryrepo "github.com/vsriram/simple-host/internal/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/internal/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/internal/repository/hackteams"
	"github.com/vsriram/simple-host/internal/rules/hackentries"
)

// EntryRulesImpact is the dry run's counts (section 4.2): how many already-
// submitted entries would newly read as missing something under the
// proposed fields (they stay submitted regardless -- this is
// informational), and how many drafts would newly need more before they can
// submit. "Newly" means the proposed rules ask for a line the current rules
// don't: an entry already short of the current rules in exactly the same
// way is not counted again, so loosening or re-saving unchanged rules
// reports no impact.
// ImpactTeam identifies an entry affected by a rule change.
type ImpactTeam struct {
	ID, Name string
	Missing  []string
}

// RulesImpact counts entries affected by a rule change.
type RulesImpact struct {
	SubmittedMissing, DraftsNewlyBlocked int
	Teams                                []ImpactTeam
}

// RulesResult contains the result of an update or preview.
type RulesResult struct {
	Impact     RulesImpact
	Preset     string
	Fields     map[string]string
	JudgeFirst string
	HasTracks  bool
	Updated    model.Event
}

func (s *Service) EntryRulesImpact(ctx context.Context, tx *sql.Tx, ev model.Event, fields map[string]string) (RulesImpact, error) {
	current := hackentries.LoadRules(ev.EntryRules)
	teams, err := teamrepo.ListEventTeams(ctx, tx, ev.ID)
	if err != nil {
		return RulesImpact{}, err
	}
	entries, err := entryrepo.ListEventEntries(ctx, tx, ev.ID)
	if err != nil {
		return RulesImpact{}, err
	}
	tracks, err := eventrepo.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return RulesImpact{}, err
	}
	hasTracks := len(tracks) > 0
	byTeam := map[string]model.EventEntry{}
	for _, e := range entries {
		byTeam[e.TeamID] = e
	}
	submittedMissing, draftsNewlyBlocked := 0, 0
	affected := []ImpactTeam{}
	for _, team := range teams {
		e, ok := byTeam[team.ID]
		if !ok {
			continue
		}
		track, err := eventrepo.TeamTrack(ctx, tx, team.ID)
		if err != nil {
			return RulesImpact{}, err
		}
		siteLive := s.siteLive(ctx, ev, team.Slug, team.SiteTakenDownAt.Valid)
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
		affected = append(affected, ImpactTeam{ID: team.ID, Name: team.Name, Missing: check.Missing})
	}
	return RulesImpact{SubmittedMissing: submittedMissing, DraftsNewlyBlocked: draftsNewlyBlocked, Teams: affected}, nil
}

// RulesUpdate contains the requested rule changes.
type RulesUpdate struct {
	Preset     *string
	Fields     map[string]string
	JudgeFirst *string
	DryRun     bool
}

// PutRules updates entry rules or previews their impact.
func (s *Service) PutRules(ctx context.Context, eventID string, req RulesUpdate) (RulesResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RulesResult{}, err
	}
	defer tx.Rollback()
	ev, err := eventrepo.GetEventForUpdate(ctx, tx, eventID)
	if err != nil {
		return RulesResult{}, err
	}
	if !hackentries.RulesEditable(ev.Stage) {
		return RulesResult{}, &Error{Code: "entry_rules_closed", Message: "entry rules are fixed once entries close"}
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
				return RulesResult{}, &Error{Code: "invalid_entry_field", Message: "fields keys must be one of: " + strings.Join(hackentries.RuleFields, ", ")}
			}
			if !hackentries.RuleModeOK[v] || (v == "one_of" && !hackentries.OneOfFields[k]) {
				return RulesResult{}, &Error{Code: "invalid_entry_field_mode", Message: k + " must be required, optional or off" + hackentries.ModeSuffix(k)}
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
		impact, err := s.EntryRulesImpact(ctx, tx, ev, fields)
		if err != nil {
			return RulesResult{}, err
		}
		tracks, err := eventrepo.ListEventTracks(ctx, tx, eventID)
		if err != nil {
			return RulesResult{}, err
		}
		return RulesResult{Impact: impact, Preset: preset, Fields: fields, JudgeFirst: judgeFirst, HasTracks: len(tracks) > 0}, nil
	}
	rulesJSON := hackentries.MarshalRules(fields)
	legacyRequired := []string{}
	for _, f := range []string{"title", "tagline", "description", "video_url", "code_url", "screenshot"} {
		if fields[f] == "required" {
			legacyRequired = append(legacyRequired, f)
		}
	}
	updated, err := eventrepo.UpdateEventEntryRules(ctx, tx, eventID, preset, rulesJSON, judgeFirst, legacyRequired)
	if err != nil {
		return RulesResult{}, err
	}
	// Tightening never un-submits (section 12): nothing here touches
	// submitted_at. A team newly short of the tightened rules just shows
	// "Incomplete" if it later unsubmits and the deadline passes again.
	if err := tx.Commit(); err != nil {
		return RulesResult{}, err
	}
	tracks, _ := eventrepo.ListEventTracks(ctx, s.db, updated.ID)
	return RulesResult{Updated: updated, HasTracks: len(tracks) > 0}, nil
}

// EvaluateEntryTx is hackentries.Evaluate with every piece read fresh inside tx:
// the rules, the team's track and the site's live state.
func (s *Service) EvaluateEntryTx(ctx context.Context, tx *sql.Tx, ev model.Event, st model.TeamWriteState, teamID string, e model.EventEntry) (hackentries.Check, error) {
	fields := hackentries.LoadRules(ev.EntryRules)
	tracks, err := eventrepo.ListEventTracks(ctx, tx, ev.ID)
	if err != nil {
		return hackentries.Check{}, err
	}
	track, err := eventrepo.TeamTrack(ctx, tx, teamID)
	if err != nil {
		return hackentries.Check{}, err
	}
	siteLive := s.siteLive(ctx, ev, st.TeamSlug, st.SiteTakenDown)
	return hackentries.Evaluate(fields, e, siteLive, len(tracks) > 0, track), nil
}

// EntryProgress is the participant Home checklist (section 4.3): four
// steps (team, summary, evidence, submit), the next one to do, and the
// deadline, so the website and the AI apps compute it identically.
// Progress contains the facts used to render the participant checklist.
type Progress struct {
	Entry                           model.EventEntry
	Filled, Of                      int
	SummaryMissing, EvidenceMissing []string
	EvidenceDone                    bool
	Summary, TeamLine, Next         string
	Now                             time.Time
}

func (s *Service) EntryProgress(ctx context.Context, ev model.Event, team model.EventTeam, teammates []string, st model.TeamWriteState) (Progress, error) {
	e, err := entryrepo.GetEventEntry(ctx, s.db, team.ID)
	if errors.Is(err, sql.ErrNoRows) {
		e = model.EventEntry{}
	} else if err != nil {
		return Progress{}, err
	}
	fields := hackentries.LoadRules(ev.EntryRules)
	tracks, err := eventrepo.ListEventTracks(ctx, s.db, ev.ID)
	if err != nil {
		return Progress{}, err
	}
	hasTracks := len(tracks) > 0
	track, err := eventrepo.TeamTrack(ctx, s.db, team.ID)
	if err != nil {
		return Progress{}, err
	}
	siteLive := s.siteLive(ctx, ev, team.Slug, st.SiteTakenDown)

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
	_ = s.db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return Progress{Entry: e, Filled: filled, Of: of, SummaryMissing: summaryMissing, EvidenceMissing: evidenceMissing, EvidenceDone: evidenceDone, Summary: hackentries.RulesSummary(fields), TeamLine: teamLine, Next: next, Now: now}, nil
}
