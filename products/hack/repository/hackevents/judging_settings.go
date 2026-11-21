package hackevents

import (
	"context"

	"github.com/lib/pq"
	"github.com/vsriram/simple-host/host/sqlkit"
)

func UpdateEventEntryRules(ctx context.Context, q sqlkit.Querier, eventID, preset string, rulesJSON []byte, judgeFirst string, legacyRequired []string) (Event, error) {
	if legacyRequired == nil {
		legacyRequired = []string{}
	}
	return ScanEvent(q.QueryRowContext(ctx, `
		UPDATE events SET entry_preset = $2, entry_rules = $3, judge_first = $4, entry_required = $5, entry_rules_chosen = true, updated_at = now()
		 WHERE id = $1 RETURNING `+EventColumns, eventID, preset, rulesJSON, judgeFirst, pq.Array(legacyRequired)))
}

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

func UpdateScoreSettings(ctx context.Context, q sqlkit.Querier, eventID string, scoreMode any, hasTie bool, tie, publicScores, publicRanks any) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET score_mode=COALESCE($2::text,score_mode),
			tie_criterion_id=CASE WHEN $3::boolean THEN NULLIF($4::text,'')::uuid ELSE tie_criterion_id END,
			public_scores=COALESCE($5::boolean,public_scores), public_ranks=COALESCE($6::boolean,public_ranks), updated_at=now() WHERE id=$1`, eventID, scoreMode, hasTie, tie, publicScores, publicRanks)
	return err
}

// SetOrganiserJudges stores which organisers judge (the caller checked each is one).
func SetOrganiserJudges(ctx context.Context, q sqlkit.Querier, eventID string, ids []string) error {
	_, err := q.ExecContext(ctx, `UPDATE events SET organiser_judges=$2::uuid[], updated_at=now() WHERE id=$1`, eventID, pq.Array(ids))
	return err
}
