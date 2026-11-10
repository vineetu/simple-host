package hackevents

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// UpdateEventEntryRules writes the organiser's entry rules: the preset name,
// the expanded+validated rules JSON (never the client's raw JSON -- the
// handler normalises it first) and which evidence judges see first. It also
// writes entry_required for the deprecated alias, kept in sync with the
// rules' required fields among hackEntryFields for one release. Every real
// call here is an organiser explicitly saving rules (PutRules's non-dry-run
// path, or the deprecated entry_required alias), so it also sets
// entry_rules_chosen true for good (0005-hack-entry-rules-chosen.sql,
// SIM-382) -- even a save that keeps the default preset counts as a choice.

func UpdateEventEntryRules(ctx context.Context, q sqlkit.Querier, eventID, preset string, rulesJSON []byte, judgeFirst string, legacyRequired []string) (Event, error) {
	if legacyRequired == nil {
		legacyRequired = []string{}
	}
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET entry_preset = $2, entry_rules = $3, judge_first = $4, entry_required = $5, entry_rules_chosen = true, updated_at = now()
		 WHERE id = $1 RETURNING `+EventColumns, eventID, preset, rulesJSON, judgeFirst, pq.Array(legacyRequired)))
}

// UpdateJudgingSettings writes the fields that are non-nil and returns the event.
func UpdateJudgingSettings(ctx context.Context, q sqlkit.Querier, eventID string, mode *string, perTeam *int) (Event, error) {
	var modeArg, perArg any
	if mode != nil {
		modeArg = *mode
	}
	if perTeam != nil {
		perArg = *perTeam
	}
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET
			judge_assignment_mode = CASE WHEN $2::text IS NULL THEN judge_assignment_mode ELSE $2::text END,
			judges_per_team = CASE WHEN $3::integer IS NULL THEN judges_per_team ELSE $3::integer END,
			updated_at = now()
		WHERE id = $1
		RETURNING `+EventColumns, eventID, modeArg, perArg))
}

// SetJudgingLock sets or clears judging_locked_at/judging_lock_reason.
// locked=false clears both regardless of reason; locked=true sets
// judging_locked_at to now() only if it was not already set (idempotent),
// leaving the stored reason from the last unlock in place until the next one.
func SetJudgingLock(ctx context.Context, q sqlkit.Querier, eventID string, locked bool, reason string) (Event, error) {
	if locked {
		return ScanEvent(q.QueryRowContext(ctx, `
			UPDATE events SET
				judging_locked_at = COALESCE(judging_locked_at, clock_timestamp()),
				updated_at = now()
			WHERE id = $1
			RETURNING `+EventColumns, eventID))
	}
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET
			judging_locked_at = NULL,
			judging_lock_reason = $2,
			updated_at = now()
		WHERE id = $1
		RETURNING `+EventColumns, eventID, reason))
}

// UpdateScoreSettings writes the event's score presentation settings.
func UpdateScoreSettings(ctx context.Context, q sqlkit.Querier, eventID string, scoreMode any, hasTie bool, tie, publicScores, publicRanks any) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET score_mode=COALESCE($2::text,score_mode),
			tie_criterion_id=CASE WHEN $3::boolean THEN NULLIF($4::text,'')::uuid ELSE tie_criterion_id END,
			public_scores=COALESCE($5::boolean,public_scores), public_ranks=COALESCE($6::boolean,public_ranks), updated_at=now() WHERE id=$1`, eventID, scoreMode, hasTie, tie, publicScores, publicRanks)
	return err
}

// SetOrganiserJudges validates the current membership and persists the selection.
func SetOrganiserJudges(ctx context.Context, q sqlkit.Querier, eventID string, ids []string) (bool, error) {
	var valid bool
	err := q.QueryRowContext(ctx, `SELECT NOT EXISTS (SELECT 1 FROM unnest($2::uuid[]) id WHERE NOT EXISTS (SELECT 1 FROM event_members m WHERE m.event_id=$1 AND m.user_id=id AND m.role='organiser'))`, eventID, pq.Array(ids)).Scan(&valid)
	if err != nil || !valid {
		return valid, err
	}
	_, err = q.ExecContext(ctx, `UPDATE events SET organiser_judges=$2::uuid[], updated_at=now() WHERE id=$1`, eventID, pq.Array(ids))
	return true, err
}

// OrganiserIDs is the legacy include_organisers=true selection.
func OrganiserIDs(ctx context.Context, q sqlkit.Querier, eventID string) ([]string, error) {
	rows, err := queryContext(ctx, q, `SELECT user_id FROM event_members WHERE event_id=$1 AND role='organiser' ORDER BY user_id`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
