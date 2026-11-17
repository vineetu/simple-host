package accounts

import (
	"context"
)

// SetSignInAlerts turns sign-in alert emails on or off for the account.
func SetSignInAlerts(ctx context.Context, q Querier, userID string, on bool) error {
	_, err := q.ExecContext(ctx, `UPDATE users SET signin_alerts = $2 WHERE id = $1`, userID, on)
	return err
}
