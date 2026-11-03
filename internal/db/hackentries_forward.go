package db

import (
	"context"
	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/repository/hackentries"
)

// Transitional (cleanup 4.1): callers move to app/hack/entries and repository/hackentries; delete as the last caller moves.
// Alias (cleanup 4.1): defined in internal/model.
type EventEntry = model.EventEntry

// Alias (cleanup 4.1): defined in internal/repository/hackentries.
type FrozenTeamWithoutEntry = hackentries.FrozenTeamWithoutEntry

// Alias (cleanup 4.1): defined in internal/model.
type RubricCriterion = model.RubricCriterion

// Alias (cleanup 4.1): defined in internal/model.
type NamedTeam = model.NamedTeam

// Alias (cleanup 4.1): defined in internal/model.
type NamedJudge = model.NamedJudge

// Alias (cleanup 4.1): defined in internal/repository/hackentries.
type EventConflict = hackentries.EventConflict

// Alias (cleanup 4.1): defined in internal/repository/hackentries.
type ConflictPair = hackentries.ConflictPair

// Alias (cleanup 4.1): defined in internal/model.
type JudgeAssignment = model.JudgeAssignment

// Alias (cleanup 4.1): defined in internal/repository/hackentries.
type JudgeScore = hackentries.JudgeScore

// Alias (cleanup 4.1): defined in internal/model.
type RawScoreRow = model.RawScoreRow

// Alias (cleanup 4.1): defined in internal/repository/hackentries.
type EventResultsRow = hackentries.EventResultsRow

// Alias (cleanup 4.1): defined in internal/repository/hackentries.
type TrackPanelMember = hackentries.TrackPanelMember

const EntryJudgeableSQL = hackentries.EntryJudgeableSQL

var (
	GetEventEntry                       = hackentries.GetEventEntry
	GetEventEntryForUpdate              = hackentries.GetEventEntryForUpdate
	ListEventEntries                    = hackentries.ListEventEntries
	UpsertEventEntryText                = hackentries.UpsertEventEntryText
	UpsertEventEntryScreenshot          = hackentries.UpsertEventEntryScreenshot
	GetEventScreenshot                  = hackentries.GetEventScreenshot
	TeamSiteLive                        = hackentries.TeamSiteLive
	SubmitEventEntry                    = hackentries.SubmitEventEntry
	UnsubmitEventEntry                  = hackentries.UnsubmitEventEntry
	SettleEntryComplete                 = hackentries.SettleEntryComplete
	SettleEntryIncomplete               = hackentries.SettleEntryIncomplete
	UnsettledFrozenTeamIDs              = hackentries.UnsettledFrozenTeamIDs
	FrozenTeamsWithoutEntry             = hackentries.FrozenTeamsWithoutEntry
	StartEmptyEventEntry                = hackentries.StartEmptyEventEntry
	UnsettleTeamEntry                   = hackentries.UnsettleTeamEntry
	UnsettleEventDrafts                 = hackentries.UnsettleEventDrafts
	SetEntryExcluded                    = hackentries.SetEntryExcluded
	ListRubric                          = hackentries.ListRubric
	ReplaceRubric                       = hackentries.ReplaceRubric
	ListEventJudges                     = hackentries.ListEventJudges
	ListTeamsWithMembers                = hackentries.ListTeamsWithMembers
	EventTeamInEvent                    = hackentries.EventTeamInEvent
	EventJudge                          = hackentries.EventJudge
	ListEventConflicts                  = hackentries.ListEventConflicts
	GetEventConflict                    = hackentries.GetEventConflict
	DeclareConflict                     = hackentries.DeclareConflict
	DeleteConflict                      = hackentries.DeleteConflict
	ListConflictPairs                   = hackentries.ListConflictPairs
	ReplaceAssignments                  = hackentries.ReplaceAssignments
	ScoreCoverage                       = hackentries.ScoreCoverage
	AssignmentCounts                    = hackentries.AssignmentCounts
	GetJudgeTeamScores                  = hackentries.GetJudgeTeamScores
	UpsertJudgeScores                   = hackentries.UpsertJudgeScores
	CriterionIDsForEvent                = hackentries.CriterionIDsForEvent
	ListRawScores                       = hackentries.ListRawScores
	GetEventResults                     = hackentries.GetEventResults
	UpsertEventResults                  = hackentries.UpsertEventResults
	SetResultsFullRanking               = hackentries.SetResultsFullRanking
	TeamComments                        = hackentries.TeamComments
	ListTrackPanels                     = hackentries.ListTrackPanels
	ReplaceTrackPanels                  = hackentries.ReplaceTrackPanels
	ListEventAssignments                = hackentries.ListEventAssignments
	EligibleJudgeTeams                  = hackentries.EligibleJudgeTeams
	EligibleJudgeTeamsIgnoringConflicts = hackentries.EligibleJudgeTeamsIgnoringConflicts
	JudgeTeamEligible                   = hackentries.JudgeTeamEligible
	EventTrackInEvent                   = hackentries.EventTrackInEvent
	EventJudgeInEvent                   = hackentries.EventJudgeInEvent
	EventCriterionInEvent               = hackentries.EventCriterionInEvent
	TrackForTeam                        = hackentries.TrackForTeam
)

// UpdateEventEntryRules writes the organiser's entry rules: the preset name,
// the expanded+validated rules JSON (never the client's raw JSON -- the
// handler normalises it first) and which evidence judges see first. It also
// writes entry_required for the deprecated alias, kept in sync with the
// rules' required fields among hackEntryFields for one release.

func UpdateEventEntryRules(ctx context.Context, q Querier, eventID, preset string, rulesJSON []byte, judgeFirst string, legacyRequired []string) (Event, error) {
	if legacyRequired == nil {
		legacyRequired = []string{}
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET entry_preset = $2, entry_rules = $3, judge_first = $4, entry_required = $5, updated_at = now()
		 WHERE id = $1 RETURNING `+eventColumns, eventID, preset, rulesJSON, judgeFirst, pq.Array(legacyRequired)))
}

// UpdateJudgingSettings writes the fields that are non-nil and returns the event.
func UpdateJudgingSettings(ctx context.Context, q Querier, eventID string, mode *string, perTeam *int) (Event, error) {
	var modeArg, perArg any
	if mode != nil {
		modeArg = *mode
	}
	if perTeam != nil {
		perArg = *perTeam
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET
			judge_assignment_mode = CASE WHEN $2::text IS NULL THEN judge_assignment_mode ELSE $2::text END,
			judges_per_team = CASE WHEN $3::integer IS NULL THEN judges_per_team ELSE $3::integer END,
			updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns, eventID, modeArg, perArg))
}

// SetJudgingLock sets or clears judging_locked_at/judging_lock_reason.
// locked=false clears both regardless of reason; locked=true sets
// judging_locked_at to now() only if it was not already set (idempotent),
// leaving the stored reason from the last unlock in place until the next one.
func SetJudgingLock(ctx context.Context, q Querier, eventID string, locked bool, reason string) (Event, error) {
	if locked {
		return scanEvent(q.QueryRowContext(ctx, `
			UPDATE events SET
				judging_locked_at = COALESCE(judging_locked_at, clock_timestamp()),
				updated_at = now()
			WHERE id = $1
			RETURNING `+eventColumns, eventID))
	}
	return scanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET
			judging_locked_at = NULL,
			judging_lock_reason = $2,
			updated_at = now()
		WHERE id = $1
		RETURNING `+eventColumns, eventID, reason))
}
