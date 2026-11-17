package accounts

import (
	"context"
)

// ReplaceAPIKeys removes every key an account holds and stores newKey as its
// only one. Unlike RotateAPIKey it needs no current key: the admin uses it to
// reissue a participant's lost key. Every site sign-in of the account ends
// too (EndVisitorSessions). Run it inside the caller's transaction.
func ReplaceAPIKeys(ctx context.Context, q Querier, userID, newKey, name string) error {
	if err := DeleteAllKeys(ctx, q, userID); err != nil {
		return err
	}
	if err := EndVisitorSessions(ctx, q, userID); err != nil {
		return err
	}
	return AddAPIKey(ctx, q, userID, newKey, name)
}
