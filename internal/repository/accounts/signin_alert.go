package accounts

import (
	"context"

	"github.com/vsriram/simple-host/internal/model"
)

type SignInAlertTarget = model.SignInAlertTarget

func GetSignInAlertTarget(ctx context.Context, q Querier, userID string) (SignInAlertTarget, error) {
	var t SignInAlertTarget
	err := q.QueryRowContext(ctx, `
		SELECT u.username, COALESCE(u.handle, ''), u.signin_alerts, u.event_account
		  FROM users u WHERE u.id = $1`, userID).Scan(&t.Email, &t.Handle, &t.On, &t.Event)
	return t, err
}

func ClaimSignInAlert(ctx context.Context, q Querier, userID, summary string) (bool, error) {
	if _, err := q.ExecContext(ctx, `DELETE FROM signin_alerts_sent WHERE user_id = $1 AND day < (now() AT TIME ZONE 'UTC')::date - 1`, userID); err != nil {
		return false, err
	}
	res, err := q.ExecContext(ctx, `
		INSERT INTO signin_alerts_sent (user_id, summary, day)
		VALUES ($1, $2, (now() AT TIME ZONE 'UTC')::date)
		ON CONFLICT DO NOTHING`, userID, summary)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
