package accounts

import (
	"context"
	"database/sql"
	events "github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type DeletionQueries interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// HackAccountDeleteBlock says why an account on the hackathon platform cannot
// be deleted yet ("" when it can). An event's holding account goes only with
// its event (the admin's Events tab). An organiser of an event still running
// ends or deletes it first, so no event is left that nobody can manage (an
// event the platform took down does not hold them). The organiser's name and
// contact address are blanked on the events that stay.
func CheckHackAccountDeletion(ctx context.Context, q DeletionQueries, userID string, holdingAccounts bool) (code, msg string, err error) {
	if !holdingAccounts {
		return "", "", nil
	}
	holding, err := HoldingAccount(ctx, q, userID, holdingAccounts)
	if err != nil {
		return "", "", err
	}
	if holding {
		return "event_account", "this account holds an event; delete the event from the admin Events tab", nil
	}
	running, err := events.AccountHasRunningEvents(ctx, q, userID)
	if err != nil {
		return "", "", err
	}
	if running {
		return "organises_events", "you organise an event that has not ended; end it, or delete it while nobody has joined, before deleting the account", nil
	}
	return "", "", events.CleanupDeletedAccountEvents(ctx, q, userID)
}

// HackHoldingAccount: userID is an event's holding account (never signs in).
func HoldingAccount(ctx context.Context, q DeletionQueries, userID string, holdingAccounts bool) (bool, error) {
	if !holdingAccounts {
		return false, nil
	}
	return events.AccountHoldsEvent(ctx, q, userID)
}
