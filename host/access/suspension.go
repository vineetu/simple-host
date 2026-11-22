package access

import (
	"context"
	"database/sql"
	"errors"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	siterules "github.com/vsriram/simple-host/host/rules/sites"
)

// SetSuspension is Access's part of suspending a person (on, with reason)
// or letting them back in (host/ops SuspendPerson): a malformed or unknown
// id is a 404 LifecycleError; an account that holds an event, or an operator's, is
// refused; otherwise it sets the account's flag, which every key, connected
// app, visitor session and site of theirs checks.
func SetSuspension(ctx context.Context, db *sql.DB, id, reason string, on bool) (model.User, error) {
	if !siterules.UUIDShape.MatchString(id) {
		return model.User{}, &LifecycleError{Status: 404, Message: "not found"}
	}
	if held, err := HoldingAccount(ctx, db, id); err != nil {
		return model.User{}, err
	} else if held {
		return model.User{}, &LifecycleError{Status: 409, Message: HoldingText("address"), Code: "event_account"}
	}
	target, err := repo.GetUserByID(ctx, db, id)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, &LifecycleError{Status: 404, Message: "not found"}
	}
	if err != nil {
		return model.User{}, err
	}
	if on && IsOperator(target.IsAdmin, target.Username) {
		return model.User{}, &LifecycleError{Status: 400, Message: "cannot suspend an admin account", Code: "admin_account"}
	}
	// The account can be deleted between the read and the update.
	if err := repo.SetUserSuspended(ctx, db, target.ID, reason); errors.Is(err, sql.ErrNoRows) {
		return model.User{}, &LifecycleError{Status: 404, Message: "not found"}
	} else if err != nil {
		return model.User{}, err
	}
	return target, nil
}
