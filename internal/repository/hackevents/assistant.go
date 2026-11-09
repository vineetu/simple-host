package hackevents

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/vsriram/simple-host/internal/repository/sqlkit"
)

// ErrAssistantDailyLimit and ErrAssistantMonthlyBudget are the two refusals
// AssistantReserve returns; the caller (internal/app/hack/events) maps them
// to the contract's daily_limit/monthly_budget error codes.
var (
	ErrAssistantDailyLimit    = errors.New("hackevents: assistant daily question limit reached")
	ErrAssistantMonthlyBudget = errors.New("hackevents: assistant monthly budget reached")
)

// assistantLockKey is the fixed pg_advisory_xact_lock key every
// Reserve/Settle/Refund call takes, since one organiser's daily count and
// the instance's monthly spend are checked and updated together and must
// never race against each other (SIM-394).
const assistantLockKey int64 = 7394202601

// AssistantReserve checks and reserves one organiser question against both
// the per-organiser daily limit and the instance-wide monthly budget, in one
// transaction under assistantLockKey: refuses with ErrAssistantDailyLimit if
// today's count is already at dailyLimit, else with ErrAssistantMonthlyBudget
// if this month's spend plus reserveMicro would exceed budgetMicro,
// otherwise increments today's question count and adds reserveMicro to this
// month's spend. day and month are UTC dates (time-of-day ignored).
func AssistantReserve(ctx context.Context, db *sql.DB, userID string, day, month time.Time, dailyLimit int, budgetMicro, reserveMicro int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, assistantLockKey); err != nil {
		return err
	}

	var questions int
	err = tx.QueryRowContext(ctx, `SELECT questions FROM hack_assistant_usage WHERE user_id = $1 AND day = $2`, userID, day).Scan(&questions)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if questions >= dailyLimit {
		return ErrAssistantDailyLimit
	}

	var spent int64
	err = tx.QueryRowContext(ctx, `SELECT cost_micro_usd FROM hack_assistant_spend WHERE month = $1`, month).Scan(&spent)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if spent+reserveMicro > budgetMicro {
		return ErrAssistantMonthlyBudget
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hack_assistant_usage (user_id, day, questions) VALUES ($1, $2, 1)
		ON CONFLICT (user_id, day) DO UPDATE SET questions = hack_assistant_usage.questions + 1
	`, userID, day); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hack_assistant_spend (month, cost_micro_usd) VALUES ($1, $2)
		ON CONFLICT (month) DO UPDATE SET cost_micro_usd = hack_assistant_spend.cost_micro_usd + EXCLUDED.cost_micro_usd
	`, month, reserveMicro); err != nil {
		return err
	}
	return tx.Commit()
}

// AssistantSettle adjusts this month's spend by deltaMicro (actual cost
// minus the amount AssistantReserve already added; negative when the actual
// answer cost less than the worst-case reservation). Never lets the ledger
// go negative, which a reservation it is settling against already covers.
func AssistantSettle(ctx context.Context, db *sql.DB, month time.Time, deltaMicro int64) error {
	if deltaMicro == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, assistantLockKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO hack_assistant_spend (month, cost_micro_usd) VALUES ($1, GREATEST(0, $2))
		ON CONFLICT (month) DO UPDATE SET cost_micro_usd = GREATEST(0, hack_assistant_spend.cost_micro_usd + $2)
	`, month, deltaMicro); err != nil {
		return err
	}
	return tx.Commit()
}

// AssistantRefund undoes one AssistantReserve: decrements today's question
// count and this month's spend by reserveMicro, both floored at zero. Used
// when the provider fails before any answer text streamed back.
func AssistantRefund(ctx context.Context, db *sql.DB, userID string, day, month time.Time, reserveMicro int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, assistantLockKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE hack_assistant_usage SET questions = GREATEST(0, questions - 1) WHERE user_id = $1 AND day = $2
	`, userID, day); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE hack_assistant_spend SET cost_micro_usd = GREATEST(0, cost_micro_usd - $1) WHERE month = $2
	`, reserveMicro, month); err != nil {
		return err
	}
	return tx.Commit()
}

// AssistantUsedToday reads today's question count for userID (0 if no row).
func AssistantUsedToday(ctx context.Context, q sqlkit.Querier, userID string, day time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT questions FROM hack_assistant_usage WHERE user_id = $1 AND day = $2`, userID, day).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// AssistantSpentThisMonth reads this month's instance-wide spend in micro-USD
// (0 if no row yet).
func AssistantSpentThisMonth(ctx context.Context, q sqlkit.Querier, month time.Time) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx, `SELECT cost_micro_usd FROM hack_assistant_spend WHERE month = $1`, month).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}
