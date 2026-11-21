package entries

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/vsriram/simple-host/products/hack/model"
	entryrepo "github.com/vsriram/simple-host/products/hack/repository/hackentries"
	eventrepo "github.com/vsriram/simple-host/products/hack/repository/hackevents"
	teamrepo "github.com/vsriram/simple-host/products/hack/repository/hackteams"
	"github.com/vsriram/simple-host/products/hack/rules/hackentries"
)

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

// EntryRulesImpact counts entries the proposed fields newly leave short; nothing is un-submitted.
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
	byTeam := entriesByTeam(entries)
	out := RulesImpact{Teams: []ImpactTeam{}}
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
		after := hackentries.Evaluate(fields, e, siteLive, len(tracks) > 0, track)
		before := hackentries.Evaluate(current, e, siteLive, len(tracks) > 0, track)
		if !hackentries.NewlyShort(before, after, e.SubmittedAt.Valid) {
			continue
		}
		if e.SubmittedAt.Valid {
			out.SubmittedMissing++
		} else {
			out.DraftsNewlyBlocked++
		}
		out.Teams = append(out.Teams, ImpactTeam{ID: team.ID, Name: team.Name, Missing: after.Missing})
	}
	return out, nil
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
	preset, fields, judgeFirst, err := nextRules(ev, req)
	if err != nil {
		return RulesResult{}, err
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
	legacyRequired := []string{}
	for _, f := range []string{"title", "tagline", "description", "video_url", "code_url", "screenshot"} {
		if fields[f] == "required" {
			legacyRequired = append(legacyRequired, f)
		}
	}
	updated, err := eventrepo.UpdateEventEntryRules(ctx, tx, eventID, preset, hackentries.MarshalRules(fields), judgeFirst, legacyRequired)
	if err != nil {
		return RulesResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return RulesResult{}, err
	}
	tracks, _ := eventrepo.ListEventTracks(ctx, s.db, updated.ID)
	return RulesResult{Updated: updated, HasTracks: len(tracks) > 0}, nil
}

// nextRules applies req to ev's rules; sent fields that differ from the preset demote it to custom.
func nextRules(ev model.Event, req RulesUpdate) (preset string, fields map[string]string, judgeFirst string, err error) {
	preset, fields, judgeFirst = ev.EntryPreset, hackentries.LoadRules(ev.EntryRules), ev.JudgeFirst
	if req.Preset != nil {
		preset, fields = *req.Preset, hackentries.ExpandPreset(*req.Preset)
		if jf, ok := hackentries.PresetJudgeFirst[preset]; ok {
			judgeFirst = jf
		}
	}
	if req.JudgeFirst != nil {
		judgeFirst = *req.JudgeFirst
	}
	for k, v := range req.Fields {
		if !slices.Contains(hackentries.RuleFields, k) {
			return "", nil, "", &Error{Code: "invalid_entry_field", Message: "fields keys must be one of: " + strings.Join(hackentries.RuleFields, ", ")}
		}
		if !hackentries.RuleModeOK[v] || (v == "one_of" && !hackentries.OneOfFields[k]) {
			return "", nil, "", &Error{Code: "invalid_entry_field_mode", Message: k + " must be required, optional or off" + hackentries.ModeSuffix(k)}
		}
		fields[k] = v
	}
	fields = hackentries.NormaliseRules(fields)
	if req.Fields != nil && preset != "custom" && !hackentries.FieldsEqual(fields, hackentries.ExpandPreset(preset)) {
		preset = "custom"
	}
	return preset, fields, judgeFirst, nil
}

// EvaluateEntryTx evaluates the entry with rules, track and site state read inside tx.
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

// Progress contains the facts used to render the participant checklist.
type Progress struct {
	Entry                           model.EventEntry
	Filled, Of                      int
	SummaryMissing, EvidenceMissing []string
	EvidenceDone                    bool
	Summary, TeamLine, Next         string
	Now                             time.Time
}

// EntryProgress is the participant Home checklist, shared by the website and the AI apps.
func (s *Service) EntryProgress(ctx context.Context, ev model.Event, team model.EventTeam, teammates []string, st model.TeamWriteState) (Progress, error) {
	e, err := entryrepo.GetEventEntry(ctx, s.db, team.ID)
	if errors.Is(err, sql.ErrNoRows) {
		e = model.EventEntry{}
	} else if err != nil {
		return Progress{}, err
	}
	tracks, err := eventrepo.ListEventTracks(ctx, s.db, ev.ID)
	if err != nil {
		return Progress{}, err
	}
	track, err := eventrepo.TeamTrack(ctx, s.db, team.ID)
	if err != nil {
		return Progress{}, err
	}
	siteLive := s.siteLive(ctx, ev, team.Slug, st.SiteTakenDown)
	p := progressSteps(hackentries.LoadRules(ev.EntryRules), e, siteLive, len(tracks) > 0, track)
	p.TeamLine = "On " + team.Name
	if len(teammates) > 0 {
		p.TeamLine += " with " + hackentries.JoinNames(teammates)
	}
	_ = s.db.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&p.Now)
	return p, nil
}

// progressSteps is the checklist's counts and next step for e under fields.
func progressSteps(fields map[string]string, e model.EventEntry, siteLive, hasTracks bool, track *model.EventTrack) Progress {
	p := Progress{Entry: e, SummaryMissing: []string{}, EvidenceMissing: []string{}, Summary: hackentries.RulesSummary(fields)}
	for _, f := range hackentries.SummaryFields {
		if fields[f] != "required" || (f == "track" && !hasTracks) {
			continue
		}
		p.Of++
		if hackentries.FieldPresent(f, e, siteLive, track) {
			p.Filled++
		} else {
			p.SummaryMissing = append(p.SummaryMissing, f)
		}
	}
	for _, m := range hackentries.Evaluate(fields, e, siteLive, hasTracks, track).Missing {
		if m == "one_of" || hackentries.OneOfFields[m] {
			p.EvidenceMissing = append(p.EvidenceMissing, m)
		}
	}
	p.EvidenceDone = len(p.EvidenceMissing) == 0
	switch {
	case p.Filled < p.Of:
		p.Next = "summary"
	case !p.EvidenceDone:
		p.Next = "evidence"
	case e.Status() != "submitted":
		p.Next = "submit"
	}
	return p
}
